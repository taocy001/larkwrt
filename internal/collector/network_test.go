package collector

import (
	"strings"
	"testing"
)

const procNetDev = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:       0       0    0    0    0     0          0         0        0       0    0    0    0     0       0          0
  eth0: 1048576    1024    0    0    0     0          0         0   524288     512    0    0    0     0       0          0
 wlan0:  262144     256    0    0    0     0          0         0   131072     128    0    0    0     0       0          0
`

func TestReadIfaceCountersFrom_fixture(t *testing.T) {
	sample, err := readIfaceCountersFrom(strings.NewReader(procNetDev))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	eth0, ok := sample.Counters["eth0"]
	if !ok {
		t.Fatal("eth0 not found in counters")
	}
	if eth0.RxBytes != 1048576 {
		t.Errorf("eth0.RxBytes: got %d want 1048576", eth0.RxBytes)
	}
	if eth0.TxBytes != 524288 {
		t.Errorf("eth0.TxBytes: got %d want 524288", eth0.TxBytes)
	}
	if eth0.RxPackets != 1024 {
		t.Errorf("eth0.RxPackets: got %d want 1024", eth0.RxPackets)
	}

	wlan0, ok := sample.Counters["wlan0"]
	if !ok {
		t.Fatal("wlan0 not found in counters")
	}
	if wlan0.RxBytes != 262144 {
		t.Errorf("wlan0.RxBytes: got %d want 262144", wlan0.RxBytes)
	}
}

func TestReadIfaceCountersFrom_loopback(t *testing.T) {
	sample, _ := readIfaceCountersFrom(strings.NewReader(procNetDev))
	lo := sample.Counters["lo"]
	if lo.RxBytes != 0 {
		t.Errorf("lo.RxBytes: got %d want 0", lo.RxBytes)
	}
}

func TestParseIPLink_json(t *testing.T) {
	const ipLinkJSON = `[
  {"ifname":"eth0","operstate":"UP","address":"aa:bb:cc:dd:ee:01","mtu":1500},
  {"ifname":"wlan0","operstate":"DOWN","address":"aa:bb:cc:dd:ee:02","mtu":1500},
  {"ifname":"lo","operstate":"UNKNOWN","address":"00:00:00:00:00:00","mtu":65536}
]`
	infos, err := parseIPLink(ipLinkJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(infos) != 3 {
		t.Fatalf("len: got %d want 3", len(infos))
	}
	if infos[0].Name != "eth0" {
		t.Errorf("Name: got %q want eth0", infos[0].Name)
	}
	if infos[0].State != "UP" {
		t.Errorf("State: got %q want UP", infos[0].State)
	}
	if infos[1].State != "DOWN" {
		t.Errorf("State: got %q want DOWN", infos[1].State)
	}
	if infos[0].MTU != 1500 {
		t.Errorf("MTU: got %d want 1500", infos[0].MTU)
	}
}

func TestParseIPAddr_json(t *testing.T) {
	const ipAddrJSON = `[
  {"ifname":"eth0","addr_info":[{"local":"203.0.113.1","prefixlen":24,"family":"inet"},{"local":"fe80::1","prefixlen":64,"family":"inet6"}]},
  {"ifname":"wlan0","addr_info":[{"local":"192.168.1.1","prefixlen":24,"family":"inet"}]}
]`
	addrs, err := parseIPAddr(ipAddrJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(addrs) != 2 {
		t.Fatalf("len: got %d want 2", len(addrs))
	}
	if addrs[0].Iface != "eth0" {
		t.Errorf("Iface: got %q want eth0", addrs[0].Iface)
	}
	if len(addrs[0].Addrs) != 2 {
		t.Errorf("eth0 addrs len: got %d want 2", len(addrs[0].Addrs))
	}
	if addrs[0].Addrs[0] != "203.0.113.1/24" {
		t.Errorf("eth0 first addr: got %q want 203.0.113.1/24", addrs[0].Addrs[0])
	}
}

func TestParseIPRoute_json(t *testing.T) {
	const ipRouteJSON = `[
  {"dst":"default","gateway":"192.168.1.254","dev":"eth0","protocol":"dhcp"},
  {"dst":"192.168.1.0/24","dev":"br-lan","protocol":"kernel"}
]`
	routes, err := parseIPRoute(ipRouteJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("len: got %d want 2", len(routes))
	}
	if routes[0].Dst != "default" {
		t.Errorf("Dst: got %q want default", routes[0].Dst)
	}
	if routes[0].Gateway != "192.168.1.254" {
		t.Errorf("Gateway: got %q want 192.168.1.254", routes[0].Gateway)
	}
}

func TestParseIPLink_invalidJSON(t *testing.T) {
	_, err := parseIPLink("not json")
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}
