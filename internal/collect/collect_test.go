package collect

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opscockpit/opscockpit/internal/collector/cgroup"
	"github.com/opscockpit/opscockpit/internal/collector/host"
	"github.com/opscockpit/opscockpit/internal/state"
)

// mockRunner simulates a real host:
//   - ss output models the spec testdata listeners
//   - systemd unit show returns active states
//   - version commands return version strings
//   - PID → service id mapping
//   - UFW, iptables nat, and ip addr/route fixtures
type mockRunner struct {
	ssText    string
	units     map[string]string
	pidToSvc  map[int]string
	versions  map[string]string // argv[0] → version
	ufwText    string
	natText    string
	ipAddr     string
	ipRoute    string
	nginxText  string
	dockerPS   string
	unitExecStart map[string]string // unit → ExecStart override
	pidCgroups map[int]string // pid → cgroup path (worker mapping)
}

func (m *mockRunner) Run(ctx context.Context, argv []string) (string, error) {
	return "", nil
}

func (m *mockRunner) RunUnit(ctx context.Context, unit string, properties []string) (string, error) {
	if out, ok := m.units[unit]; ok {
		// Allow a per-unit ExecStart override (for declared-upstream tests).
		if es, ok := m.unitExecStart[unit]; ok {
			replaced := replaceExecStart(out, es)
			if replaced != "" {
				return replaced, nil
			}
		}
		return out, nil
	}
	return "", nil
}

// replaceExecStart swaps the ExecStart= line in a systemctl show output.
func replaceExecStart(show, newExecStart string) string {
	lines := strings.Split(show, "\n")
	out := make([]string, 0, len(lines))
	replaced := false
	for _, line := range lines {
		if strings.HasPrefix(line, "ExecStart=") && !replaced {
			out = append(out, "ExecStart="+newExecStart)
			replaced = true
			continue
		}
		out = append(out, line)
	}
	if !replaced {
		return ""
	}
	return strings.Join(out, "\n")
}

func (m *mockRunner) SS(ctx context.Context) (string, error) { return m.ssText, nil }

func (m *mockRunner) UFWStatus(ctx context.Context) (string, error) {
	if m.ufwText == "" {
		return "", nil
	}
	return m.ufwText, nil
}

func (m *mockRunner) IptablesNat(ctx context.Context) (string, error) {
	if m.natText == "" {
		return "", nil
	}
	return m.natText, nil
}

func (m *mockRunner) IPAddrJSON(ctx context.Context) (string, error) {
	if m.ipAddr == "" {
		// Default: host has global IPv4 + IPv6 (both families usable).
		return `[{"ifname":"eth0","addr_info":[{"family":"inet","local":"203.0.113.10","prefixlen":24,"scope":"global"},{"family":"inet6","local":"2001:db8::10","prefixlen":64,"scope":"global"}]}]`, nil
	}
	return m.ipAddr, nil
}

func (m *mockRunner) IPRouteJSON(ctx context.Context) (string, error) {
	if m.ipRoute == "" {
		return `[{"dst":"default","dev":"eth0","family":"inet"},{"dst":"default","dev":"eth0","family":"inet6"}]`, nil
	}
	return m.ipRoute, nil
}

func (m *mockRunner) NginxT(ctx context.Context) (string, error) {
	if m.nginxText == "" {
		return "", nil // nginx absent → no deps, never fatal
	}
	return m.nginxText, nil
}

func (m *mockRunner) DockerPS(ctx context.Context) (string, error) {
	if m.dockerPS == "" {
		return "", nil // docker absent → no containers, never fatal
	}
	return m.dockerPS, nil
}

func (m *mockRunner) Version(ctx context.Context, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", nil
	}
	if v, ok := m.versions[argv[0]]; ok {
		return v, nil
	}
	return "", nil
}

func (m *mockRunner) ResolveServiceID(pid int) string { return m.pidToSvc[pid] }

// ssFixtureText matches the spec's testdata environment.
const ssFixtureText = `tcp   LISTEN 0 511    0.0.0.0:443       0.0.0.0:*  users:(("nginx",pid=1001,fd=10))
udp   UNCONN 0 0      [::]:443         [::]:*     users:(("hysteria",pid=2002,fd=7))
udp   UNCONN 0 0      [::]:8443        [::]:*     users:(("tuic",pid=3003,fd=9))
tcp   LISTEN 0 128    0.0.0.0:853      0.0.0.0:*  users:(("adguard",pid=4004,fd=6))
udp   UNCONN 0 0      0.0.0.0:853      0.0.0.0:*  users:(("adguard",pid=4004,fd=8))
tcp   LISTEN 0 128    127.0.0.1:18444  0.0.0.0:*  users:(("xray",pid=5005,fd=12))
`

func mockServicesPath(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "services.yaml")
	data := []byte(`services:
  - id: nginx
    name: Nginx
    systemd: { unit: nginx.service }
    config_paths: ["/etc/nginx/nginx.conf"]
    restart_enabled: true
  - id: hysteria2
    name: Hysteria2
    systemd: { unit: hysteria-server.service }
    config_paths: ["/etc/hysteria/config.yaml"]
    version: { command: ["/usr/local/bin/hysteria", "version"], timeout: 5s }
    restart_enabled: true
  - id: tuic
    name: TUIC
    systemd: { unit: tuic.service }
    config_paths: ["/etc/tuic/config.json"]
    restart_enabled: true
  - id: adguard-home
    name: AdGuard Home
    systemd: { unit: adguard.service }
    config_paths: ["/etc/AdGuardHome/AdGuardHome.yaml"]
    restart_enabled: true
  - id: xray
    name: Xray
    systemd: { unit: xray.service }
    config_paths: ["/etc/xray/config.json"]
    version: { command: ["/usr/local/bin/xray", "version"] }
    restart_enabled: true
`)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func unitShow(active string, pid int, cg string) string {
	return "ActiveState=" + active + "\n" +
		"SubState=running\n" +
		"MainPID=" + itoa(pid) + "\n" +
		"ControlGroup=" + cg + "\n" +
		"FragmentPath=/etc/systemd/system/x.service\n" +
		"ExecStart=...\n" +
		"Result=success\n" +
		"LoadState=loaded\n"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	b := []byte{}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func defaultMockRunner() *mockRunner {
	return &mockRunner{
		ssText: ssFixtureText,
		units: map[string]string{
			"nginx.service":           unitShow("active", 1001, "/system.slice/nginx.service"),
			"hysteria-server.service": unitShow("active", 2002, "/system.slice/hysteria-server.service"),
			"tuic.service":            unitShow("active", 3003, "/system.slice/tuic.service"),
			"adguard.service":         unitShow("active", 4004, "/system.slice/adguard.service"),
			"xray.service":            unitShow("active", 5005, "/system.slice/xray.service"),
		},
		pidToSvc: map[int]string{
			1001: "nginx",
			2002: "hysteria2",
			3003: "tuic",
			4004: "adguard-home",
			5005: "xray",
		},
		versions: map[string]string{
			"/usr/local/bin/hysteria": "Hysteria 2.5.0\n",
			"/usr/local/bin/xray":     "Xray 24.9.1\n",
		},
		ufwText: `Status: active
Logging: on (low)
Default: deny (incoming), allow (outgoing), disabled (routed)
New profiles: skip

To                         Action      From
--                         ------      ----
22/tcp                     ALLOW IN    Anywhere
443/tcp                    ALLOW IN    Anywhere
443/udp                    ALLOW IN    Anywhere
853/tcp                    ALLOW IN    Anywhere
853/udp                    ALLOW IN    Anywhere
8443/udp                   ALLOW IN    Anywhere
`,
	}
}

func TestCollectEndToEnd(t *testing.T) {
	r := defaultMockRunner()
	cfgDir := t.TempDir()
	// Simulate existing config files for some services.
	os.MkdirAll(filepath.Join(cfgDir, "etc", "nginx"), 0o755)
	os.WriteFile(filepath.Join(cfgDir, "etc", "nginx", "nginx.conf"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(cfgDir, "etc", "hysteria"), 0o755)
	os.WriteFile(filepath.Join(cfgDir, "etc", "hysteria", "config.yaml"), []byte("x"), 0o644)

	statePath := filepath.Join(cfgDir, "state.json")
	res, err := Collect(context.Background(), r, Options{
		HostSource:    host.FromDir(filepath.Join(cfgDir, "root")),
		CgroupSource:  cgroup.FromDir(filepath.Join(cfgDir, "root")),
		ServicesPath:  mockServicesPath(t),
		StatePath:     statePath,
		ConfigExists: func(p string) bool {
			return fileExists(filepath.Join(cfgDir, p))
		},
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	st := res.State
	if st.SchemaVersion != 1 {
		t.Errorf("schema_version = %d", st.SchemaVersion)
	}
	if st.CollectorVersion == "" {
		t.Error("collector_version empty")
	}

	// Services count.
	if len(st.Services) != 5 {
		t.Fatalf("services = %d, want 5", len(st.Services))
	}

	// Hysteria2: active, has listener UDP/443, version set.
	h := byID(t, st, "hysteria2")
	if h.Status != state.StatusHealthy {
		t.Errorf("hysteria2 status = %q", h.Status)
	}
	if h.Version != "Hysteria 2.5.0" {
		t.Errorf("hysteria2 version = %q", h.Version)
	}
	if !hasPortProto(h.Listeners, 443, "udp") {
		t.Errorf("hysteria2 listeners = %+v", h.Listeners)
	}
	if h.ConfigPath != "/etc/hysteria/config.yaml" {
		t.Errorf("hysteria2 config path = %q", h.ConfigPath)
	}
	if !h.ConfigExists {
		t.Error("hysteria2 config should exist")
	}

	// Xray has an internal listener only.
	x := byID(t, st, "xray")
	if len(x.Listeners) != 1 || !x.Listeners[0].Internal {
		t.Errorf("xray listeners = %+v, want one internal listener", x.Listeners)
	}

	// Topology: port tree must match the spec exactly.
	tp := st.Topology
	portIDs := []string{}
	for _, n := range tp.Nodes {
		if n.Type == state.NodePort {
			portIDs = append(portIDs, n.Label)
		}
	}
	if len(portIDs) != 3 {
		t.Fatalf("port nodes = %v, want 3", portIDs)
	}
	// 18444 is internal → must never be a top-level port.
	for _, id := range portIDs {
		if id == "18444" {
			t.Error("internal port 18444 leaked into top-level ports")
		}
	}

	// The written state.json must exist and parse.
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed state.State
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("state.json is invalid JSON: %v", err)
	}
	if parsed.Topology.Nodes == nil {
		t.Fatal("written state.json has no topology")
	}
}

// TestCollectSecretExclusion runs the full pipeline against a config that is
// riddled with secrets and asserts none of them reach state.json.
func TestCollectSecretExclusion(t *testing.T) {
	r := defaultMockRunner()
	cfgDir := t.TempDir()
	// Mock config files containing obvious secrets.
	os.MkdirAll(filepath.Join(cfgDir, "etc", "hysteria"), 0o755)
	os.WriteFile(filepath.Join(cfgDir, "etc", "hysteria", "config.yaml"),
		[]byte("password: abc\ntoken: 123\nuuid: xxx\nprivate_key: ...\nsecret: ...\n"), 0o644)

	statePath := filepath.Join(cfgDir, "state.json")
	res, err := Collect(context.Background(), r, Options{
		ServicesPath: mockServicesPath(t),
		StatePath:    statePath,
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if err := res.State.Validate(); err != nil {
		t.Fatalf("state validation failed (secret leaked?): %v", err)
	}

	raw, _ := os.ReadFile(statePath)
	for _, bad := range []string{"password", "token", "uuid", "private_key", "secret", "abc", "123"} {
		if bytesContains(raw, bad) {
			t.Errorf("state.json contains leaked %q", bad)
		}
	}
}

// TestCollectServicesOverride proves services.yaml overrides the friendly name
// without recompiling: same binary, same runtime, different YAML → different
// output.
func TestCollectServicesOverride(t *testing.T) {
	r := defaultMockRunner()
	cfgDir := t.TempDir()
	statePath := filepath.Join(cfgDir, "state.json")

	svcPath := mockServicesPath(t)
	// Rewrite hysteria2's friendly name in the YAML.
	data, _ := os.ReadFile(svcPath)
	rewritten := []byte{}
	for _, line := range splitLines(string(data)) {
		if contains(line, "name: Hysteria2") {
			rewritten = append(rewritten, []byte("    name: Hysteria Two\n")...)
			continue
		}
		rewritten = append(rewritten, []byte(line+"\n")...)
	}
	os.WriteFile(svcPath, rewritten, 0o644)

	res, err := Collect(context.Background(), r, Options{
		ServicesPath: svcPath,
		StatePath:    statePath,
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	h := byID(t, res.State, "hysteria2")
	if h.Name != "Hysteria Two" {
		t.Errorf("name = %q, want Hysteria Two (services.yaml override)", h.Name)
	}
	// The topology must reflect the new friendly name too.
	found := false
	for _, n := range res.State.Topology.Nodes {
		if n.ServiceID == "hysteria2" && n.Label == "Hysteria Two" {
			found = true
		}
	}
	if !found {
		t.Error("topology node label did not reflect services.yaml override")
	}
}

func byID(t *testing.T, st *state.State, id string) *state.Service {
	t.Helper()
	for i := range st.Services {
		if st.Services[i].ID == id {
			return &st.Services[i]
		}
	}
	t.Fatalf("service %q not found", id)
	return nil
}

func hasPortProto(l []state.Listener, port int, proto string) bool {
	for _, ll := range l {
		if ll.Port == port && ll.Protocol == proto && !ll.Internal {
			return true
		}
	}
	return false
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func bytesContains(b []byte, s string) bool {
	return len(b) >= len(s) && (func() bool {
		for i := 0; i+len(s) <= len(b); i++ {
			if string(b[i:i+len(s)]) == s {
				return true
			}
		}
		return false
	})()
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}()
}
