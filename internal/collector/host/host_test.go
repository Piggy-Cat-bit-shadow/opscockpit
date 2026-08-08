package host

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFixture builds a fake Linux /proc tree under t.TempDir().
func writeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("proc/sys/kernel/hostname", "fixture-host\n")
	write("proc/uptime", "3600.00 100.00\n")
	write("proc/loadavg", "0.10 0.20 0.30 1/100 1234\n")
	write("proc/stat", `cpu  1000 0 500 9000 100 0 50 0 0 0
cpu0 500 0 250 4500 50 0 25 0 0 0
cpu1 500 0 250 4500 50 0 25 0 0 0
intr 123 45
ctxt 99
processes 42
`)
	write("proc/meminfo", `MemTotal:       1024000 kB
MemFree:         200000 kB
MemAvailable:    300000 kB
Buffers:          10000 kB
Cached:           50000 kB
SwapCached:            0 kB
SwapTotal:        512000 kB
SwapFree:         512000 kB
`)
	write("proc/self/fixture_disk", "1000000000 250000000\n")
	return dir
}

func TestHostCollect(t *testing.T) {
	src := FromDir(writeFixture(t))
	info, err := Collect(src, 0)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	if info.Hostname != "fixture-host" {
		t.Errorf("hostname = %q, want fixture-host", info.Hostname)
	}
	if info.UptimeSeconds != 3600 {
		t.Errorf("uptime = %v, want 3600", info.UptimeSeconds)
	}
	if info.Load.Load1 != 0.10 || info.Load.Load5 != 0.20 || info.Load.Load15 != 0.30 {
		t.Errorf("load = %v", info.Load)
	}
	if info.CPU.Cores != 2 {
		t.Errorf("cores = %d, want 2", info.CPU.Cores)
	}
	if info.Memory.Total != 1024000*1024 {
		t.Errorf("mem total = %d", info.Memory.Total)
	}
	// used = total - available
	wantUsed := int64((1024000 - 300000) * 1024)
	if info.Memory.Used != wantUsed {
		t.Errorf("mem used = %d, want %d", info.Memory.Used, wantUsed)
	}
	if info.Swap.Total != 512000*1024 {
		t.Errorf("swap total = %d", info.Swap.Total)
	}
	if info.Swap.Used != 0 {
		t.Errorf("swap used = %d, want 0", info.Swap.Used)
	}
	if info.Disk.Total != 1000000000 || info.Disk.Used != 250000000 {
		t.Errorf("disk = %+v", info.Disk)
	}
	if info.Disk.Percent <= 0 || info.Disk.Percent >= 100 {
		t.Errorf("disk percent = %v", info.Disk.Percent)
	}
	if info.CPU.Percent != 0 {
		t.Errorf("cpu percent with zero interval should be 0, got %v", info.CPU.Percent)
	}
}

func TestCPUPercentDelta(t *testing.T) {
	// Two sequential samples: the second shows a jump in busy time.
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
	}
	// First sample: idle 9000 of total 11650.
	write("proc/stat", "cpu  1000 0 500 9000 100 0 50 0 0 0\ncpu0 1 2 3 4\n")
	src := FromDir(dir)

	before, _ := src.cpuSample()

	// Second sample: idle stays 9000, total jumps to 12450 (all busy).
	write("proc/stat", "cpu  1000 0 2500 9000 100 0 850 0 0 0\ncpu0 1 2 3 4\n")
	after, _ := src.cpuSample()

	dTotal := after.Time.total - before.Time.total
	dIdle := after.Time.idle - before.Time.idle
	busy := (dTotal - dIdle) / dTotal * 100
	if dTotal <= 0 || busy <= 0 {
		t.Fatalf("expected positive busy percentage, dTotal=%v busy=%v", dTotal, busy)
	}
}

func TestParseLoad(t *testing.T) {
	l, err := ParseLoad("1.50 2.25 3.75 5/300 999\n")
	if err != nil {
		t.Fatal(err)
	}
	if l.Load1 != 1.5 || l.Load5 != 2.25 || l.Load15 != 3.75 {
		t.Errorf("load = %+v", l)
	}
}

func TestParseMeminfoFallback(t *testing.T) {
	// No MemAvailable → fallback formula.
	total, used, err := ParseMeminfo("MemTotal: 1000 kB\nMemFree: 100 kB\nBuffers: 50 kB\nCached: 150 kB\n")
	if err != nil {
		t.Fatal(err)
	}
	if total != 1000*1024 {
		t.Errorf("total = %d", total)
	}
	want := (1000 - 100 - 50 - 150) * 1024
	if used != int64(want) {
		t.Errorf("used = %d, want %d", used, want)
	}
}

func TestMissingFixtureFailsGracefully(t *testing.T) {
	src := FromDir(t.TempDir()) // empty tree
	info, err := Collect(src, 0)
	if err != nil {
		t.Fatalf("collect should degrade gracefully: %v", err)
	}
	if info.Hostname != "" {
		t.Errorf("hostname should be empty on missing fixture, got %q", info.Hostname)
	}
}
