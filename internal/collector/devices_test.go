package collector

import (
	"strings"
	"testing"
	"time"
)

const arpFixture = `IP address       HW type     Flags       HW address            Mask     Device
192.168.1.100    0x1         0x2         aa:bb:cc:dd:ee:01     *        br-lan
192.168.1.101    0x1         0x2         aa:bb:cc:dd:ee:02     *        br-lan
192.168.1.102    0x1         0x0         00:00:00:00:00:00     *        br-lan
10.0.0.1         0x1         0x2         ff:ee:dd:cc:bb:aa     *        eth0
`

const leasesFixture = `1900000000 aa:bb:cc:dd:ee:01 192.168.1.100 iPhone-15 *
1900000000 aa:bb:cc:dd:ee:02 192.168.1.101 MacBook-Pro *
`

func TestParseARP_completeFlagsOnly(t *testing.T) {
	// no iface filter → all 0x2 entries included
	devices := parseARP(strings.NewReader(arpFixture), "")
	if len(devices) != 3 {
		t.Fatalf("len: got %d want 3", len(devices))
	}
	ips := make(map[string]bool)
	for _, d := range devices {
		ips[d.IP] = true
	}
	if !ips["192.168.1.100"] {
		t.Error("192.168.1.100 missing")
	}
	if !ips["192.168.1.101"] {
		t.Error("192.168.1.101 missing")
	}
	if !ips["10.0.0.1"] {
		t.Error("10.0.0.1 missing")
	}
	if ips["192.168.1.102"] {
		t.Error("192.168.1.102 should be excluded (flags=0x0)")
	}
}

func TestParseARP_ifaceFilter(t *testing.T) {
	devices := parseARP(strings.NewReader(arpFixture), "br-lan")
	// only br-lan entries with 0x2 → 192.168.1.100 and 192.168.1.101
	if len(devices) != 2 {
		t.Fatalf("len with br-lan filter: got %d want 2", len(devices))
	}
	for _, d := range devices {
		if d.Iface != "br-lan" {
			t.Errorf("non-br-lan device slipped through: %+v", d)
		}
	}
}

func TestParseARP_macAndIface(t *testing.T) {
	devices := parseARP(strings.NewReader(arpFixture), "")
	for _, d := range devices {
		if d.IP == "192.168.1.100" {
			if d.MAC != "aa:bb:cc:dd:ee:01" {
				t.Errorf("MAC: got %q want aa:bb:cc:dd:ee:01", d.MAC)
			}
			if d.Iface != "br-lan" {
				t.Errorf("Iface: got %q want br-lan", d.Iface)
			}
		}
	}
}

func TestParseDHCPLeases_ok(t *testing.T) {
	devices := parseDHCPLeases(strings.NewReader(leasesFixture))
	if len(devices) != 2 {
		t.Fatalf("len: got %d want 2", len(devices))
	}
	if devices[0].Hostname != "iPhone-15" {
		t.Errorf("Hostname: got %q want iPhone-15", devices[0].Hostname)
	}
	if devices[1].Hostname != "MacBook-Pro" {
		t.Errorf("Hostname: got %q want MacBook-Pro", devices[1].Hostname)
	}
	expectedExpiry := time.Unix(1900000000, 0)
	if !devices[0].LeaseEnd.Equal(expectedExpiry) {
		t.Errorf("LeaseEnd: got %v want %v", devices[0].LeaseEnd, expectedExpiry)
	}
}

func TestParseDHCPLeases_wildcardHostname(t *testing.T) {
	const leases = "1900000000 aa:bb:cc:00:00:01 192.168.1.200 * *\n"
	devices := parseDHCPLeases(strings.NewReader(leases))
	if len(devices) != 1 {
		t.Fatalf("len: got %d want 1", len(devices))
	}
	if devices[0].Hostname != "" {
		t.Errorf("wildcard hostname should be empty, got %q", devices[0].Hostname)
	}
}

func TestParseDHCPLeases_badEpoch(t *testing.T) {
	const leases = "notanumber aa:bb:cc:00:00:01 192.168.1.200 host *\n"
	devices := parseDHCPLeases(strings.NewReader(leases))
	if len(devices) != 0 {
		t.Errorf("bad epoch line should be skipped, got %d devices", len(devices))
	}
}

func TestMergeDevices_hostnameFromLease(t *testing.T) {
	arp := []Device{
		{IP: "192.168.1.100", MAC: "aa:bb:cc:dd:ee:01", Iface: "br-lan"},
	}
	leases := []Device{
		{MAC: "aa:bb:cc:dd:ee:01", IP: "192.168.1.100", Hostname: "MyPhone", LeaseEnd: time.Unix(1900000000, 0)},
	}
	merged := mergeDevices(arp, leases)
	if len(merged) != 1 {
		t.Fatalf("len: got %d want 1", len(merged))
	}
	if merged[0].Hostname != "MyPhone" {
		t.Errorf("Hostname: got %q want MyPhone", merged[0].Hostname)
	}
	if merged[0].Iface != "br-lan" {
		t.Errorf("Iface should be preserved from ARP, got %q", merged[0].Iface)
	}
}

func TestMergeDevices_macCaseInsensitive(t *testing.T) {
	arp := []Device{
		{IP: "192.168.1.100", MAC: "AA:BB:CC:DD:EE:01"},
	}
	leases := []Device{
		{MAC: "aa:bb:cc:dd:ee:01", Hostname: "Phone"},
	}
	merged := mergeDevices(arp, leases)
	if len(merged) != 1 || merged[0].Hostname != "Phone" {
		t.Errorf("MAC case-insensitive merge failed: %+v", merged)
	}
}
