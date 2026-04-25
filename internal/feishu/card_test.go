package feishu

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"larkwrt/internal/collector"
	"larkwrt/internal/events"
)

func makeTestSnapshot() collector.Snapshot {
	return collector.Snapshot{
		At:     time.Now(),
		CPU:    43.5,
		Mem:    collector.MemInfo{Total: 524288, Available: 262144},
		Load:   collector.LoadAvg{One: 0.45, Five: 0.32, Fifteen: 0.21},
		Uptime: 14*time.Hour + 32*time.Minute,
		Disk:   collector.DiskInfo{TotalMB: 400, UsedMB: 152, FreeMB: 248},
		Devices: []collector.Device{
			{IP: "192.168.1.100", MAC: "aa:bb:cc:dd:ee:01", Hostname: "iPhone-15"},
			{IP: "192.168.1.101", MAC: "aa:bb:cc:dd:ee:02", Hostname: "MacBook-Pro"},
		},
		NetRates: map[string]collector.NetRate{
			"eth0": {RxBps: 8.7e6, TxBps: 1.2e6},
		},
		Addrs: []collector.AddrInfo{
			{Iface: "eth0", Addrs: []string{"203.0.113.42/24"}},
		},
	}
}

func TestBuildStatusCard_notNil(t *testing.T) {
	snap := makeTestSnapshot()
	card := BuildStatusCard("TestRouter", snap)
	if card == nil {
		t.Fatal("BuildStatusCard returned nil")
	}
}

func TestBuildStatusCard_validJSON(t *testing.T) {
	snap := makeTestSnapshot()
	card := BuildStatusCard("TestRouter", snap)

	data, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("card is not JSON-serializable: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("empty JSON output")
	}
}

func TestBuildStatusCard_headerContainsRouterName(t *testing.T) {
	snap := makeTestSnapshot()
	card := BuildStatusCard("MyGLinet", snap)

	if card.Header == nil {
		t.Fatal("card.Header is nil")
	}
	if !strings.Contains(card.Header.Title.Content, "MyGLinet") {
		t.Errorf("header title %q does not contain router name", card.Header.Title.Content)
	}
}

func TestBuildStatusCard_hasElements(t *testing.T) {
	snap := makeTestSnapshot()
	card := BuildStatusCard("R", snap)
	if len(card.Body.Elements) == 0 {
		t.Error("card has no elements")
	}
}

func TestBuildStatusCard_zeroValues(t *testing.T) {
	// should not panic on zero snapshot
	var snap collector.Snapshot
	snap.NetRates = map[string]collector.NetRate{}
	card := BuildStatusCard("Empty", snap)
	if card == nil {
		t.Fatal("BuildStatusCard with zero snapshot returned nil")
	}
}

func TestBuildDeviceListCard_empty(t *testing.T) {
	card := BuildDeviceListCard("R", nil, nil)
	data, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), "无设备") {
		t.Errorf("empty device list card should indicate no devices")
	}
}

func TestBuildDeviceListCard_withDevices(t *testing.T) {
	devices := []collector.Device{
		{IP: "192.168.1.100", MAC: "aa:bb:cc:dd:ee:01", Hostname: "iPhone"},
	}
	card := BuildDeviceListCard("R", devices, nil)
	data, _ := json.Marshal(card)
	if !strings.Contains(string(data), "iPhone") {
		t.Errorf("device list card should contain device hostname")
	}
}

func TestBuildDeviceListCard_customName(t *testing.T) {
	devices := []collector.Device{
		{IP: "192.168.1.100", MAC: "aa:bb:cc:dd:ee:01"},
	}
	names := map[string]string{"aa:bb:cc:dd:ee:01": "老爸的手机"}
	card := BuildDeviceListCard("R", devices, names)
	data, _ := json.Marshal(card)
	if !strings.Contains(string(data), "老爸的手机") {
		t.Errorf("device list card should use custom name")
	}
}

func TestBuildDeviceListCard_vendorFallback(t *testing.T) {
	devices := []collector.Device{
		{IP: "192.168.1.100", MAC: "bc:24:11:90:cb:a1", Vendor: "Proxmox VE"},
	}
	card := BuildDeviceListCard("R", devices, nil)
	data, _ := json.Marshal(card)
	if !strings.Contains(string(data), "Proxmox VE") {
		t.Errorf("device list card should show vendor when no hostname")
	}
}

func TestBuildAlertCard_deviceJoin(t *testing.T) {
	ev := events.Event{
		Type: events.EvDeviceJoin,
		Payload: events.DevicePayload{
			MAC: "aa:bb:cc:dd:ee:01", IP: "192.168.1.100", Hostname: "iPhone-15",
		},
		At: time.Now(),
	}
	card := BuildAlertCard("R", ev)
	if card == nil {
		t.Fatal("nil card")
	}
	if card.Header.Template != "green" {
		t.Errorf("join alert should be green, got %q", card.Header.Template)
	}
	data, _ := json.Marshal(card)
	if !strings.Contains(string(data), "iPhone-15") {
		t.Error("alert card should contain device name")
	}
}

func TestBuildAlertCard_highCPU(t *testing.T) {
	ev := events.Event{
		Type: events.EvHighCPU,
		Payload: events.CPUPayload{Percent: 91.5, Duration: 65 * time.Second},
		At:      time.Now(),
	}
	card := BuildAlertCard("R", ev)
	if card.Header.Template != "red" {
		t.Errorf("high CPU alert should be red, got %q", card.Header.Template)
	}
}

func TestBuildAlertCard_wanIPChange(t *testing.T) {
	ev := events.Event{
		Type: events.EvWANIPChange,
		Payload: events.WANIPPayload{OldIP: "1.2.3.4", NewIP: "5.6.7.8", Iface: "eth0"},
		At:      time.Now(),
	}
	card := BuildAlertCard("R", ev)
	data, _ := json.Marshal(card)
	if !strings.Contains(string(data), "1.2.3.4") || !strings.Contains(string(data), "5.6.7.8") {
		t.Error("WAN IP change card should contain both old and new IPs")
	}
}

func TestBuildConfirmCard_structure(t *testing.T) {
	card := BuildConfirmCard("Router", "重启路由器", "token-abc")
	if card == nil {
		t.Fatal("nil card")
	}
	if card.Header.Template != "yellow" {
		t.Errorf("confirm card should be yellow, got %q", card.Header.Template)
	}
	data, _ := json.Marshal(card)
	// Should contain both confirm and cancel actions
	if !strings.Contains(string(data), "token-abc") {
		t.Error("confirm card should contain the action token")
	}
	if !strings.Contains(string(data), "confirm") {
		t.Error("confirm card should have a confirm action")
	}
	if !strings.Contains(string(data), "cancel") {
		t.Error("confirm card should have a cancel action")
	}
}

func TestBuildResultCard_success(t *testing.T) {
	card := BuildResultCard("R", "重启", "重启命令已发出", true)
	if card.Header.Template != "green" {
		t.Errorf("success result should be green, got %q", card.Header.Template)
	}
}

func TestBuildResultCard_failure(t *testing.T) {
	card := BuildResultCard("R", "重启", "permission denied", false)
	if card.Header.Template != "red" {
		t.Errorf("failure result should be red, got %q", card.Header.Template)
	}
}

func TestProgressBar(t *testing.T) {
	tests := []struct {
		val, max float64
		width    int
		wantFull int
	}{
		{50, 100, 8, 4},
		{0, 100, 8, 0},
		{100, 100, 8, 8},
		{200, 100, 8, 8}, // clamped
	}
	for _, tt := range tests {
		bar := progressBar(tt.val, tt.max, tt.width)
		filled := strings.Count(bar, "█")
		if filled != tt.wantFull {
			t.Errorf("progressBar(%.0f,%.0f,%d): got %d filled, want %d (bar=%q)",
				tt.val, tt.max, tt.width, filled, tt.wantFull, bar)
		}
		if len([]rune(bar)) != tt.width {
			t.Errorf("progressBar width: got %d want %d", len([]rune(bar)), tt.width)
		}
	}
}

func TestFormatRate(t *testing.T) {
	tests := []struct {
		bps  float64
		want string
	}{
		{500, "500B"},
		{1500, "2K"},
		{1.5e6, "1.5M"},
		{2.3e9, "2.3G"},
	}
	for _, tt := range tests {
		got := formatRate(tt.bps)
		if got != tt.want {
			t.Errorf("formatRate(%.0f): got %q want %q", tt.bps, got, tt.want)
		}
	}
}
