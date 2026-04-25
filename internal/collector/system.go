package collector

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// CPUSample is a raw /proc/stat snapshot.
type CPUSample struct {
	User, Nice, System, Idle, IOWait, IRQ, SoftIRQ, Steal uint64
}

func (s CPUSample) total() uint64 {
	return s.User + s.Nice + s.System + s.Idle + s.IOWait + s.IRQ + s.SoftIRQ + s.Steal
}
func (s CPUSample) busy() uint64 { return s.total() - s.Idle - s.IOWait }

// CPUPercent computes CPU usage between two samples (0–100).
func CPUPercent(a, b CPUSample) float64 {
	dt := float64(b.total() - a.total())
	if dt == 0 {
		return 0
	}
	return float64(b.busy()-a.busy()) / dt * 100
}

func ReadCPUSample() (CPUSample, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return CPUSample{}, err
	}
	defer f.Close()
	return readCPUSampleFrom(f)
}

func readCPUSampleFrom(r io.Reader) (CPUSample, error) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "cpu ") {
			return parseCPULine(line)
		}
	}
	return CPUSample{}, fmt.Errorf("/proc/stat: cpu line not found")
}

func parseCPULine(line string) (CPUSample, error) {
	fields := strings.Fields(line)
	if len(fields) < 8 {
		return CPUSample{}, fmt.Errorf("unexpected cpu line: %q", line)
	}
	nums := make([]uint64, 8)
	for i := range nums {
		nums[i], _ = strconv.ParseUint(fields[i+1], 10, 64)
	}
	return CPUSample{
		User: nums[0], Nice: nums[1], System: nums[2], Idle: nums[3],
		IOWait: nums[4], IRQ: nums[5], SoftIRQ: nums[6], Steal: nums[7],
	}, nil
}

// MemInfo holds parsed /proc/meminfo values (kB).
type MemInfo struct {
	Total     uint64
	Available uint64
	Free      uint64
	Buffers   uint64
	Cached    uint64
}

func (m MemInfo) UsedMB() int  { return int((m.Total - m.Available) / 1024) }
func (m MemInfo) TotalMB() int { return int(m.Total / 1024) }
func (m MemInfo) UsedPct() float64 {
	if m.Total == 0 {
		return 0
	}
	return float64(m.Total-m.Available) / float64(m.Total) * 100
}

func ReadMemInfo() (MemInfo, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return MemInfo{}, err
	}
	defer f.Close()
	return readMemInfoFrom(f)
}

func readMemInfoFrom(r io.Reader) (MemInfo, error) {
	info := MemInfo{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		val, _ := strconv.ParseUint(fields[1], 10, 64)
		switch fields[0] {
		case "MemTotal:":
			info.Total = val
		case "MemAvailable:":
			info.Available = val
		case "MemFree:":
			info.Free = val
		case "Buffers:":
			info.Buffers = val
		case "Cached:":
			info.Cached = val
		}
	}
	return info, nil
}

type LoadAvg struct {
	One, Five, Fifteen float64
}

func ReadLoadAvg() (LoadAvg, error) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return LoadAvg{}, err
	}
	return parseLoadAvg(string(data))
}

func parseLoadAvg(raw string) (LoadAvg, error) {
	fields := strings.Fields(raw)
	if len(fields) < 3 {
		return LoadAvg{}, fmt.Errorf("unexpected /proc/loadavg: %q", raw)
	}
	var la LoadAvg
	fmt.Sscanf(fields[0], "%f", &la.One)
	fmt.Sscanf(fields[1], "%f", &la.Five)
	fmt.Sscanf(fields[2], "%f", &la.Fifteen)
	return la, nil
}

func ReadUptime() (time.Duration, error) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, err
	}
	return parseUptime(string(data))
}

func parseUptime(raw string) (time.Duration, error) {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty uptime")
	}
	secs, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, err
	}
	return time.Duration(secs * float64(time.Second)), nil
}

type TempReading struct {
	Zone  string
	TempC float64
}

func ReadTemperatures() []TempReading {
	var out []TempReading
	entries, _ := os.ReadDir("/sys/class/thermal")
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "thermal_zone") {
			continue
		}
		raw, err := os.ReadFile("/sys/class/thermal/" + e.Name() + "/temp")
		if err != nil {
			continue
		}
		val, err := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
		if err != nil {
			continue
		}
		out = append(out, TempReading{Zone: e.Name(), TempC: val / 1000})
	}
	return out
}

// DiskInfo holds usage info for the root filesystem.
type DiskInfo struct {
	TotalMB int
	UsedMB  int
	FreeMB  int
}

func (d DiskInfo) UsedPct() float64 {
	if d.TotalMB == 0 {
		return 0
	}
	return float64(d.UsedMB) / float64(d.TotalMB) * 100
}

func ReadDiskInfo() (DiskInfo, error) {
	out, _, err := runCmd("df", "-m", "/overlay")
	if err != nil {
		out, _, err = runCmd("df", "-m", "/")
		if err != nil {
			return DiskInfo{}, err
		}
	}
	return parseDFOutput(out)
}

func parseDFOutput(out string) (DiskInfo, error) {
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[0] == "Filesystem" {
			continue
		}
		total, _ := strconv.Atoi(fields[1])
		used, _ := strconv.Atoi(fields[2])
		free, _ := strconv.Atoi(fields[3])
		return DiskInfo{TotalMB: total, UsedMB: used, FreeMB: free}, nil
	}
	return DiskInfo{}, fmt.Errorf("df output unexpected: %q", out)
}
