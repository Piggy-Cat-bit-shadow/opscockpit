package collect

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWorkerPIDMapping verifies that a worker PID (whose socket differs from
// the unit MainPID) resolves to the same service via /proc/<pid>/cgroup.
func TestWorkerPIDMapping(t *testing.T) {
	dir := t.TempDir()
	// proc/7001/cgroup points at the nginx cgroup (worker).
	writeTest(t, filepath.Join(dir, "proc/7001/cgroup"),
		"0::/system.slice/nginx.service\n")
	// proc/7002/cgroup points at a template instance.
	writeTest(t, filepath.Join(dir, "proc/7002/cgroup"),
		"0::/system.slice/foo@abc.service\n")

	proc := ProcCgroup{Root: dir}
	if got := proc.CgroupPath(7001); got != "/system.slice/nginx.service" {
		t.Fatalf("cgroup path = %q", got)
	}
	if got := UnitFromCgroup("/system.slice/nginx.service"); got != "nginx.service" {
		t.Fatalf("unit = %q", got)
	}
	if got := UnitFromCgroup("/system.slice/foo@abc.service"); got != "foo@abc.service" {
		t.Fatalf("instance unit = %q", got)
	}
	// Unknown PID → no mapping.
	if got := proc.CgroupPath(99999); got != "" {
		t.Fatalf("unknown pid cgroup = %q", got)
	}

	// Build a resolver: worker 7001 maps to nginx via unit→svc.
	resolver := BuildPIDResolver(nil, proc, map[string]string{
		"nginx.service":     "nginx",
		"foo@abc.service":   "foo",
	})
	if got := resolver(7001); got != "nginx" {
		t.Fatalf("worker 7001 → %q, want nginx", got)
	}
	if got := resolver(7002); got != "foo" {
		t.Fatalf("template instance 7002 → %q, want foo", got)
	}
	if got := resolver(99999); got != "" {
		t.Fatalf("unknown pid → %q, want empty", got)
	}
}

// TestWorkerOverridesDirectMap: an explicit pidToSvc entry wins over cgroup.
func TestWorkerOverridesDirectMap(t *testing.T) {
	dir := t.TempDir()
	writeTest(t, filepath.Join(dir, "proc/7001/cgroup"),
		"0::/system.slice/other.service\n")
	proc := ProcCgroup{Root: dir}
	resolver := BuildPIDResolver(map[int]string{7001: "explicit"}, proc, map[string]string{})
	if got := resolver(7001); got != "explicit" {
		t.Fatalf("direct map should win: %q", got)
	}
}

func writeTest(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
