package cgroup

import (
	"os"
	"path/filepath"
	"testing"
)

// buildFixture writes a cgroup v2 tree and /proc status files.
func buildFixture(t *testing.T) string {
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

	// cgroup v2 root
	write("sys/fs/cgroup/system.slice/hysteria-server.service/cgroup.procs", "100\n200\n")
	write("sys/fs/cgroup/system.slice/hysteria-server.service/memory.current", "10485760\n")

	write("sys/fs/cgroup/system.slice/adguard.service/cgroup.procs", "300\n")
	// memory.current deliberately missing → fallback path
	write("sys/fs/cgroup/system.slice/adguard.service/cgroup.procs", "300\n")

	// Multi-process cgroup WITHOUT memory.current → PID-sum fallback
	write("sys/fs/cgroup/system.slice/xray.service/cgroup.procs", "400\n401\n")

	write("proc/100/status", "Name:\thysteria\nVmRSS:\t4096 kB\nVmSize:\t8192 kB\n")
	write("proc/200/status", "Name:\thysteria\nVmRSS:\t2048 kB\nVmSize:\t4096 kB\n")
	write("proc/300/status", "Name:\tadguard\nVmRSS:\t1024 kB\nVmSize:\t2048 kB\n")
	write("proc/400/status", "Name:\txray\nVmRSS:\t512 kB\nVmSize:\t1024 kB\n")
	write("proc/401/status", "Name:\txray\nVmRSS:\t256 kB\nVmSize:\t512 kB\n")

	return dir
}

func TestCgroupMemoryCurrentPrimary(t *testing.T) {
	s := FromDir(buildFixture(t))
	res, err := s.MemoryForControlGroup("/system.slice/hysteria-server.service")
	if err != nil {
		t.Fatal(err)
	}
	if res.RSSBytes != 10485760 {
		t.Errorf("RSS = %d, want 10485760", res.RSSBytes)
	}
	if res.Source != "cgroup_memory_current" {
		t.Errorf("source = %q, want cgroup_memory_current", res.Source)
	}
}

func TestCgroupFallbackToPIDSum(t *testing.T) {
	s := FromDir(buildFixture(t))
	// xray has no memory.current, only two PIDs.
	res, err := s.MemoryForControlGroup("/system.slice/xray.service")
	if err != nil {
		t.Fatal(err)
	}
	want := int64((512 + 256) * 1024)
	if res.RSSBytes != want {
		t.Errorf("RSS = %d, want %d", res.RSSBytes, want)
	}
	if res.Source != "proc_rss" {
		t.Errorf("source = %q, want proc_rss", res.Source)
	}
}

func TestCgroupMultipleProcessSum(t *testing.T) {
	s := FromDir(buildFixture(t))
	pids, err := s.PIDSet("/system.slice/hysteria-server.service")
	if err != nil {
		t.Fatal(err)
	}
	if len(pids) != 2 {
		t.Fatalf("pids = %v, want 2", pids)
	}
	total := s.SumRSS(pids)
	if total != (4096+2048)*1024 {
		t.Errorf("sum = %d, want %d", total, (4096+2048)*1024)
	}
}

func TestCgroupMissingMemoryCurrentFallback(t *testing.T) {
	s := FromDir(buildFixture(t))
	// adguard has cgroup.procs (300) but no memory.current.
	res, err := s.MemoryForControlGroup("/system.slice/adguard.service")
	if err != nil {
		t.Fatal(err)
	}
	if res.RSSBytes != 1024*1024 {
		t.Errorf("RSS = %d, want %d", res.RSSBytes, 1024*1024)
	}
	if res.Source != "proc_rss" {
		t.Errorf("source = %q, want proc_rss", res.Source)
	}
}

func TestMainPIDFallback(t *testing.T) {
	s := FromDir(buildFixture(t))
	res, err := s.MemoryForMainPID(300)
	if err != nil {
		t.Fatal(err)
	}
	if res.RSSBytes != 1024*1024 {
		t.Errorf("RSS = %d", res.RSSBytes)
	}
	if res.Source != "proc_rss" {
		t.Errorf("source = %q", res.Source)
	}
}

func TestEmptyControlGroup(t *testing.T) {
	s := FromDir(buildFixture(t))
	if _, err := s.MemoryForControlGroup(""); err == nil {
		t.Fatal("expected ErrNoCgroup for empty control group")
	}
}

func TestStoppedServiceNoData(t *testing.T) {
	s := FromDir(buildFixture(t))
	// A stopped unit has an empty cgroup.procs and no memory.current.
	writeFile(t, filepath.Join(t.TempDir(), "empty"), "")
	if _, err := s.MemoryForControlGroup("/system.slice/stopped.service"); err == nil {
		t.Fatal("expected error for stopped service with no data")
	}
}

func TestProcStatusMissingPID(t *testing.T) {
	s := FromDir(buildFixture(t))
	// PID 999 has no /proc/999/status.
	pids := []int{999}
	if total := s.SumRSS(pids); total != 0 {
		t.Errorf("sum for missing pid = %d, want 0", total)
	}
	if _, err := s.MemoryForMainPID(999); err == nil {
		t.Fatal("expected error reading missing PID")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
