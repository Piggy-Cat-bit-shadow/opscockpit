package collect

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/opscockpit/opscockpit/internal/collector/cgroup"
	"github.com/opscockpit/opscockpit/internal/collector/host"
	"github.com/opscockpit/opscockpit/internal/state"
)

// writeRealisticRuntime builds a fixture tree that mirrors a real VPS:
// systemd units with /proc/<pid>/cgroup → /system.slice/<unit>, cgroup v2
// memory.current, /proc/<pid>/status, and an ss listing with those PIDs.
// This exercises the production wiring (effective Root="" → "/" semantics)
// without touching the CI host's real /proc.
func writeRealisticRuntime(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// systemd cgroup v2 trees.
	for unit, pid := range map[string]string{
		"nginx.service":            "1001",
		"hysteria-server.service":  "2002",
		"sing-box.service":         "3003",
		"xray.service":             "4004",
	} {
		write("sys/fs/cgroup/system.slice/"+unit+"/cgroup.procs", pid+"\n")
		write("sys/fs/cgroup/system.slice/"+unit+"/memory.current", "8388608\n")
	}
	// /proc/<pid>/cgroup (worker mapping uses this).
	for pid, unit := range map[string]string{
		"1001": "/system.slice/nginx.service",
		"2002": "/system.slice/hysteria-server.service",
		"3003": "/system.slice/sing-box.service",
		"4004": "/system.slice/xray.service",
	} {
		write("proc/"+pid+"/cgroup", "0::"+unit+"\n")
		write("proc/"+pid+"/status", "VmRSS:\t4096 kB\n")
	}
	// Minimal host proc for host collection.
	write("proc/sys/kernel/hostname", "prod-host\n")
	write("proc/uptime", "3600.00 100.00\n")
	write("proc/loadavg", "0.10 0.20 0.30 1/100 1234\n")
	write("proc/stat", "cpu  1000 0 500 9000 100 0 50 0 0 0\ncpu0 1 2 3 4\n")
	write("proc/meminfo", "MemTotal: 1024000 kB\nMemFree: 200000 kB\nMemAvailable: 300000 kB\n")
	return root
}

// realisticSS has the PIDs matching the /proc fixture above.
const realisticSS = `tcp   LISTEN 0 511    0.0.0.0:443        0.0.0.0:*  users:(("nginx",pid=1001,fd=10))
udp   UNCONN 0 0      [::]:443          [::]:*     users:(("hysteria",pid=2002,fd=7))
udp   UNCONN 0 0      [::]:8443         [::]:*     users:(("sing-box",pid=3003,fd=9))
tcp   LISTEN 0 128    127.0.0.1:18444   0.0.0.0:*  users:(("xray",pid=4004,fd=12))
`

func realisticServicesPath(t *testing.T) string {
	t.Helper()
	return writeServicesYAML(t, `services:
  - id: nginx
    name: Nginx
    systemd: { unit: nginx.service }
    restart_enabled: true
  - id: hysteria2
    name: Hysteria2
    systemd: { unit: hysteria-server.service }
    restart_enabled: true
  - id: sing-box
    name: sing-box
    systemd: { unit: sing-box.service }
    restart_enabled: true
  - id: xray
    name: Xray
    systemd: { unit: xray.service }
    restart_enabled: true
`)
}

// realisticRunner simulates the runtime with production-style command output:
// ss, systemd unit show (active), UFW, iptables, ip, docker/nginx absent.
func realisticRunner() *mockRunner {
	return &mockRunner{
		ssText: realisticSS,
		units: map[string]string{
			"nginx.service":           unitShow("active", 1001, "/system.slice/nginx.service"),
			"hysteria-server.service": unitShow("active", 2002, "/system.slice/hysteria-server.service"),
			"sing-box.service":        unitShow("active", 3003, "/system.slice/sing-box.service"),
			"xray.service":            unitShow("active", 4004, "/system.slice/xray.service"),
		},
		ufwText: `Status: active
Default: deny (incoming), allow (outgoing), disabled (routed)

To                         Action      From
--                         ------      ----
443/tcp                    ALLOW IN    Anywhere
443/udp                    ALLOW IN    Anywhere
8443/udp                   ALLOW IN    Anywhere
`,
		ipAddr:  `[{"ifname":"eth0","addr_info":[{"family":"inet","local":"203.0.113.10","prefixlen":24,"scope":"global"},{"family":"inet6","local":"2001:db8::10","prefixlen":64,"scope":"global"}]}]`,
		ipRoute: `[{"dst":"default","dev":"eth0","family":"inet"},{"dst":"default","dev":"eth0","family":"inet6"}]`,
	}
}

// TestProductionWiringIntegration is the key regression test: it builds the
// runtime-root selection + resolver construction EXACTLY as cmdCollect does in
// production (Root="" effective "/" semantics), feeds a realistic fixture
// through a real collection, and asserts the resolver actually wired listeners
// to services, filled memory, and produced a non-empty topology.
func TestProductionWiringIntegration(t *testing.T) {
	runtimeRoot := writeRealisticRuntime(t)
	servicesPath := realisticServicesPath(t)
	statePath := filepath.Join(t.TempDir(), "state.json")

	// Mirror cmdCollect's production wiring: build the resolver and install it
	// as the runner's PID→service hook (ProductionRunner.ResolveServiceID calls
	// this closure; for the test we wrap the mock runner the same way).
	unitSvc, pidSvc := LoadUnitServiceMapping(servicesPath, runtimeRoot)
	resolver := BuildPIDResolver(pidSvc, ProcCgroup{Root: runtimeRoot}, unitSvc)

	r := realisticRunner()
	r.pidToSvc = map[int]string{} // not set directly; resolution goes through cgroup
	r.resolveFn = func(pid int) string { return resolver(pid) }

	res, err := Collect(context.Background(), r, Options{
		HostSource:    host.FromDir(runtimeRoot),
		CgroupSource:  cgroup.FromDir(runtimeRoot),
		FixtureRoot:   runtimeRoot,
		ServicesPath:  servicesPath,
		StatePath:     statePath,
		CPUIntervalMs: 0,
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	st := res.State

	// 1. Service listeners must be populated (resolver worked).
	ng := byID(t, st, "nginx")
	if len(ng.Listeners) == 0 {
		t.Fatal("nginx listeners empty — PID resolver did not wire listener → service")
	}
	foundPort := false
	for _, l := range ng.Listeners {
		if l.Port == 443 && l.Protocol == "tcp" && !l.Internal {
			foundPort = true
		}
	}
	if !foundPort {
		t.Errorf("nginx 0.0.0.0:443/tcp not mapped: %+v", ng.Listeners)
	}

	// 2. Service memory must be populated (cgroup v2 memory.current under the
	// effective /sys/fs/cgroup path).
	for _, id := range []string{"nginx", "hysteria2", "sing-box", "xray"} {
		s := byID(t, st, id)
		if s.Memory == nil {
			t.Fatalf("service %s has no memory — cgroup memory.current not read", id)
		}
	}

	// 3. Topology must be non-trivial and edges must be an array.
	if len(st.Topology.Nodes) <= 1 {
		t.Fatalf("topology nodes = %d, want > 1 (only Internet)", len(st.Topology.Nodes))
	}
	raw, _ := os.ReadFile(statePath)
	var asMap map[string]any
	if err := json.Unmarshal(raw, &asMap); err != nil {
		t.Fatal(err)
	}
	topo, _ := asMap["topology"].(map[string]any)
	if topo == nil {
		t.Fatal("topology missing from state.json")
	}
	if _, ok := topo["edges"].([]any); !ok {
		t.Fatalf("topology.edges must marshal as an array, got %T", topo["edges"])
	}
	if _, ok := topo["nodes"].([]any); !ok {
		t.Fatalf("topology.nodes must marshal as an array, got %T", topo["nodes"])
	}

	// 4. Active daemons with discovered listeners must be healthy (not masked
	// by require_listener=false).
	if ng.Status != state.StatusHealthy {
		t.Errorf("nginx status = %q, want healthy (resolver must make this work)", ng.Status)
	}
}
