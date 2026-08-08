package topology

import (
	"fmt"
	"reflect"
	"testing"

	svc "github.com/opscockpit/opscockpit/internal/collector/config"
	"github.com/opscockpit/opscockpit/internal/state"
)

// mockServices returns the registry used across tests.
func mockServices() []svc.Service {
	return []svc.Service{
		{ID: "nginx", Name: "Nginx", StatusHint: state.StatusHealthy},
		{ID: "hysteria2", Name: "Hysteria2", StatusHint: state.StatusHealthy},
		{ID: "tuic", Name: "TUIC", StatusHint: state.StatusHealthy},
		{ID: "adguard-home", Name: "AdGuard Home", StatusHint: state.StatusHealthy},
		{ID: "xray", Name: "Xray", StatusHint: state.StatusHealthy},
	}
}

// mockListeners models the spec testdata: 443/TCP nginx, 443/UDP hysteria2,
// 8443/UDP tuic, 853/TCP+UDP adguard, and internal 127.0.0.1:18444 xray.
// Exposure is pre-classified (the collect layer does this from firewall+NAT).
func mockListeners() []Listener {
	return []Listener{
		{ServiceID: "nginx", Protocol: "tcp", Port: 443, Address: "0.0.0.0", Exposure: state.ExposureDirectPublic},
		{ServiceID: "hysteria2", Protocol: "udp", Port: 443, Address: "::", Exposure: state.ExposureDirectPublic},
		{ServiceID: "tuic", Protocol: "udp", Port: 8443, Address: "::", Exposure: state.ExposureDirectPublic},
		{ServiceID: "adguard-home", Protocol: "tcp", Port: 853, Address: "0.0.0.0", Exposure: state.ExposureDirectPublic},
		{ServiceID: "adguard-home", Protocol: "udp", Port: 853, Address: "0.0.0.0", Exposure: state.ExposureDirectPublic},
		{ServiceID: "xray", Protocol: "tcp", Port: 18444, Address: "127.0.0.1", Internal: true, Exposure: state.ExposureInternal},
	}
}

func TestGenerateFullTopology(t *testing.T) {
	in := Input{
		Services: mockServices(),
		Listeners: mockListeners(),
		Dependencies: map[string][]Dependency{
			"nginx": {{TargetServiceID: "xray", Source: state.EvidenceNginxProxyPass, Confidence: state.ConfidenceConfigured}},
		},
	}
	tp, err := Generate(in, Options{IncludeInternetRoot: true})
	if err != nil {
		t.Fatal(err)
	}

	// Expect node counts:
	// internet(1) + ports 443,853,8443 (3) + protocols 443(tcp,udp) 853(tcp,udp) 8443(udp) (5)
	// + services: 443tcp nginx, 443udp hysteria2, 853tcp adguard, 853udp adguard, 8443udp tuic (5)
	// + dependency xray node (1) = 15
	if len(tp.Nodes) != 15 {
		t.Fatalf("nodes = %d, want 15: %+v", len(tp.Nodes), nodeIDs(tp.Nodes))
	}

	// Internet root exists.
	if tp.Nodes[0].ID != "internet" || tp.Nodes[0].Type != state.NodeInternet {
		t.Errorf("node[0] = %+v, want internet root", tp.Nodes[0])
	}

	// Port order must be ascending: 443, 853, 8443.
	ports := []string{}
	for _, n := range tp.Nodes {
		if n.Type == state.NodePort {
			ports = append(ports, n.Label)
		}
	}
	if !reflect.DeepEqual(ports, []string{"443", "853", "8443"}) {
		t.Errorf("port order = %v, want [443 853 8443]", ports)
	}

	// AdGuard Home must be a single service object across two instances.
	adguardInstances := 0
	for _, n := range tp.Nodes {
		if n.ServiceID == "adguard-home" {
			adguardInstances++
			if n.Type != state.NodeService {
				t.Errorf("adguard instance node has type %q", n.Type)
			}
		}
	}
	if adguardInstances != 2 {
		t.Errorf("adguard instances = %d, want 2", adguardInstances)
	}

	// Nginx → Xray dependency edge exists with nginx_proxy_pass evidence.
	foundDep := false
	for _, e := range tp.Edges {
		if e.Source == "nginx@tcp:443" && e.Target == "xray@dep:tcp:18444" {
			foundDep = true
			if e.Evidence == nil || e.Evidence.Source != state.EvidenceNginxProxyPass {
				t.Errorf("dep edge evidence = %+v", e.Evidence)
			}
		}
	}
	if !foundDep {
		t.Errorf("nginx→xray dependency edge not found; edges = %+v", edgeIDs(tp.Edges))
	}

	// No edge may target the loopback port 18444 as a top-level port.
	for _, n := range tp.Nodes {
		if n.ID == "port-18444" {
			t.Error("loopback port 18444 must not appear as a top-level port node")
		}
	}
}

func TestGenerateDeterministic(t *testing.T) {
	in := Input{
		Services: mockServices(),
		Listeners: mockListeners(),
		Dependencies: map[string][]Dependency{
			"nginx": {{TargetServiceID: "xray", Source: state.EvidenceNginxProxyPass, Confidence: state.ConfidenceConfigured}},
		},
	}
	a, _ := Generate(in, Options{IncludeInternetRoot: true})
	b, _ := Generate(in, Options{IncludeInternetRoot: true})
	if !reflect.DeepEqual(a, b) {
		t.Fatal("topology generation must be deterministic")
	}
}

// TestRuntimeChange is the critical "no hardcoded topology" test: only the
// runtime fixture changes (443→9443); code, registry and frontend stay the
// same, and the generated topology must change accordingly.
func TestRuntimeChange(t *testing.T) {
	services := mockServices()

	// Fixture A: Hysteria on UDP/443.
	a := Input{
		Services: services,
		Listeners: []Listener{
			{ServiceID: "hysteria2", Protocol: "udp", Port: 443, Address: "::", Exposure: state.ExposureDirectPublic},
		},
	}
	ta, _ := Generate(a, Options{IncludeInternetRoot: true})

	// Fixture B: Hysteria on UDP/9443 — nothing else changes.
	b := Input{
		Services: services,
		Listeners: []Listener{
			{ServiceID: "hysteria2", Protocol: "udp", Port: 9443, Address: "::", Exposure: state.ExposureDirectPublic},
		},
	}
	tb, _ := Generate(b, Options{IncludeInternetRoot: true})

	hasPort := func(tp state.Topology, port int) bool {
		for _, n := range tp.Nodes {
			if n.Type == state.NodePort && n.Port == port {
				return true
			}
		}
		return false
	}

	if !hasPort(ta, 443) || hasPort(ta, 9443) {
		t.Errorf("fixture A ports wrong: 443=%v 9443=%v", hasPort(ta, 443), hasPort(ta, 9443))
	}
	if !hasPort(tb, 9443) || hasPort(tb, 443) {
		t.Errorf("fixture B ports wrong: 443=%v 9443=%v", hasPort(tb, 443), hasPort(tb, 9443))
	}

	// Service instance labels reflect the new port.
	var instA, instB string
	for _, n := range ta.Nodes {
		if n.Type == state.NodeService {
			instA = n.ID
		}
	}
	for _, n := range tb.Nodes {
		if n.Type == state.NodeService {
			instB = n.ID
		}
	}
	if instA != "hysteria2@udp:443" {
		t.Errorf("fixture A instance = %q", instA)
	}
	if instB != "hysteria2@udp:9443" {
		t.Errorf("fixture B instance = %q", instB)
	}
}

func TestInternalListenerNeverTopLevel(t *testing.T) {
	in := Input{
		Services: mockServices(),
		Listeners: []Listener{
			{ServiceID: "xray", Protocol: "tcp", Port: 18444, Address: "127.0.0.1", Internal: true},
		},
	}
	tp, _ := Generate(in, Options{IncludeInternetRoot: true})
	for _, n := range tp.Nodes {
		if n.Type == state.NodePort {
			t.Errorf("internal-only runtime produced a port node %+v", n)
		}
	}
}

func TestDuplicateProtocolServicePair(t *testing.T) {
	// Two listeners for the same service/proto/port (e.g. IPv4 + IPv6 any)
	// must collapse to a single instance node.
	in := Input{
		Services: []svc.Service{{ID: "nginx", Name: "Nginx", StatusHint: state.StatusHealthy}},
		Listeners: []Listener{
			{ServiceID: "nginx", Protocol: "tcp", Port: 443, Address: "0.0.0.0", Exposure: state.ExposureDirectPublic},
			{ServiceID: "nginx", Protocol: "tcp", Port: 443, Address: "::", Exposure: state.ExposureDirectPublic},
		},
	}
	tp, _ := Generate(in, Options{IncludeInternetRoot: true})
	svc := 0
	for _, n := range tp.Nodes {
		if n.Type == state.NodeService {
			svc++
		}
	}
	if svc != 1 {
		t.Errorf("duplicate tcp/443 nginx produced %d service nodes, want 1", svc)
	}
}

func TestUnknownServiceSkipped(t *testing.T) {
	in := Input{
		Services: []svc.Service{{ID: "nginx", Name: "Nginx"}},
		Listeners: []Listener{
			{ServiceID: "unknown-svc", Protocol: "tcp", Port: 9999, Address: "0.0.0.0"},
		},
	}
	tp, _ := Generate(in, Options{IncludeInternetRoot: true})
	if len(tp.Nodes) != 1 {
		t.Fatalf("unregistered listener leaked into topology: %+v", nodeIDs(tp.Nodes))
	}
}

func nodeIDs(nodes []state.Node) []string {
	var ids []string
	for _, n := range nodes {
		ids = append(ids, fmt.Sprintf("%s(%s)", n.ID, n.Type))
	}
	return ids
}

func edgeIDs(edges []state.Edge) []string {
	var ids []string
	for _, e := range edges {
		ids = append(ids, fmt.Sprintf("%s→%s", e.Source, e.Target))
	}
	return ids
}
