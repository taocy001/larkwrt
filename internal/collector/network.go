package collector

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// IfaceCounters holds raw /proc/net/dev byte/packet counters.
type IfaceCounters struct {
	RxBytes   uint64
	TxBytes   uint64
	RxPackets uint64
	TxPackets uint64
}

// IfaceSample is a timestamped counter snapshot used for rate calculation.
type IfaceSample struct {
	Counters map[string]IfaceCounters
}

func ReadIfaceCounters() (IfaceSample, error) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return IfaceSample{}, err
	}
	defer f.Close()
	return readIfaceCountersFrom(f)
}

func readIfaceCountersFrom(r io.Reader) (IfaceSample, error) {
	sample := IfaceSample{Counters: make(map[string]IfaceCounters)}
	sc := bufio.NewScanner(r)
	sc.Scan() // skip header line 1
	sc.Scan() // skip header line 2
	for sc.Scan() {
		line := sc.Text()
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		name := strings.TrimSpace(line[:colon])
		fields := strings.Fields(line[colon+1:])
		if len(fields) < 10 {
			continue
		}
		rx, _ := strconv.ParseUint(fields[0], 10, 64)
		rxp, _ := strconv.ParseUint(fields[1], 10, 64)
		tx, _ := strconv.ParseUint(fields[8], 10, 64)
		txp, _ := strconv.ParseUint(fields[9], 10, 64)
		sample.Counters[name] = IfaceCounters{
			RxBytes: rx, TxBytes: tx,
			RxPackets: rxp, TxPackets: txp,
		}
	}
	return sample, nil
}

// IfaceInfo holds interface metadata from `ip -j link`.
type IfaceInfo struct {
	Name  string
	State string // "UP" | "DOWN" | "UNKNOWN"
	MAC   string
	MTU   int
}

type ipLinkEntry struct {
	Ifname    string `json:"ifname"`
	OperState string `json:"operstate"`
	Address   string `json:"address"`
	MTU       int    `json:"mtu"`
}

func ReadIfaceInfos() ([]IfaceInfo, error) {
	out, _, err := runCmd("ip", "-j", "link")
	if err == nil {
		if infos, err2 := parseIPLink(out); err2 == nil {
			return infos, nil
		}
	}
	// BusyBox fallback: plain text
	out, _, err = runCmd("ip", "link")
	if err != nil {
		return readIfaceInfoFallback(), nil
	}
	return parseIPLinkText(out), nil
}

func parseIPLink(jsonStr string) ([]IfaceInfo, error) {
	var entries []ipLinkEntry
	if err := json.Unmarshal([]byte(jsonStr), &entries); err != nil {
		return nil, err
	}
	infos := make([]IfaceInfo, 0, len(entries))
	for _, e := range entries {
		infos = append(infos, IfaceInfo{
			Name: e.Ifname, State: e.OperState, MAC: e.Address, MTU: e.MTU,
		})
	}
	return infos, nil
}

func parseIPLinkText(text string) []IfaceInfo {
	var result []IfaceInfo
	var cur *IfaceInfo
	for _, line := range strings.Split(text, "\n") {
		if len(line) == 0 {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			if cur != nil {
				result = append(result, *cur)
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				cur = nil
				continue
			}
			name := strings.TrimSuffix(fields[1], ":")
			if idx := strings.Index(name, "@"); idx >= 0 {
				name = name[:idx]
			}
			state := "UNKNOWN"
			mtu := 0
			for i, f := range fields {
				if f == "state" && i+1 < len(fields) {
					state = fields[i+1]
				}
				if f == "mtu" && i+1 < len(fields) {
					fmt.Sscanf(fields[i+1], "%d", &mtu)
				}
			}
			cur = &IfaceInfo{Name: name, State: state, MTU: mtu}
		} else if cur != nil {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[0] == "link/ether" {
				cur.MAC = fields[1]
			}
		}
	}
	if cur != nil {
		result = append(result, *cur)
	}
	return result
}

func readIfaceInfoFallback() []IfaceInfo {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []IfaceInfo
	sc := bufio.NewScanner(f)
	sc.Scan()
	sc.Scan()
	for sc.Scan() {
		line := sc.Text()
		colon := strings.Index(line, ":")
		if colon < 0 {
			continue
		}
		out = append(out, IfaceInfo{Name: strings.TrimSpace(line[:colon])})
	}
	return out
}

// AddrInfo holds IP addresses per interface from `ip -j addr`.
type AddrInfo struct {
	Iface string
	Addrs []string // CIDR notation
}

type ipAddrEntry struct {
	Ifname    string `json:"ifname"`
	AddrInfos []struct {
		Local     string `json:"local"`
		Prefixlen int    `json:"prefixlen"`
		Family    string `json:"family"`
	} `json:"addr_info"`
}

func ReadAddrInfos() ([]AddrInfo, error) {
	out, _, err := runCmd("ip", "-j", "addr")
	if err == nil {
		if infos, err2 := parseIPAddr(out); err2 == nil {
			return infos, nil
		}
	}
	// BusyBox fallback: plain text
	out, _, err = runCmd("ip", "addr")
	if err != nil {
		return nil, fmt.Errorf("ip addr: %w", err)
	}
	return parseIPAddrText(out), nil
}

func parseIPAddrText(text string) []AddrInfo {
	var result []AddrInfo
	var cur *AddrInfo
	for _, line := range strings.Split(text, "\n") {
		if len(line) == 0 {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			if cur != nil {
				result = append(result, *cur)
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				cur = nil
				continue
			}
			name := strings.TrimSuffix(fields[1], ":")
			if idx := strings.Index(name, "@"); idx >= 0 {
				name = name[:idx]
			}
			cur = &AddrInfo{Iface: name}
		} else if cur != nil {
			fields := strings.Fields(line)
			if len(fields) >= 2 && (fields[0] == "inet" || fields[0] == "inet6") {
				cur.Addrs = append(cur.Addrs, fields[1])
			}
		}
	}
	if cur != nil {
		result = append(result, *cur)
	}
	return result
}

func parseIPAddr(jsonStr string) ([]AddrInfo, error) {
	var entries []ipAddrEntry
	if err := json.Unmarshal([]byte(jsonStr), &entries); err != nil {
		return nil, err
	}
	infos := make([]AddrInfo, 0, len(entries))
	for _, e := range entries {
		ai := AddrInfo{Iface: e.Ifname}
		for _, a := range e.AddrInfos {
			if a.Family == "inet" || a.Family == "inet6" {
				ai.Addrs = append(ai.Addrs, fmt.Sprintf("%s/%d", a.Local, a.Prefixlen))
			}
		}
		infos = append(infos, ai)
	}
	return infos, nil
}

// RouteEntry holds a single route from `ip -j route`.
type RouteEntry struct {
	Dst     string
	Gateway string
	Dev     string
	Proto   string
}

type ipRouteEntry struct {
	Dst     string `json:"dst"`
	Gateway string `json:"gateway"`
	Dev     string `json:"dev"`
	Proto   string `json:"protocol"`
}

func ReadRoutes() ([]RouteEntry, error) {
	out, _, err := runCmd("ip", "-j", "route")
	if err == nil {
		if routes, err2 := parseIPRoute(out); err2 == nil {
			return routes, nil
		}
	}
	// BusyBox fallback: plain text
	out, _, err = runCmd("ip", "route")
	if err != nil {
		return nil, fmt.Errorf("ip route: %w", err)
	}
	return parseIPRouteText(out), nil
}

func parseIPRouteText(text string) []RouteEntry {
	var result []RouteEntry
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		r := RouteEntry{}
		r.Dst = fields[0]
		for i, f := range fields {
			switch f {
			case "via":
				if i+1 < len(fields) {
					r.Gateway = fields[i+1]
				}
			case "dev":
				if i+1 < len(fields) {
					r.Dev = fields[i+1]
				}
			case "proto":
				if i+1 < len(fields) {
					r.Proto = fields[i+1]
				}
			}
		}
		result = append(result, r)
	}
	return result
}

func parseIPRoute(jsonStr string) ([]RouteEntry, error) {
	var entries []ipRouteEntry
	if err := json.Unmarshal([]byte(jsonStr), &entries); err != nil {
		return nil, err
	}
	routes := make([]RouteEntry, len(entries))
	for i, e := range entries {
		routes[i] = RouteEntry{Dst: e.Dst, Gateway: e.Gateway, Dev: e.Dev, Proto: e.Proto}
	}
	return routes, nil
}
