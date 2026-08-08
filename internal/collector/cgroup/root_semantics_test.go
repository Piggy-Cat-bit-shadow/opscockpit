package cgroup

import (
	"os"
	"path/filepath"
	"testing"
)

// writeCgroupFixture builds a cgroup v2 tree under a temp root.
func writeCgroupFixture(t *testing.T) string {
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
	write("sys/fs/cgroup/system.slice/example.service/cgroup.procs", "4242\n")
	write("sys/fs/cgroup/system.slice/example.service/memory.current", "10485760\n")
	write("proc/4242/status", "Name:\texample\nVmRSS:\t4096 kB\n")
	return dir
}

// TestEffectiveRootSemantics: Root="" and Root="/" both resolve to the real
// /proc and /sys paths (verified without touching a real host by checking the
// path() helper). Fixture roots use their own directory.
func TestEffectiveRootSemantics(t *testing.T) {
	empty := Source{}
	if got := empty.path("proc/1/status"); got != "/proc/1/status" {
		t.Fatalf("Root='' path = %q, want /proc/1/status", got)
	}
	slash := Source{Root: "/"}
	if got := slash.path("proc/1/status"); got != "/proc/1/status" {
		t.Fatalf("Root='/' path = %q, want /proc/1/status", got)
	}
	fixture := FromDir(t.TempDir())
	if got := fixture.path("proc/1/status"); got == "/proc/1/status" {
		t.Fatalf("fixture path must be under the fixture root, got %q", got)
	}
}

// TestMemoryCurrentRealRootPath is a regression test for the production bug:
// ControlGroup=/system.slice/example.service must resolve to
// /sys/fs/cgroup/system.slice/example.service/memory.current — never a bare
// relative ./sys/fs/cgroup/... path.
func TestMemoryCurrentRealRootPath(t *testing.T) {
	src := FromDir(writeCgroupFixture(t))
	res, err := src.MemoryForControlGroup("/system.slice/example.service")
	if err != nil {
		t.Fatal(err)
	}
	if res.RSSBytes != 10485760 {
		t.Fatalf("rss = %d, want 10485760 (memory.current must resolve under /sys/fs/cgroup)", res.RSSBytes)
	}
	if res.Source != "cgroup_memory_current" {
		t.Fatalf("source = %q, want cgroup_memory_current", res.Source)
	}
}

// TestMemoryPIDFallbackRealRootPath: /proc/<pid>/status must resolve under
// /proc, not a relative path. Covers the fallback when memory.current is
// missing.
func TestMemoryPIDFallbackRealRootPath(t *testing.T) {
	dir := writeCgroupFixture(t)
	// Remove memory.current → fallback to PID VmRSS sum.
	os.Remove(filepath.Join(dir, "sys/fs/cgroup/system.slice/example.service/memory.current"))
	src := FromDir(dir)
	res, err := src.MemoryForControlGroup("/system.slice/example.service")
	if err != nil {
		t.Fatal(err)
	}
	if res.RSSBytes != 4096*1024 {
		t.Fatalf("rss = %d, want 4096*1024 (PID fallback via /proc/%d/status)", res.RSSBytes, 4242)
	}
	if res.Source != "proc_rss" {
		t.Fatalf("source = %q, want proc_rss", res.Source)
	}
}
