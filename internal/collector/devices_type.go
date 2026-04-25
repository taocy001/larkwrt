package collector

import (
	"bufio"
	"os"
	"strings"
	"sync"
	"time"
)

// ── Device type inference ─────────────────────────────────────────────────────

// DeviceType classifies a device into a broad category.
type DeviceType string

const (
	TypePhone   DeviceType = "phone"
	TypeTablet  DeviceType = "tablet"
	TypeLaptop  DeviceType = "laptop"
	TypeDesktop DeviceType = "desktop"
	TypeTV      DeviceType = "tv"
	TypeRouter  DeviceType = "router"
	TypeNAS     DeviceType = "nas"
	TypePrinter DeviceType = "printer"
	TypeSBC     DeviceType = "sbc"    // Raspberry Pi etc.
	TypeVM      DeviceType = "vm"     // Virtual machine
	TypeIoT     DeviceType = "iot"
	TypeGaming  DeviceType = "gaming"
	TypeApple   DeviceType = "apple"  // Apple device, type ambiguous
	TypeUnknown DeviceType = "unknown"
)

// Icon returns the emoji icon for the device type.
func (t DeviceType) Icon() string {
	switch t {
	case TypePhone, TypeTablet:
		return "📱"
	case TypeLaptop:
		return "💻"
	case TypeDesktop:
		return "🖥️"
	case TypeTV:
		return "📺"
	case TypeRouter:
		return "📡"
	case TypeNAS:
		return "🗄️"
	case TypePrinter:
		return "🖨️"
	case TypeSBC:
		return "🍓"
	case TypeVM:
		return "☁️"
	case TypeIoT:
		return "🏠"
	case TypeGaming:
		return "🎮"
	case TypeApple:
		return "🍎"
	default:
		return "❓"
	}
}

// String returns a human-readable label.
func (t DeviceType) String() string {
	switch t {
	case TypePhone:
		return "手机"
	case TypeTablet:
		return "平板"
	case TypeLaptop:
		return "笔记本"
	case TypeDesktop:
		return "台式机"
	case TypeTV:
		return "电视"
	case TypeRouter:
		return "路由/AP"
	case TypeNAS:
		return "NAS"
	case TypePrinter:
		return "打印机"
	case TypeSBC:
		return "单板机"
	case TypeVM:
		return "虚拟机"
	case TypeIoT:
		return "IoT"
	case TypeGaming:
		return "游戏机"
	case TypeApple:
		return "Apple"
	default:
		return "未知"
	}
}

// InferDeviceType guesses the device type from hostname and vendor name.
func InferDeviceType(hostname, vendor string) DeviceType {
	h := strings.ToLower(hostname)
	v := strings.ToLower(vendor)

	// ── Hostname-based (most reliable) ──────────────────────────────────────
	switch {
	case anyPrefix(h, "iphone"):
		return TypePhone
	case anyPrefix(h, "ipad"):
		return TypeTablet
	case anyInfix(h, "macbook"):
		return TypeLaptop
	case anyInfix(h, "imac", "mac-mini", "mac-pro", "mac-studio"):
		return TypeDesktop
	case anyInfix(h, "appletv", "apple-tv"):
		return TypeTV
	case anyInfix(h, "homepod"):
		return TypeIoT
	case anyInfix(h, "android"):
		return TypePhone
	case anyPrefix(h, "desktop-", "desktop_", "pc-"):
		return TypeDesktop
	case anyInfix(h, "thinkpad", "ideapad", "thinkbook", "yoga", "latitude", "pavilion", "elitebook"):
		return TypeLaptop
	case anyInfix(h, "diskstation"):
		return TypeNAS
	case anyInfix(h, "qnap", "nas"):
		return TypeNAS
	case anyInfix(h, "openwrt", "ubnt", "unifi", "mikrotik"):
		return TypeRouter
	case anyPrefix(h, "raspberrypi", "raspberry-pi"):
		return TypeSBC
	case anyInfix(h, "firetv", "fire-tv", "fire_tv", "androidtv"):
		return TypeTV
	case anyInfix(h, "chromecast"):
		return TypeTV
	case anyInfix(h, "echo-", "echo_"):
		return TypeIoT
	case anyInfix(h, "nintendo-switch", "switch-"):
		return TypeGaming
	case anyInfix(h, "playstation", "ps4-", "ps5-"):
		return TypeGaming
	case anyPrefix(h, "xbox-"):
		return TypeGaming
	case anyInfix(h, "printer", "printserver"):
		return TypePrinter
	case anyInfix(h, "proxmox", "pve-", "qemu"):
		return TypeVM
	}

	// ── Vendor-based (secondary) ─────────────────────────────────────────────
	switch {
	case anyInfix(v, "tp-link", "tplink", "netgear", "mikrotik", "ubiquiti",
		"aruba", "d-link", "dlink", "zyxel", "cisco", "ruckus", "linksys"):
		return TypeRouter
	case anyInfix(v, "zte"):
		return TypeRouter // ZTE mostly makes routers/modems in home context
	case anyInfix(v, "synology", "qnap", "drobo", "buffalo"):
		return TypeNAS
	case anyInfix(v, "raspberry"):
		return TypeSBC
	case anyInfix(v, "proxmox"):
		return TypeVM
	case anyInfix(v, "nintendo"):
		return TypeGaming
	case anyInfix(v, "sony"):
		return TypeGaming // PlayStation is most common Sony device on home net
	case anyInfix(v, "xiaomi", "oppo", "vivo", "oneplus"):
		return TypePhone
	case anyInfix(v, "samsung"):
		return TypePhone
	case anyInfix(v, "huawei"):
		return TypePhone
	case anyInfix(v, "apple"):
		return TypeApple // ambiguous: phone/tablet/laptop/TV
	case anyInfix(v, "intel", "realtek"):
		return TypeLaptop // most common for embedded NIC
	case anyInfix(v, "dell", "lenovo", "hp inc", "hewlett", "acer", "msi", "asus"):
		return TypeLaptop
	case anyInfix(v, "amazon"):
		return TypeIoT
	case anyInfix(v, "google"):
		return TypePhone // most likely Pixel
	case anyInfix(v, "philips", "yeelight", "tuya", "espressif", "shelly"):
		return TypeIoT
	}

	return TypeUnknown
}

func anyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func anyInfix(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// ── /etc/ethers ───────────────────────────────────────────────────────────────

// readEthers reads /etc/ethers for static MAC→hostname mappings.
// Format: <MAC>  <hostname or IP>
func readEthers() map[string]string {
	f, err := os.Open("/etc/ethers")
	if err != nil {
		return nil
	}
	defer f.Close()
	out := make(map[string]string)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		mac := strings.ToLower(fields[0])
		name := fields[1]
		// Skip if the "hostname" is actually an IP address
		if strings.Count(name, ".") == 3 {
			continue
		}
		// Strip domain suffix (hostname.lan → hostname)
		if idx := strings.Index(name, "."); idx > 0 {
			name = name[:idx]
		}
		out[mac] = name
	}
	return out
}

// ── Concurrent name enrichment ────────────────────────────────────────────────

// enrichDeviceNames fills Hostname for devices that have none, using:
// 1. mDNS cache (instant, no network call)
// 2. reverse DNS via local dnsmasq (async, 1s timeout)
func enrichDeviceNames(devices []Device) {
	needsEnrich := false
	for i := range devices {
		if devices[i].Hostname != "" {
			continue
		}
		// Check mDNS cache first (free)
		if name := MDNSHostname(devices[i].IP); name != "" {
			devices[i].Hostname = name
			continue
		}
		needsEnrich = true
	}
	if !needsEnrich {
		return
	}

	var wg sync.WaitGroup
	for i := range devices {
		if devices[i].Hostname != "" {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if name := queryLocalDNS(devices[i].IP); name != "" {
				devices[i].Hostname = name
			}
		}(i)
	}
	wg.Wait()
}

// queryLocalDNS does a PTR lookup via local dnsmasq (127.0.0.1).
func queryLocalDNS(ip string) string {
	out, _, err := runCmdWithTimeout(1*time.Second, "nslookup", ip, "127.0.0.1")
	if err != nil || out == "" {
		return ""
	}
	// BusyBox nslookup output:
	// Name:      hostname.lan
	// Address 1: x.x.x.x hostname.lan
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Name:") {
			name := strings.TrimSpace(strings.TrimPrefix(line, "Name:"))
			// Strip trailing domain
			if idx := strings.Index(name, "."); idx > 0 {
				name = name[:idx]
			}
			return name
		}
	}
	return ""
}
