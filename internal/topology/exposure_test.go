package topology

import (
	"reflect"
	"testing"

	svc "github.com/opscockpit/opscockpit/internal/collector/config"
	"github.com/opscockpit/opscockpit/internal/state"
)

// Exposure fixtures matching the audit scenarios.
func expServices() []svc.Service {
	return []svc.Service{
		{ID: "nginx", Name: "Nginx", StatusHint: state.StatusHealthy},
		{ID: "hysteria2", Name: "Hysteria2", StatusHint: state.StatusHealthy},
		{ID: "tuic", Name: "TUIC", StatusHint: state.StatusHealthy},
		{ID: "snell", Name: "Snell", StatusHint: state.StatusHealthy},
	}
}

func TestNATIngressGeneration(t *testing.T) {
	in := Input{
		Services: expServices(),
		Listeners: []Listener{
			{ServiceID: "hysteria2", Protocol: "udp", Port: 443, Address: "::", Exposure: state.ExposureNATIngress},
		},
		NATIngresses: []NATIngress{
			{Protocol: "udp", PortStart: 20000, PortEnd: 20099, TargetPort: 443, ServiceID: "hysteria2"},
		},
	}
	tp, err := Generate(in, Options{IncludeInternetRoot: true})
	if err != nil {
		t.Fatal(err)
	}

	// Internet → 20000–20099 → UDP → Hysteria2.
	portLabels := []string{}
	for _, n := range tp.Nodes {
		if n.Type == state.NodePort {
			portLabels = append(portLabels, n.Label)
		}
	}
	if !reflect.DeepEqual(portLabels, []string{"20000–20099"}) {
		t.Fatalf("port nodes = %v, want [20000–20099]", portLabels)
	}

	// The range node carries port_start/port_end and the NAT target port.
	var rangeNode *state.Node
	for i := range tp.Nodes {
		if tp.Nodes[i].Type == state.NodePort {
			rangeNode = &tp.Nodes[i]
		}
	}
	if rangeNode == nil || rangeNode.PortStart != 20000 || rangeNode.PortEnd != 20099 {
		t.Fatalf("range node = %+v", rangeNode)
	}
	if rangeNode.TargetPort != 443 {
		t.Errorf("target port = %d, want 443", rangeNode.TargetPort)
	}
	if rangeNode.Exposure != state.ExposureNATIngress {
		t.Errorf("exposure = %q", rangeNode.Exposure)
	}

	// The backend listener port 443 must NOT appear as a separate top-level port.
	for _, n := range tp.Nodes {
		if n.Type == state.NodePort && n.PortStart == 443 && n.PortEnd == 443 {
			t.Error("NAT target 443 must not be its own top-level port")
		}
	}

	// The service instance must reference the backend port 443.
	found := false
	for _, n := range tp.Nodes {
		if n.Type == state.NodeService && n.ServiceID == "hysteria2" {
			found = true
			if n.Port != 443 {
				t.Errorf("service instance port = %d, want backend 443", n.Port)
			}
		}
	}
	if !found {
		t.Error("hysteria2 service instance not found")
	}
}

func TestNATIngressSinglePort(t *testing.T) {
	in := Input{
		Services: expServices(),
		Listeners: []Listener{
			{ServiceID: "snell", Protocol: "udp", Port: 17414, Address: "::", Exposure: state.ExposureNATIngress},
		},
		NATIngresses: []NATIngress{
			{Protocol: "udp", PortStart: 8554, PortEnd: 8554, TargetPort: 17414, ServiceID: "snell"},
		},
	}
	tp, _ := Generate(in, Options{IncludeInternetRoot: true})
	labels := []string{}
	for _, n := range tp.Nodes {
		if n.Type == state.NodePort {
			labels = append(labels, n.Label)
		}
	}
	if !reflect.DeepEqual(labels, []string{"8554"}) {
		t.Fatalf("port labels = %v, want [8554]", labels)
	}
	// 17414 (the backend) must not appear.
	for _, n := range tp.Nodes {
		if n.Type == state.NodePort && n.Label == "17414" {
			t.Error("NAT backend 17414 leaked as a top-level port")
		}
	}
}

func TestNATTargetSuppression(t *testing.T) {
	// 8554 → 17414 → Snell. 17414 ALSO has a direct public listener AND a
	// firewall allow in real life, but because it is a REDIRECT target it must
	// be suppressed (rendered only as a backend) unless the service forces
	// direct exposure.
	in := Input{
		Services: expServices(),
		Listeners: []Listener{
			// 17414 is classified nat_ingress by the collect layer because it
			// is a REDIRECT target.
			{ServiceID: "snell", Protocol: "udp", Port: 17414, Address: "::", Exposure: state.ExposureNATIngress},
		},
		NATIngresses: []NATIngress{
			{Protocol: "udp", PortStart: 8554, PortEnd: 8554, TargetPort: 17414, ServiceID: "snell"},
		},
	}
	tp, _ := Generate(in, Options{IncludeInternetRoot: true})
	for _, n := range tp.Nodes {
		if n.Type == state.NodePort && (n.PortStart == 17414 || n.Label == "17414") {
			t.Error("NAT target 17414 must be suppressed as a top-level port")
		}
	}
}

func TestForceDirectPublicOverride(t *testing.T) {
	// With force_direct_public, the 17414 backend ALSO becomes a direct public
	// top-level port alongside the NAT ingress 8554.
	in := Input{
		Services: []svc.Service{
			{ID: "snell", Name: "Snell", StatusHint: state.StatusHealthy, Exposure: &svc.ExposureConfig{Mode: "nat-target", ForceDirectPublic: true}},
		},
		Listeners: []Listener{
			{ServiceID: "snell", Protocol: "udp", Port: 17414, Address: "::", Exposure: state.ExposureDirectPublic},
		},
		NATIngresses: []NATIngress{
			{Protocol: "udp", PortStart: 8554, PortEnd: 8554, TargetPort: 17414, ServiceID: "snell"},
		},
	}
	tp, _ := Generate(in, Options{IncludeInternetRoot: true})
	labels := []string{}
	for _, n := range tp.Nodes {
		if n.Type == state.NodePort {
			labels = append(labels, n.Label)
		}
	}
	want := []string{"8554", "17414"}
	if !reflect.DeepEqual(labels, want) {
		t.Fatalf("port labels = %v, want %v (force_direct_public)", labels, want)
	}
}

func TestPortRangeOrdering(t *testing.T) {
	// Ranges and single ports must sort by (start, end) ascending. 443 is a
	// NAT target (nat_ingress) so it is suppressed; 8443 is a direct public
	// listener; both ranges render as top-level.
	in := Input{
		Services: expServices(),
		Listeners: []Listener{
			{ServiceID: "hysteria2", Protocol: "udp", Port: 443, Address: "::", Exposure: state.ExposureNATIngress},
			{ServiceID: "tuic", Protocol: "udp", Port: 8443, Address: "::", Exposure: state.ExposureDirectPublic},
		},
		NATIngresses: []NATIngress{
			{Protocol: "udp", PortStart: 20000, PortEnd: 20099, TargetPort: 443, ServiceID: "hysteria2"},
			{Protocol: "udp", PortStart: 20100, PortEnd: 20199, TargetPort: 8443, ServiceID: "tuic"},
		},
	}
	tp, _ := Generate(in, Options{IncludeInternetRoot: true})
	labels := []string{}
	for _, n := range tp.Nodes {
		if n.Type == state.NodePort {
			labels = append(labels, n.Label)
		}
	}
	want := []string{"8443", "20000–20099", "20100–20199"}
	if !reflect.DeepEqual(labels, want) {
		t.Fatalf("ordering = %v, want %v", labels, want)
	}
}

func TestDeterministicWithRanges(t *testing.T) {
	in := Input{
		Services: expServices(),
		Listeners: []Listener{
			{ServiceID: "hysteria2", Protocol: "udp", Port: 443, Address: "::", Exposure: state.ExposureNATIngress},
		},
		NATIngresses: []NATIngress{
			{Protocol: "udp", PortStart: 20000, PortEnd: 20099, TargetPort: 443, ServiceID: "hysteria2"},
		},
	}
	a, _ := Generate(in, Options{IncludeInternetRoot: true})
	b, _ := Generate(in, Options{IncludeInternetRoot: true})
	if !reflect.DeepEqual(a, b) {
		t.Fatal("topology with NAT ranges must be deterministic")
	}
}

func TestPortLabel(t *testing.T) {
	if got := state.PortLabel(443, 443); got != "443" {
		t.Errorf("PortLabel(443,443) = %q", got)
	}
	if got := state.PortLabel(20000, 20099); got != "20000–20099" {
		t.Errorf("PortLabel(20000,20099) = %q", got)
	}
}

func TestExplosionGuard(t *testing.T) {
	// 3000 public listeners would normally produce thousands of nodes; the
	// guard must cap the total (allowance for one loop iteration appending a
	// port+protocol+service+edges before the cap re-checks).
	services := []svc.Service{{ID: "s", Name: "S", StatusHint: state.StatusHealthy}}
	listeners := make([]Listener, 0, 3000)
	for i := 0; i < 3000; i++ {
		listeners = append(listeners, Listener{
			ServiceID: "s", Protocol: "tcp", Port: 10000 + i,
			Address: "0.0.0.0", Exposure: state.ExposureDirectPublic,
		})
	}
	tp, err := Generate(Input{Services: services, Listeners: listeners}, Options{IncludeInternetRoot: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(tp.Nodes) > defaultMaxNodes+8 {
		t.Fatalf("nodes = %d, exceeds explosion guard %d", len(tp.Nodes), defaultMaxNodes+8)
	}
	if len(tp.Edges) > defaultMaxEdges+8 {
		t.Fatalf("edges = %d, exceeds explosion guard %d", len(tp.Edges), defaultMaxEdges+8)
	}
	// Deterministic even when capped.
	tp2, _ := Generate(Input{Services: services, Listeners: listeners}, Options{IncludeInternetRoot: true})
	if !reflect.DeepEqual(tp, tp2) {
		t.Fatal("capped topology must be deterministic")
	}
}
