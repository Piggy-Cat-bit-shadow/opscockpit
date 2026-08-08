// Package topology builds the data-driven port tree from runtime listeners and
// service registry entries.
//
// The generator is pure and deterministic: given the same runtime + services,
// it always produces the same nodes and edges in the same order. The frontend
// knows nothing about individual services — it only renders PortNode,
// ProtocolNode and ServiceNode from this data.
//
// Tree shape:
//
//	Internet
//	  └─ Port 443
//	       ├─ TCP → Nginx → (Xray, via internal dependency)
//	       └─ UDP → Hysteria2
//
// Internal listeners (loopback) never become top-level ports.
package topology

import (
	"fmt"
	"sort"

	svc "github.com/opscockpit/opscockpit/internal/collector/config"
	"github.com/opscockpit/opscockpit/internal/state"
)
// Input is everything the generator needs. Collectors produce it; tests build
// it directly.
type Input struct {
	// Services in registry order (the config's Services slice).
	Services []svc.Service
	// Listeners discovered at runtime (public and internal).
	Listeners []Listener
	// Dependencies maps a service id to its internal dependencies (other
	// service ids). Evidence records how the link was found.
	Dependencies map[string][]Dependency
}

// Listener is a runtime socket in topology terms.
type Listener struct {
	ServiceID string
	Protocol  string // tcp | udp
	Port      int
	Address   string
	Internal  bool
	PID       int
	Process   string
}

// Dependency is an internal service dependency with evidence.
type Dependency struct {
	TargetServiceID string
	Source          string // evidence kind
	Confidence      string
}

// protoSvc binds a protocol+port to a service id.
type protoSvc struct {
	protocol  string
	serviceID string
}

// Options tune generation.
type Options struct {
	// IncludeInternetRoot adds the Internet root node.
	IncludeInternetRoot bool
}

// ID schemes (stable across runs).
const internetID = "internet"

func portNodeID(port int) string { return fmt.Sprintf("port-%d", port) }
func protocolNodeID(port int, p string) string {
	return fmt.Sprintf("port-%d-%s", port, p)
}
func serviceInstanceID(serviceID, protocol string, port int) string {
	return fmt.Sprintf("%s@%s:%d", serviceID, protocol, port)
}
func depInstanceID(serviceID, proto string, port int) string {
	return fmt.Sprintf("%s@dep:%s:%d", serviceID, proto, port)
}

// Generate produces the deterministic port tree.
func Generate(in Input, opts Options) (state.Topology, error) {
	t := state.Topology{}

	byID := make(map[string]svc.Service, len(in.Services))
	for _, s := range in.Services {
		byID[s.ID] = s
	}

	// Public ports → protocol/service pairs. Internal listeners are excluded
	// entirely: a loopback port can never be a top-level Internet port.
	type portGroup struct {
		port   int
		protos []protoSvc
	}
	var groups []portGroup
	seenPorts := map[int]bool{}

	for _, l := range in.Listeners {
		if l.Internal || l.Port <= 0 || l.Port > 65535 {
			continue
		}
		if _, exists := byID[l.ServiceID]; !exists {
			continue
		}
		if !seenPorts[l.Port] {
			seenPorts[l.Port] = true
			groups = append(groups, portGroup{port: l.Port})
		}
	}
	for i := range groups {
		for _, l := range in.Listeners {
			if l.Internal || l.Port != groups[i].port {
				continue
			}
			if _, exists := byID[l.ServiceID]; !exists {
				continue
			}
			groups[i].protos = append(groups[i].protos, protoSvc{protocol: l.Protocol, serviceID: l.ServiceID})
		}
		groups[i].protos = dedupProtos(groups[i].protos)
	}

	// Deterministic ordering: port ascending, then TCP before UDP, then
	// service id.
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].port < groups[j].port })
	for i := range groups {
		sort.SliceStable(groups[i].protos, func(a, b int) bool {
			if groups[i].protos[a].protocol != groups[i].protos[b].protocol {
				return groups[i].protos[a].protocol < groups[i].protos[b].protocol
			}
			return groups[i].protos[a].serviceID < groups[i].protos[b].serviceID
		})
	}

	if opts.IncludeInternetRoot {
		t.Nodes = append(t.Nodes, state.Node{
			ID:    internetID,
			Type:  state.NodeInternet,
			Label: "Internet",
		})
	}

	// Dependency instances already emitted per service+proto+port, to avoid
	// duplicate nodes when the same target is reached twice.
	emittedDep := map[string]bool{}

	for _, g := range groups {
		portID := portNodeID(g.port)
		t.Nodes = append(t.Nodes, state.Node{
			ID:    portID,
			Type:  state.NodePort,
			Label: fmt.Sprintf("%d", g.port),
			Port:  g.port,
		})
		if opts.IncludeInternetRoot {
			t.Edges = append(t.Edges, state.Edge{
				ID:     fmt.Sprintf("e-%s-%s", internetID, portID),
				Source: internetID,
				Target: portID,
				Evidence: &state.Evidence{
					Source:     state.EvidenceRuntimeListener,
					Confidence: state.ConfidenceConfirmed,
				},
			})
		}

		for _, ps := range g.protos {
			pID := protocolNodeID(g.port, ps.protocol)
			t.Nodes = append(t.Nodes, state.Node{
				ID:       pID,
				Type:     state.NodeProtocol,
				Label:    upperProto(ps.protocol),
				Protocol: ps.protocol,
				Port:     g.port,
			})
			t.Edges = append(t.Edges, state.Edge{
				ID:     fmt.Sprintf("e-%s-%s", portID, pID),
				Source: portID,
				Target: pID,
				Evidence: &state.Evidence{
					Source:     state.EvidenceRuntimeListener,
					Confidence: state.ConfidenceConfirmed,
				},
			})

			entry := byID[ps.serviceID]
			instID := serviceInstanceID(ps.serviceID, ps.protocol, g.port)
			t.Nodes = append(t.Nodes, state.Node{
				ID:        instID,
				Type:      state.NodeService,
				Label:     entry.Name,
				ServiceID: entry.ID,
				Protocol:  ps.protocol,
				Port:      g.port,
				Status:    normStatus(entry.StatusHint),
			})
			t.Edges = append(t.Edges, state.Edge{
				ID:     fmt.Sprintf("e-%s-%s", pID, instID),
				Source: pID,
				Target: instID,
				Evidence: &state.Evidence{
					Source:     state.EvidenceRuntimeListener,
					Confidence: state.ConfidenceConfirmed,
				},
			})

			// Internal dependencies from this service instance.
			for _, dep := range in.Dependencies[ps.serviceID] {
				depSvc, exists := byID[dep.TargetServiceID]
				if !exists {
					continue
				}
				// Find a runtime listener instance for the target.
				dp, dport, found := targetInstance(in, dep.TargetServiceID)
				if !found {
					continue
				}
				depID := depInstanceID(dep.TargetServiceID, dp, dport)
				if !emittedDep[depID] {
					emittedDep[depID] = true
					t.Nodes = append(t.Nodes, state.Node{
						ID:        depID,
						Type:      state.NodeService,
						Label:     depSvc.Name,
						ServiceID: depSvc.ID,
						Status:    normStatus(depSvc.StatusHint),
					})
				}
				t.Edges = append(t.Edges, state.Edge{
					ID:     fmt.Sprintf("e-%s-%s", instID, depID),
					Source: instID,
					Target: depID,
					Evidence: &state.Evidence{
						Source:     dep.Source,
						Confidence: dep.Confidence,
					},
				})
			}
		}
	}

	return t, nil
}

// targetInstance finds a rendered listener instance for a dependency target.
func targetInstance(in Input, serviceID string) (proto string, port int, found bool) {
	for _, l := range in.Listeners {
		if l.ServiceID == serviceID {
			return l.Protocol, l.Port, true
		}
	}
	return "", 0, false
}

func dedupProtos(in []protoSvc) []protoSvc {
	seen := map[string]bool{}
	var out []protoSvc
	for _, p := range in {
		key := p.protocol + "/" + p.serviceID
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}
	return out
}

func upperProto(p string) string {
	if p == "tcp" {
		return "TCP"
	}
	if p == "udp" {
		return "UDP"
	}
	return p
}

func normStatus(hint string) string {
	switch hint {
	case state.StatusHealthy, state.StatusWarning, state.StatusFailed, state.StatusUnknown, "":
		return hint
	default:
		return state.StatusUnknown
	}
}
