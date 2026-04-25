package collector

import (
	"strings"
	"testing"
	"time"
)

// ── CPUSample ─────────────────────────────────────────────────────────────────

func TestParseCPULine_ok(t *testing.T) {
	line := "cpu  4000 100 1000 15000 200 50 50 0"
	s, err := parseCPULine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.User != 4000 {
		t.Errorf("User: got %d want 4000", s.User)
	}
	if s.Idle != 15000 {
		t.Errorf("Idle: got %d want 15000", s.Idle)
	}
	if s.IOWait != 200 {
		t.Errorf("IOWait: got %d want 200", s.IOWait)
	}
}

func TestParseCPULine_tooFewFields(t *testing.T) {
	_, err := parseCPULine("cpu  100 200")
	if err == nil {
		t.Error("expected error for truncated cpu line")
	}
}

func TestReadCPUSampleFrom_fixtureFile(t *testing.T) {
	const fixture = `cpu  4000 100 1000 15000 200 50 50 0 0 0
cpu0 2000 50 500 7500 100 25 25 0 0 0
`
	s, err := readCPUSampleFrom(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// total = 4000+100+1000+15000+200+50+50 = 20400
	if s.total() != 20400 {
		t.Errorf("total: got %d want 20400", s.total())
	}
}

func TestCPUPercent_normalUsage(t *testing.T) {
	// /proc/stat values are cumulative — b must be larger than a.
	// Delta: User+=300, Idle+=700 → total delta=1000, busy delta=300 → 30%
	a := CPUSample{User: 1000, Idle: 9000}  // total=10000
	b := CPUSample{User: 1300, Idle: 9700}  // total=11000
	pct := CPUPercent(a, b)
	if pct < 29.9 || pct > 30.1 {
		t.Errorf("CPUPercent: got %.2f want ~30.0", pct)
	}
}

func TestCPUPercent_zeroDelta(t *testing.T) {
	s := CPUSample{User: 100, Idle: 900}
	if pct := CPUPercent(s, s); pct != 0 {
		t.Errorf("identical samples should give 0%%, got %.2f", pct)
	}
}

func TestCPUPercent_100Percent(t *testing.T) {
	a := CPUSample{User: 0, Idle: 0}
	b := CPUSample{User: 1000, Idle: 0}
	pct := CPUPercent(a, b)
	if pct < 99.9 {
		t.Errorf("full busy should give ~100%%, got %.2f", pct)
	}
}

// ── MemInfo ───────────────────────────────────────────────────────────────────

func TestReadMemInfoFrom_fixture(t *testing.T) {
	const fixture = `MemTotal:         524288 kB
MemFree:          204800 kB
MemAvailable:     262144 kB
Buffers:           20480 kB
Cached:            40960 kB
`
	m, err := readMemInfoFrom(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Total != 524288 {
		t.Errorf("Total: got %d want 524288", m.Total)
	}
	if m.Available != 262144 {
		t.Errorf("Available: got %d want 262144", m.Available)
	}
}

func TestMemInfo_UsedPct(t *testing.T) {
	m := MemInfo{Total: 524288, Available: 262144}
	pct := m.UsedPct()
	// (524288 - 262144) / 524288 * 100 = 50%
	if pct < 49.9 || pct > 50.1 {
		t.Errorf("UsedPct: got %.2f want ~50.0", pct)
	}
}

func TestMemInfo_UsedPct_zero(t *testing.T) {
	m := MemInfo{Total: 0}
	if pct := m.UsedPct(); pct != 0 {
		t.Errorf("zero total should return 0, got %.2f", pct)
	}
}

func TestMemInfo_UsedMB_TotalMB(t *testing.T) {
	m := MemInfo{Total: 524288, Available: 262144}
	if m.TotalMB() != 512 {
		t.Errorf("TotalMB: got %d want 512", m.TotalMB())
	}
	if m.UsedMB() != 256 {
		t.Errorf("UsedMB: got %d want 256", m.UsedMB())
	}
}

// ── LoadAvg ───────────────────────────────────────────────────────────────────

func TestParseLoadAvg_ok(t *testing.T) {
	la, err := parseLoadAvg("0.45 0.32 0.21 2/189 12345\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if la.One < 0.44 || la.One > 0.46 {
		t.Errorf("One: got %.2f want 0.45", la.One)
	}
	if la.Five < 0.31 || la.Five > 0.33 {
		t.Errorf("Five: got %.2f want 0.32", la.Five)
	}
}

func TestParseLoadAvg_tooFewFields(t *testing.T) {
	_, err := parseLoadAvg("0.45")
	if err == nil {
		t.Error("expected error for single-field loadavg")
	}
}

// ── Uptime ────────────────────────────────────────────────────────────────────

func TestParseUptime_ok(t *testing.T) {
	d, err := parseUptime("52213.00 104426.00\n")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := time.Duration(52213 * float64(time.Second))
	if d != expected {
		t.Errorf("uptime: got %v want %v", d, expected)
	}
}

func TestParseUptime_empty(t *testing.T) {
	_, err := parseUptime("")
	if err == nil {
		t.Error("expected error for empty uptime")
	}
}

// ── DiskInfo ──────────────────────────────────────────────────────────────────

func TestParseDFOutput_ok(t *testing.T) {
	const df = `Filesystem           1M-blocks      Used Available Use% Mounted on
/dev/sda1                  400       152       248  38% /
`
	d, err := parseDFOutput(df)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.TotalMB != 400 {
		t.Errorf("TotalMB: got %d want 400", d.TotalMB)
	}
	if d.UsedMB != 152 {
		t.Errorf("UsedMB: got %d want 152", d.UsedMB)
	}
	if d.FreeMB != 248 {
		t.Errorf("FreeMB: got %d want 248", d.FreeMB)
	}
}

func TestDiskInfo_UsedPct(t *testing.T) {
	d := DiskInfo{TotalMB: 400, UsedMB: 152}
	pct := d.UsedPct()
	if pct < 37.9 || pct > 38.1 {
		t.Errorf("UsedPct: got %.2f want ~38.0", pct)
	}
}
