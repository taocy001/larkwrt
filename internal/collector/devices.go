package collector

import (
	"bufio"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// Device represents a LAN-connected device.
type Device struct {
	IP         string
	MAC        string
	Hostname   string
	Vendor     string     // from MAC OUI lookup
	Iface      string
	LeaseEnd   time.Time
	Type       DeviceType // inferred from hostname + vendor
	RandomMAC  bool       // locally administered (privacy/randomized MAC)
	Note       string     // user-supplied label from DevDB
}

// DisplayName returns the best available human-readable name.
// Note takes precedence over hostname when set by the user.
func (d Device) DisplayName() string {
	if d.Note != "" {
		return d.Note
	}
	if d.Hostname != "" {
		return d.Hostname
	}
	if d.Vendor != "" {
		return d.Vendor
	}
	return d.MAC
}

// ReadDevices merges ARP table and DHCP leases, filtering to the given LAN interface.
// If the filtered result is empty (interface mismatch), falls back to all interfaces.
func ReadDevices(lanIface string) []Device {
	arp := readARP(lanIface)
	if len(arp) == 0 && lanIface != "" {
		arp = readARP("") // interface name mismatch — try all
	}
	leases := readDHCPLeases()
	return mergeDevices(arp, leases)
}

// OUILookup exports the vendor name for a MAC address (for use in feishu package).
func OUILookup(mac string) string { return ouiLookup(mac) }

func mergeDevices(arp, leases []Device) []Device {
	leaseByMAC := make(map[string]Device, len(leases))
	for _, l := range leases {
		leaseByMAC[strings.ToLower(l.MAC)] = l
	}

	ethers := readEthers() // static MAC→hostname from /etc/ethers

	seen := make(map[string]bool)
	var out []Device

	// Prefer ARP entries (have iface info), augmented by lease/ethers info
	for _, a := range arp {
		key := strings.ToLower(a.MAC)
		seen[key] = true
		d := a
		if l, ok := leaseByMAC[key]; ok {
			if l.Hostname != "" {
				d.Hostname = l.Hostname
			}
			d.LeaseEnd = l.LeaseEnd
		}
		// /etc/ethers overrides DHCP hostname
		if name, ok := ethers[key]; ok && name != "" {
			d.Hostname = name
		}
		d.Vendor = ouiLookup(a.MAC)
		d.RandomMAC = isRandomMAC(a.MAC)
		d.Type = InferDeviceType(d.Hostname, d.Vendor)
		out = append(out, d)
	}
	// Add DHCP-only entries (device has lease but no ARP entry yet)
	for _, l := range leases {
		key := strings.ToLower(l.MAC)
		if seen[key] {
			continue
		}
		if name, ok := ethers[key]; ok && name != "" {
			l.Hostname = name
		}
		l.Vendor = ouiLookup(l.MAC)
		l.RandomMAC = isRandomMAC(l.MAC)
		l.Type = InferDeviceType(l.Hostname, l.Vendor)
		out = append(out, l)
	}

	// Enrich hostnames via local DNS for devices still missing names
	enrichDeviceNames(out)

	// Re-infer type for devices that just got a hostname
	for i := range out {
		if out[i].Type == TypeUnknown && out[i].Hostname != "" {
			out[i].Type = InferDeviceType(out[i].Hostname, out[i].Vendor)
		}
	}

	return out
}

// isRandomMAC returns true when the MAC is locally administered (privacy/randomized).
// The second-least-significant bit of the first octet indicates "locally administered".
func isRandomMAC(mac string) bool {
	if len(mac) < 2 {
		return false
	}
	// Parse first octet
	var first byte
	for _, c := range mac[:2] {
		first <<= 4
		switch {
		case c >= '0' && c <= '9':
			first |= byte(c - '0')
		case c >= 'a' && c <= 'f':
			first |= byte(c-'a') + 10
		case c >= 'A' && c <= 'F':
			first |= byte(c-'A') + 10
		}
	}
	return first&0x02 != 0
}

func readARP(lanIface string) []Device {
	f, err := os.Open("/proc/net/arp")
	if err != nil {
		return nil
	}
	defer f.Close()
	return parseARP(f, lanIface)
}

// parseARP parses /proc/net/arp, keeping only entries with Flags 0x2 (complete).
// If lanIface is non-empty, only entries on that interface are returned.
func parseARP(r io.Reader, lanIface string) []Device {
	var out []Device
	sc := bufio.NewScanner(r)
	sc.Scan() // header
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 6 {
			continue
		}
		ip, flags, mac, iface := fields[0], fields[2], fields[3], fields[5]
		if flags != "0x2" {
			continue
		}
		if mac == "00:00:00:00:00:00" {
			continue
		}
		if lanIface != "" && iface != lanIface {
			continue
		}
		out = append(out, Device{IP: ip, MAC: mac, Iface: iface})
	}
	return out
}

func readDHCPLeases() []Device {
	paths := []string{"/tmp/dhcp.leases", "/var/lib/misc/dnsmasq.leases"}
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		devices := parseDHCPLeases(f)
		f.Close()
		return devices
	}
	return nil
}

// parseDHCPLeases parses dnsmasq lease file format from any reader.
// Format: <expire_epoch> <mac> <ip> <hostname> <client_id>
func parseDHCPLeases(r io.Reader) []Device {
	var out []Device
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 {
			continue
		}
		expireEpoch, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		mac, ip, hostname := fields[1], fields[2], fields[3]
		if hostname == "*" {
			hostname = ""
		}
		out = append(out, Device{
			MAC:      mac,
			IP:       ip,
			Hostname: hostname,
			LeaseEnd: time.Unix(expireEpoch, 0),
		})
	}
	return out
}

// ouiLookup returns the vendor name for a MAC address using the first 3 octets.
func ouiLookup(mac string) string {
	if len(mac) < 8 {
		return ""
	}
	// normalize to "XX:XX:XX" uppercase
	key := strings.ToUpper(mac[:8])
	if v, ok := ouiTable[key]; ok {
		return v
	}
	return ""
}

// ouiTable maps OUI prefixes (XX:XX:XX) to vendor names.
// Focused on common consumer devices in China and globally.
var ouiTable = map[string]string{
	// Apple
	"00:1C:B3": "Apple", "00:1E:52": "Apple", "00:1F:F3": "Apple",
	"00:21:E9": "Apple", "00:25:00": "Apple", "00:26:B9": "Apple",
	"04:52:F3": "Apple", "04:D3:CF": "Apple", "08:70:45": "Apple",
	"0C:74:C2": "Apple", "0C:BC:9F": "Apple", "14:20:5E": "Apple",
	"18:AF:61": "Apple", "1C:1A:C0": "Apple", "20:78:F0": "Apple",
	"24:A0:74": "Apple", "28:6A:BA": "Apple", "2C:B4:3A": "Apple",
	"34:08:BC": "Apple", "38:F9:D3": "Apple", "3C:07:54": "Apple",
	"40:31:3C": "Apple", "40:A6:D9": "Apple", "44:2A:60": "Apple",
	"4C:57:CA": "Apple", "50:7A:55": "Apple", "54:4E:90": "Apple",
	"58:B0:35": "Apple", "5C:59:48": "Apple", "60:C5:47": "Apple",
	"64:5A:04": "Apple", "68:FB:7E": "Apple", "6C:72:E7": "Apple",
	"70:CD:60": "Apple", "74:81:14": "Apple", "7C:D1:C3": "Apple",
	"84:78:8B": "Apple", "88:19:08": "Apple", "8C:00:6D": "Apple",
	"90:60:F1": "Apple", "98:01:A7": "Apple", "9C:35:EB": "Apple",
	"A4:83:E7": "Apple", "A8:86:DD": "Apple", "A8:96:75": "Apple",
	"AC:29:3A": "Apple", "AC:87:A3": "Apple", "AC:BC:32": "Apple",
	"B4:18:D1": "Apple", "B8:17:C2": "Apple", "B8:44:D9": "Apple",
	"BC:54:36": "Apple", "C0:84:7A": "Apple", "C4:B3:01": "Apple",
	"C8:2A:14": "Apple", "C8:6F:1D": "Apple", "CC:08:8D": "Apple",
	"D0:03:4B": "Apple", "D4:61:DA": "Apple", "DC:2B:2A": "Apple",
	"E0:AC:CB": "Apple", "E4:CE:8F": "Apple", "E8:B2:AC": "Apple",
	"F0:DB:F8": "Apple", "F4:1B:A1": "Apple", "F4:F1:5A": "Apple",
	"FC:25:3F": "Apple", "FC:E9:98": "Apple",
	// Xiaomi / MIUI
	"00:9E:C8": "Xiaomi", "04:CF:8C": "Xiaomi", "08:21:EF": "Xiaomi",
	"10:2A:B3": "Xiaomi", "18:59:36": "Xiaomi", "20:82:C0": "Xiaomi",
	"28:6C:07": "Xiaomi", "28:D1:27": "Xiaomi", "34:80:B3": "Xiaomi",
	"38:A4:ED": "Xiaomi", "50:64:2B": "Xiaomi", "50:8F:4C": "Xiaomi",
	"58:44:98": "Xiaomi", "64:09:80": "Xiaomi", "78:11:DC": "Xiaomi",
	"8C:BE:BE": "Xiaomi", "98:FA:E3": "Xiaomi", "AC:F7:F3": "Xiaomi",
	"B0:E2:35": "Xiaomi", "BC:D0:74": "Xiaomi", "C4:0B:CB": "Xiaomi",
	"D4:97:0B": "Xiaomi", "F4:8E:38": "Xiaomi", "FC:64:BA": "Xiaomi",
	"FC:8B:97": "Xiaomi", "00:EC:0A": "Xiaomi", "24:CF:24": "Xiaomi",
	"30:1F:9A": "Xiaomi", "58:31:F1": "Xiaomi", "64:B4:73": "Xiaomi",
	"68:3E:26": "Xiaomi", "7C:1D:D9": "Xiaomi", "8C:53:C3": "Xiaomi",
	"AC:2B:6E": "Xiaomi", "D4:DA:21": "Xiaomi", "F0:B4:29": "Xiaomi",
	// Huawei
	"00:18:82": "Huawei", "00:46:4B": "Huawei", "00:9A:CD": "Huawei",
	"04:02:1F": "Huawei", "04:BD:70": "Huawei", "04:C0:6F": "Huawei",
	"0C:37:DC": "Huawei", "14:A5:1A": "Huawei", "18:66:DA": "Huawei",
	"1C:8E:5C": "Huawei", "28:31:52": "Huawei", "28:6E:D4": "Huawei",
	"2C:9D:1E": "Huawei", "38:F8:89": "Huawei", "3C:47:11": "Huawei",
	"48:AD:08": "Huawei", "48:DB:50": "Huawei", "4C:B1:CD": "Huawei",
	"4C:F8:EF": "Huawei", "54:51:1B": "Huawei", "5C:C3:07": "Huawei",
	"60:DE:44": "Huawei", "60:E7:01": "Huawei", "64:3E:8C": "Huawei",
	"70:72:CF": "Huawei", "74:1A:E0": "Huawei", "78:1D:BA": "Huawei",
	"7C:1C:68": "Huawei", "80:3F:5D": "Huawei", "8C:34:FD": "Huawei",
	"90:67:1C": "Huawei", "98:52:61": "Huawei", "9C:28:EF": "Huawei",
	"AC:E2:15": "Huawei", "B4:86:55": "Huawei", "C4:07:2F": "Huawei",
	"C4:9A:02": "Huawei", "C8:14:51": "Huawei", "D4:6D:50": "Huawei",
	"DC:D2:FC": "Huawei", "E4:68:A3": "Huawei", "E8:CD:2D": "Huawei",
	"F4:4C:7F": "Huawei", "F8:01:13": "Huawei", "F8:98:EF": "Huawei",
	// Samsung
	"00:12:47": "Samsung", "00:15:99": "Samsung", "00:16:32": "Samsung",
	"00:1A:8A": "Samsung", "08:08:C2": "Samsung", "0C:14:20": "Samsung",
	"1C:AF:F7": "Samsung", "20:55:31": "Samsung", "2C:AE:2B": "Samsung",
	"30:CD:A7": "Samsung", "38:AA:3C": "Samsung", "48:13:7E": "Samsung",
	"4C:BC:A5": "Samsung", "5C:F6:DC": "Samsung", "60:6B:FF": "Samsung",
	"68:48:98": "Samsung", "78:59:5E": "Samsung", "78:F8:82": "Samsung",
	"7C:61:93": "Samsung", "88:9B:39": "Samsung", "8C:F5:A3": "Samsung",
	"90:18:7C": "Samsung", "98:52:B1": "Samsung", "A0:82:1F": "Samsung",
	"A4:7B:9D": "Samsung", "AC:5F:3E": "Samsung", "BC:14:85": "Samsung",
	"C4:62:EA": "Samsung", "D8:57:EF": "Samsung", "E4:92:FB": "Samsung",
	"F4:09:D8": "Samsung", "F8:04:2E": "Samsung", "FC:A1:3E": "Samsung",
	// OPPO
	"1C:77:F6": "OPPO", "34:14:5F": "OPPO", "3C:CB:7C": "OPPO",
	"44:CA:00": "OPPO", "48:9A:E4": "OPPO", "50:D4:F7": "OPPO",
	"64:1C:AE": "OPPO", "6C:5C:14": "OPPO", "74:42:8B": "OPPO",
	"78:BB:A8": "OPPO", "84:0D:8E": "OPPO", "88:2A:5E": "OPPO",
	"90:D9:F7": "OPPO", "9C:BE:97": "OPPO", "A4:38:CC": "OPPO",
	"AC:32:97": "OPPO", "C0:26:DA": "OPPO", "D4:DC:CD": "OPPO",
	"E0:13:B6": "OPPO", "F4:60:E2": "OPPO", "FC:B4:63": "OPPO",
	// vivo
	"04:03:D6": "vivo", "08:4F:0A": "vivo", "24:4B:4E": "vivo",
	"34:7D:F6": "vivo", "58:48:12": "vivo", "5C:3A:45": "vivo",
	"68:15:75": "vivo", "7C:04:D0": "vivo", "88:30:8A": "vivo",
	"98:37:FC": "vivo", "A8:35:41": "vivo", "B4:4B:D2": "vivo",
	"D0:F6:CC": "vivo", "D8:C6:73": "vivo", "E8:68:E7": "vivo",
	// OnePlus
	"04:4E:AF": "OnePlus", "18:CF:5E": "OnePlus", "3C:28:6D": "OnePlus",
	"40:EC:99": "OnePlus", "60:BE:B5": "OnePlus", "80:4E:81": "OnePlus",
	"94:87:E0": "OnePlus", "AC:2A:A1": "OnePlus", "E0:20:CB": "OnePlus",
	// TP-Link
	"00:27:19": "TP-Link", "14:CC:20": "TP-Link", "18:D6:C7": "TP-Link",
	"1C:FA:68": "TP-Link", "20:DC:E6": "TP-Link", "28:87:BA": "TP-Link",
	"2C:D0:5A": "TP-Link", "30:B4:9E": "TP-Link", "3C:84:6A": "TP-Link",
	"40:ED:98": "TP-Link", "44:94:FC": "TP-Link", "50:C7:BF": "TP-Link",
	"54:A7:03": "TP-Link", "60:32:B1": "TP-Link", "60:E3:27": "TP-Link",
	"68:FF:7B": "TP-Link", "6C:5A:B5": "TP-Link", "74:DA:38": "TP-Link",
	"78:44:FD": "TP-Link", "8C:A6:DF": "TP-Link", "90:F6:52": "TP-Link",
	"A0:F3:C1": "TP-Link", "B0:BE:76": "TP-Link", "B4:D7:E8": "TP-Link",
	"C4:E9:84": "TP-Link", "D8:0D:17": "TP-Link", "DC:FE:18": "TP-Link",
	"E4:C3:2A": "TP-Link", "EC:08:6B": "TP-Link", "F0:A7:31": "TP-Link",
	"F8:1A:67": "TP-Link",
	// ASUS
	"00:1A:92": "ASUS", "04:D9:F5": "ASUS", "08:60:6E": "ASUS",
	"10:7B:44": "ASUS", "14:DA:E9": "ASUS", "18:31:BF": "ASUS",
	"2C:4D:54": "ASUS", "2C:FD:A1": "ASUS", "38:2C:4A": "ASUS",
	"50:46:5D": "ASUS", "5C:AA:FD": "ASUS", "60:45:CB": "ASUS",
	"74:D0:2B": "ASUS", "9C:5C:8E": "ASUS", "AC:22:0B": "ASUS",
	"B0:6E:BF": "ASUS", "BC:EE:7B": "ASUS", "C8:60:00": "ASUS",
	"F0:2F:74": "ASUS", "FC:34:97": "ASUS",
	// ZTE
	"28:2C:B2": "ZTE", "3C:81:D8": "ZTE", "44:E9:DD": "ZTE",
	"48:22:54": "ZTE", "50:6E:9A": "ZTE", "5C:63:BF": "ZTE",
	"68:89:C1": "ZTE", "80:88:00": "ZTE", "B4:30:52": "ZTE",
	"C8:6A:64": "ZTE", "00:19:CB": "ZTE",
	// MikroTik
	"00:0C:42": "MikroTik", "08:55:31": "MikroTik", "18:FD:74": "MikroTik",
	"2C:C8:1B": "MikroTik", "4C:5E:0C": "MikroTik", "6C:3B:6B": "MikroTik",
	"74:4D:28": "MikroTik", "B8:69:F4": "MikroTik", "D4:CA:6D": "MikroTik",
	// Proxmox VE
	"BC:24:11": "Proxmox VE",
	// Raspberry Pi
	"B8:27:EB": "Raspberry Pi", "DC:A6:32": "Raspberry Pi",
	"E4:5F:01": "Raspberry Pi", "D8:3A:DD": "Raspberry Pi",
	"2C:CF:67": "Raspberry Pi",
	// Synology
	"00:11:32": "Synology", "90:09:D0": "Synology",
	// QNAP
	"00:08:9B": "QNAP", "24:5E:BE": "QNAP",
	// Netgear
	"00:14:6C": "Netgear", "20:E5:2A": "Netgear", "30:46:9A": "Netgear",
	"A0:21:B7": "Netgear", "C0:FF:D4": "Netgear", "E8:1C:23": "Netgear",
	// Lenovo
	"00:01:97": "Lenovo", "00:09:6B": "Lenovo", "04:EA:56": "Lenovo",
	"04:B1:67": "Lenovo", "18:5E:0F": "Lenovo", "28:D2:44": "Lenovo",
	"38:52:EA": "Lenovo", "54:EE:75": "Lenovo", "5C:90:03": "Lenovo",
	"60:D9:C7": "Lenovo", "6C:88:14": "Lenovo", "70:5A:0F": "Lenovo",
	"78:45:C4": "Lenovo", "80:56:F2": "Lenovo", "84:0B:2D": "Lenovo",
	// Dell
	"00:15:C5": "Dell", "00:1A:A0": "Dell", "00:21:9B": "Dell",
	"08:8D:28": "Dell", "0C:B4:D5": "Dell", "14:FE:B5": "Dell",
	"18:03:73": "Dell", "34:73:5A": "Dell", "50:9A:4C": "Dell",
	"5C:26:0A": "Dell", "60:03:08": "Dell", "84:8F:69": "Dell",
	"B8:2A:72": "Dell", "B8:CA:3A": "Dell", "D4:BE:D9": "Dell",
	"F0:BF:97": "Dell", "F8:DB:88": "Dell",
	// Intel (common in laptops)
	"00:21:6A": "Intel", "3C:A9:F4": "Intel", "40:0E:85": "Intel",
	"60:F2:62": "Intel", "68:05:CA": "Intel", "7C:76:35": "Intel",
	"8C:8D:28": "Intel", "94:65:9C": "Intel",
	// Realtek
	"00:E0:4C": "Realtek",
	// Google (Pixel, Chromecast, Home, Nest)
	"00:1A:11": "Google", "3C:5A:B4": "Google", "54:60:09": "Google",
	"F4:F5:D8": "Google", "94:EB:2C": "Google", "D4:3D:7E": "Google",
	"48:D6:D5": "Google", "58:CB:52": "Google", "A4:77:33": "Google",
	"1C:F2:9A": "Google", "54:80:F7": "Google", "20:DF:B9": "Google",
	"F8:8A:3C": "Google", "7C:2E:BD": "Google", "E4:F0:42": "Google",
	"E0:32:84": "Google", "28:19:FA": "Google", "9C:29:76": "Google",
	// Amazon (Echo, Kindle, Fire TV)
	"44:65:0D": "Amazon", "74:C2:46": "Amazon", "84:D6:D0": "Amazon",
	"A0:02:DC": "Amazon", "FC:A1:83": "Amazon", "F0:D2:F1": "Amazon",
	"00:FC:8B": "Amazon", "68:37:E9": "Amazon", "78:E1:03": "Amazon",
	"34:D2:70": "Amazon", "40:B4:CD": "Amazon", "88:71:E5": "Amazon",
	"50:DC:E7": "Amazon", "B0:FC:36": "Amazon", "FC:65:DE": "Amazon",
	// Microsoft (Surface, Xbox, HoloLens)
	"28:18:78": "Microsoft", "28:16:A8": "Microsoft", "3C:2C:30": "Microsoft",
	"7C:1E:52": "Microsoft", "94:B8:6D": "Microsoft", "A4:C3:61": "Microsoft",
	"00:17:FA": "Microsoft", "00:50:F2": "Microsoft", "48:50:73": "Microsoft",
	"70:77:81": "Microsoft", "BC:83:85": "Microsoft", "C8:3F:26": "Microsoft",
	// Sony (PlayStation, Bravia TV)
	"00:04:1F": "Sony", "00:13:A9": "Sony", "00:24:BE": "Sony",
	"28:0D:FC": "Sony", "4C:4F:EE": "Sony", "5C:ED:8C": "Sony",
	"AC:9B:0A": "Sony", "70:3A:51": "Sony", "F8:D0:27": "Sony",
	"00:D9:D1": "Sony", "30:17:C8": "Sony", "FC:0F:E6": "Sony",
	// Nintendo (Switch, 3DS, Wii U)
	"00:0D:67": "Nintendo", "00:17:AB": "Nintendo", "00:19:1D": "Nintendo",
	"00:1F:32": "Nintendo", "8C:56:C5": "Nintendo", "98:B6:E9": "Nintendo",
	"E0:0C:7F": "Nintendo", "E8:4E:CE": "Nintendo",
	// LG Electronics (TV, phone)
	"CC:FA:00": "LG Electronics", "6C:40:08": "LG Electronics",
	"00:1E:75": "LG Electronics", "98:93:CC": "LG Electronics",
	"F4:6D:04": "LG Electronics", "18:67:B0": "LG Electronics",
	"AC:B9:2F": "LG Electronics", "E8:F2:E2": "LG Electronics",
	"B4:E6:2D": "LG Electronics", "A8:16:D0": "LG Electronics",
	// HP Inc (laptop, printer)
	"3C:D9:2B": "HP Inc", "00:26:55": "HP Inc", "24:BE:05": "HP Inc",
	"3C:A8:2A": "HP Inc", "68:B5:99": "HP Inc", "70:5A:BF": "HP Inc",
	"98:4B:E1": "HP Inc", "A0:D3:C1": "HP Inc", "BC:EA:FA": "HP Inc",
	"D0:BF:9C": "HP Inc",
	// Acer
	"00:21:5A": "Acer", "E0:94:67": "Acer", "D0:37:45": "Acer",
	// MSI
	"00:D8:61": "MSI", "70:85:C2": "MSI", "24:BE:88": "MSI",
	// NVIDIA (Shield TV, gaming)
	"00:04:4B": "NVIDIA", "40:9F:38": "NVIDIA",
	// Philips Hue / Signify
	"00:17:88": "Philips Hue", "EC:B5:FA": "Philips Hue",
	// Espressif (ESP8266/ESP32 IoT modules)
	"24:6F:28": "Espressif", "3C:71:BF": "Espressif", "A4:CF:12": "Espressif",
	"DC:54:75": "Espressif", "EC:FA:BC": "Espressif", "84:F3:EB": "Espressif",
	// Tuya (Smart home modules)
	"10:07:B6": "Tuya", "50:02:91": "Tuya",
	// Xiaomi — additional OUIs (deduplicated from above)
	"0C:3C:27": "Xiaomi", "14:F6:5A": "Xiaomi", "34:CE:00": "Xiaomi",
	"4C:49:E3": "Xiaomi", "60:AB:67": "Xiaomi", "74:23:44": "Xiaomi",
	"A4:AE:12": "Xiaomi", "C0:EE:FB": "Xiaomi", "2C:DB:07": "Xiaomi",
	// Huawei — additional OUIs
	"40:CB:C0": "Huawei", "44:55:B1": "Huawei", "70:8A:09": "Huawei",
	"84:DB:AC": "Huawei", "88:4A:F6": "Huawei", "AC:E8:7B": "Huawei",
	"CC:96:A0": "Huawei", "D0:5G:D3": "Huawei", "E8:8D:28": "Huawei",
	// Realme (phone)
	"2C:6D:C1": "Realme", "B4:AD:59": "Realme",
	// IQOO / iQOO (sub-brand of vivo)
	"28:31:01": "iQOO",
	// Meizu
	"B4:07:F9": "Meizu", "20:CD:39": "Meizu",
	// Nothing Phone
	"5C:BB:F6": "Nothing",
	// Dyson (smart appliances)
	"04:D3:B0": "Dyson",
	// Roku (streaming TV)
	"00:08:9E": "Roku", "B8:3E:59": "Roku", "CC:6D:A0": "Roku",
	"DC:3A:5E": "Roku",
	// HEVC Advance / TCL TV
	"7C:4B:46": "TCL",
	// Hisense TV
	"14:B4:84": "Hisense", "7C:CB:E2": "Hisense",
}
