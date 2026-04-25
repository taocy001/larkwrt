package collector

import (
	"strings"
)

// WifiInfo holds key stats from `iwinfo <iface> info`.
type WifiInfo struct {
	Iface     string
	SSID      string
	Channel   string
	Frequency string
	TxPower   string
	Signal    string
	Mode      string // "Master" | "Client" | ...
	Encryption string
}

// ReadWifiInfos tries common wireless interface names.
func ReadWifiInfos() []WifiInfo {
	candidates := probeWifiIfaces()
	var out []WifiInfo
	for _, iface := range candidates {
		info := readIwinfo(iface)
		if info != nil {
			out = append(out, *info)
		}
	}
	return out
}

func probeWifiIfaces() []string {
	// Common OpenWrt wireless interface names
	candidates := []string{"wlan0", "wlan1", "ath0", "ath1", "ra0", "ra1"}

	// Also check /proc/net/dev for any wlan* / ath* / ra*
	out, _, err := runCmd("ip", "-j", "link")
	if err == nil {
		// Simple string scan — avoid a full JSON parse here
		for _, word := range strings.Fields(out) {
			w := strings.Trim(word, `",:{}`)
			if isWifiIface(w) {
				candidates = append(candidates, w)
			}
		}
	}

	// Deduplicate
	seen := make(map[string]struct{})
	var deduped []string
	for _, c := range candidates {
		if _, ok := seen[c]; !ok {
			seen[c] = struct{}{}
			deduped = append(deduped, c)
		}
	}
	return deduped
}

func isWifiIface(name string) bool {
	for _, prefix := range []string{"wlan", "ath", "ra", "wl"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func readIwinfo(iface string) *WifiInfo {
	out, _, err := runCmd("iwinfo", iface, "info")
	if err != nil || out == "" {
		return nil
	}
	info := &WifiInfo{Iface: iface}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "ESSID:"):
			info.SSID = strings.Trim(strings.TrimPrefix(line, "ESSID:"), ` "`)
		case strings.HasPrefix(line, "Channel:"):
			// "Channel: 6 (2.437 GHz)"
			parts := strings.Fields(strings.TrimPrefix(line, "Channel:"))
			if len(parts) > 0 {
				info.Channel = parts[0]
			}
			if len(parts) > 1 {
				info.Frequency = strings.Trim(parts[1], "(")
			}
		case strings.HasPrefix(line, "Tx-Power:"):
			info.TxPower = strings.TrimSpace(strings.TrimPrefix(line, "Tx-Power:"))
		case strings.HasPrefix(line, "Signal:"):
			info.Signal = strings.TrimSpace(strings.TrimPrefix(line, "Signal:"))
		case strings.HasPrefix(line, "Mode:"):
			info.Mode = strings.TrimSpace(strings.TrimPrefix(line, "Mode:"))
		case strings.HasPrefix(line, "Encryption:"):
			info.Encryption = strings.TrimSpace(strings.TrimPrefix(line, "Encryption:"))
		}
	}
	if info.SSID == "" && info.Channel == "" {
		return nil // interface exists but no wifi data
	}
	return info
}
