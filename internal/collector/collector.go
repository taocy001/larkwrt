package collector

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"

	"larkwrt/internal/events"

	"github.com/rs/zerolog/log"
)

// Snapshot is a complete point-in-time view of the router state.
type Snapshot struct {
	At         time.Time
	CPU        float64      // percent
	Mem        MemInfo
	Load       LoadAvg
	Uptime     time.Duration
	Disk       DiskInfo
	Temps      []TempReading
	Ifaces     []IfaceInfo
	Addrs      []AddrInfo
	NetRates   map[string]NetRate // iface → bytes/s
	Routes     []RouteEntry
	Devices    []Device
	Wifi       []WifiInfo
}

// NetRate holds per-interface traffic rates (bytes/s).
type NetRate struct {
	RxBps float64
	TxBps float64
}

// Collector orchestrates all metric collection and emits events on changes.
type Collector struct {
	fastInterval time.Duration
	slowInterval time.Duration
	bus          *events.Bus
	lanIface     string
	db           *DevDB

	mu       sync.RWMutex
	current  Snapshot
	lastCPU  CPUSample
	lastNet  IfaceSample
	lastAt   time.Time

	// state for event detection
	prevDevices    map[string]Device // MAC → device
	prevWANIPs     map[string]string // iface → IP
	prevIfaceState map[string]string // iface → state
	cpuHighSince   time.Time
	prevUptime     time.Duration
}

func New(fastInterval, slowInterval time.Duration, bus *events.Bus, lanIface string, db *DevDB) *Collector {
	return &Collector{
		fastInterval:   fastInterval,
		slowInterval:   slowInterval,
		bus:            bus,
		lanIface:       lanIface,
		db:             db,
		prevDevices:    make(map[string]Device),
		prevWANIPs:     make(map[string]string),
		prevIfaceState: make(map[string]string),
	}
}

func (c *Collector) Start(stop <-chan struct{}) {
	// Start passive mDNS listener for device name enrichment
	go StartMDNSListener(stop)

	// prime CPU and net counters
	c.lastCPU, _ = ReadCPUSample()
	c.lastNet, _ = ReadIfaceCounters()
	c.lastAt = time.Now()

	fastTick := time.NewTicker(c.fastInterval)
	slowTick := time.NewTicker(c.slowInterval)
	defer fastTick.Stop()
	defer slowTick.Stop()

	// collect slow metrics immediately on start
	c.collectSlow()
	// collect fast metrics to populate rates
	c.collectFast()

	for {
		select {
		case <-stop:
			return
		case <-fastTick.C:
			c.collectFast()
		case <-slowTick.C:
			c.collectSlow()
		}
	}
}

func (c *Collector) Current() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.current
}

// ── collection helpers ────────────────────────────────────────────────────────

func (c *Collector) collectFast() {
	now := time.Now()
	elapsed := now.Sub(c.lastAt).Seconds()
	if elapsed <= 0 {
		elapsed = 1
	}

	cpu2, err := ReadCPUSample()
	if err != nil {
		log.Warn().Err(err).Msg("read cpu")
	}
	cpuPct := CPUPercent(c.lastCPU, cpu2)
	c.lastCPU = cpu2

	net2, err := ReadIfaceCounters()
	if err != nil {
		log.Warn().Err(err).Msg("read net counters")
	}
	rates := computeRates(c.lastNet, net2, elapsed)
	c.lastNet = net2
	c.lastAt = now

	mem, _ := ReadMemInfo()
	load, _ := ReadLoadAvg()
	uptime, _ := ReadUptime()

	c.mu.Lock()
	c.current.At = now
	c.current.CPU = cpuPct
	c.current.Mem = mem
	c.current.Load = load
	c.current.Uptime = uptime
	c.current.NetRates = rates
	c.mu.Unlock()

	c.detectCPUAlert(cpuPct)
	c.detectMemAlert(mem)
}

func (c *Collector) collectSlow() {
	disk, _ := ReadDiskInfo()
	temps := ReadTemperatures()
	ifaces, _ := ReadIfaceInfos()
	addrs, _ := ReadAddrInfos()
	routes, _ := ReadRoutes()
	devices := ReadDevices(c.lanIface)
	wifi := ReadWifiInfos()

	c.mu.Lock()
	c.current.Disk = disk
	c.current.Temps = temps
	c.current.Ifaces = ifaces
	c.current.Addrs = addrs
	c.current.Routes = routes
	c.current.Devices = devices
	c.current.Wifi = wifi
	uptime := c.current.Uptime
	c.mu.Unlock()

	if c.db != nil {
		c.db.UpsertDevices(devices)
	}
	c.detectDeviceChanges(devices)
	c.detectWANIPChanges(addrs)
	c.detectIfaceChanges(ifaces)
	c.detectReboot(uptime)
}

// ── event detection ───────────────────────────────────────────────────────────

func (c *Collector) detectCPUAlert(pct float64) {
	const threshold = 85.0
	if pct >= threshold {
		if c.cpuHighSince.IsZero() {
			c.cpuHighSince = time.Now()
		} else if time.Since(c.cpuHighSince) >= 60*time.Second {
			c.bus.Publish(events.Event{
				Type: events.EvHighCPU,
				Payload: events.CPUPayload{
					Percent:  pct,
					Duration: time.Since(c.cpuHighSince),
				},
				At: time.Now(),
			})
			c.cpuHighSince = time.Now()
		}
	} else {
		c.cpuHighSince = time.Time{}
	}
}

func (c *Collector) detectMemAlert(m MemInfo) {
	if m.UsedPct() >= 90 {
		c.bus.Publish(events.Event{
			Type: events.EvHighMemory,
			Payload: events.MemPayload{
				Percent: m.UsedPct(),
				FreeMB:  int(m.Free / 1024),
			},
			At: time.Now(),
		})
	}
}

func (c *Collector) detectDeviceChanges(current []Device) {
	cur := make(map[string]Device, len(current))
	for _, d := range current {
		cur[strings.ToLower(d.MAC)] = d
	}

	// new devices
	for mac, d := range cur {
		if _, existed := c.prevDevices[mac]; !existed {
			c.bus.Publish(events.Event{
				Type:    events.EvDeviceJoin,
				Payload: events.DevicePayload{MAC: d.MAC, IP: d.IP, Hostname: d.Hostname, Vendor: d.Vendor},
				At:      time.Now(),
			})
		}
	}
	// gone devices
	for mac, d := range c.prevDevices {
		if _, still := cur[mac]; !still {
			c.bus.Publish(events.Event{
				Type:    events.EvDeviceLeave,
				Payload: events.DevicePayload{MAC: d.MAC, IP: d.IP, Hostname: d.Hostname, Vendor: d.Vendor},
				At:      time.Now(),
			})
		}
	}
	c.prevDevices = cur
}

func (c *Collector) detectWANIPChanges(addrs []AddrInfo) {
	wanIfaces := []string{"eth0", "eth1", "pppoe-wan", "wan", "wan6"}
	for _, ai := range addrs {
		if !isWANIface(ai.Iface, wanIfaces) {
			continue
		}
		if len(ai.Addrs) == 0 {
			continue
		}
		newIP := ai.Addrs[0]
		oldIP := c.prevWANIPs[ai.Iface]
		if oldIP != "" && oldIP != newIP {
			c.bus.Publish(events.Event{
				Type: events.EvWANIPChange,
				Payload: events.WANIPPayload{
					OldIP: oldIP, NewIP: newIP, Iface: ai.Iface,
				},
				At: time.Now(),
			})
		}
		c.prevWANIPs[ai.Iface] = newIP
	}
}

func (c *Collector) detectIfaceChanges(ifaces []IfaceInfo) {
	for _, iface := range ifaces {
		prev := c.prevIfaceState[iface.Name]
		cur := strings.ToUpper(iface.State)
		if prev != "" && prev != cur {
			evType := events.EvIfaceDown
			if cur == "UP" {
				evType = events.EvIfaceUp
			}
			c.bus.Publish(events.Event{
				Type:    evType,
				Payload: events.IfacePayload{Name: iface.Name, State: strings.ToLower(cur)},
				At:      time.Now(),
			})
		}
		c.prevIfaceState[iface.Name] = cur
	}
}

func (c *Collector) detectReboot(uptime time.Duration) {
	prev := c.prevUptime
	c.prevUptime = uptime
	// uptime reset to < 2 min signals a fresh boot
	if prev > 5*time.Minute && uptime < 2*time.Minute {
		c.bus.Publish(events.Event{Type: events.EvRebootDetected, At: time.Now()})
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

func computeRates(a, b IfaceSample, elapsed float64) map[string]NetRate {
	rates := make(map[string]NetRate, len(b.Counters))
	for name, bc := range b.Counters {
		ac := a.Counters[name]
		rates[name] = NetRate{
			RxBps: float64(bc.RxBytes-ac.RxBytes) / elapsed,
			TxBps: float64(bc.TxBytes-ac.TxBytes) / elapsed,
		}
	}
	return rates
}

func isWANIface(name string, wanList []string) bool {
	for _, w := range wanList {
		if name == w {
			return true
		}
	}
	return strings.HasPrefix(name, "ppp") || name == "wan"
}

// runCmd is a thin wrapper around exec used by sub-packages.
func runCmd(name string, args ...string) (string, string, error) {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return "", err.Error(), err
	}
	return strings.TrimSpace(string(out)), "", nil
}

// runCmdWithTimeout runs a command with a timeout, returning stdout.
func runCmdWithTimeout(timeout time.Duration, name string, args ...string) (string, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return "", err.Error(), err
	}
	return strings.TrimSpace(string(out)), "", nil
}
