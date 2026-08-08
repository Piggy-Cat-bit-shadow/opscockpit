// Package topology builds the data-driven port tree from runtime listeners,
// firewall/NAT exposure evidence, and the service registry.
//
// The generator is pure and deterministic: given the same runtime + services,
// it always produces the same nodes and edges in the same order. The frontend
// knows nothing about individual services — it only renders PortNode,
// ProtocolNode and ServiceNode from this data.
//
// Exposure is NOT decided here: the collect layer classifies each listener
// (direct_public, nat_ingress, internal, unknown) using firewall + NAT +
// services.yaml override. This package only renders what it is told.
//
// Two shapes can appear:
//
//	Direct public:
//	  Internet → Port 443 → TCP → Nginx
//
//	NAT ingress (firewall allows 20000-20099, REDIRECT → 443, Hysteria listens):
//	  Internet → Port 20000–20099 → UDP → Hysteria2
//
// Internal listeners and NAT-backend targets never become their own top-level
// port unless the service explicitly forces direct exposure.
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
	// Listeners discovered at runtime, already exposure-classified by the
	// collect layer.
	Listeners []Listener
	// NATIngresses are public NAT redirects, already resolved to a service id.
	NATIngresses []NATIngress
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
	// Exposure is the classification assigned by the collect layer.
	Exposure string
}

// NATIngress is a public redirect ingress resolved to a registered service.
type NATIngress struct {
	Protocol        string
	PortStart       int
	PortEnd         int
	TargetPort      int
	ServiceID       string
}

// Dependency is an internal service dependency with evidence.
type Dependency struct {
	TargetServiceID string
	Source          string // evidence kind
	Confidence      string
}

// protoSvc binds a protocol to a service id (and the backend port it listens on).
type protoSvc struct {
	protocol  string
	serviceID string
	backendPort int
}

// Options tune generation.
type Options struct {
	// IncludeInternetRoot adds the Internet root node.
	IncludeInternetRoot bool
	// MaxNodes bounds the total node count (explosion guard). 0 = default 500.
	MaxNodes int
	// MaxEdges bounds the total edge count. 0 = default 1000.
	MaxEdges int
}

// Defaults guard against pathological configs producing thousands of nodes.
const (
	defaultMaxNodes = 500
	defaultMaxEdges = 1000
)

// ID schemes (stable across runs).
const internetID = "internet"

func portNodeID(start, end int) string {
	if start == end {
		return fmt.Sprintf("port-%d", start)
	}
	return fmt.Sprintf("port-%d-%d", start, end)
}

func protocolNodeID(start, end int, p string) string {
	return fmt.Sprintf("%s-%s", portNodeID(start, end), p)
}

func serviceInstanceID(serviceID, protocol string, backendPort int) string {
	return fmt.Sprintf("%s@%s:%d", serviceID, protocol, backendPort)
}

func depInstanceID(serviceID, proto string, port int) string {
	return fmt.Sprintf("%s@dep:%s:%d", serviceID, proto, port)
}

// Generate produces the deterministic port tree.
func Generate(in Input, opts Options) (state.Topology, error) {
	t := state.Topology{}
	maxNodes := opts.MaxNodes
	if maxNodes <= 0 {
		maxNodes = defaultMaxNodes
	}
	maxEdges := opts.MaxEdges
	if maxEdges <= 0 {
		maxEdges = defaultMaxEdges
	}
	// bounded stops appending when the explosion guard is hit. Deterministic:
	// the same pathological input always stops at the same point.
	bounded := func() bool { return len(t.Nodes) >= maxNodes || len(t.Edges) >= maxEdges }

	byID := make(map[string]svc.Service, len(in.Services))
	for _, s := range in.Services {
		byID[s.ID] = s
	}

	// ---- Group 1: direct-public listeners → top-level port tree.
	type portGroup struct {
		start  int
		end    int
		protos []protoSvc
	}
	var groups []portGroup
	groupKey := map[string]int{} // "start:end" → index

	addProto := func(groups []portGroup, key string, start, end int, ps protoSvc) []portGroup {
		if idx, ok := groupKey[key]; ok {
			groups[idx].protos = append(groups[idx].protos, ps)
			return groups
		}
		groupKey[key] = len(groups)
		groups = append(groups, portGroup{start: start, end: end, protos: []protoSvc{ps}})
		return groups
	}

	for _, l := range in.Listeners {
		if l.Exposure != state.ExposureDirectPublic {
			continue // nat targets, internal, unknown, filtered → not top-level
		}
		if l.Port <= 0 || l.Port > 65535 {
			continue
		}
		if _, exists := byID[l.ServiceID]; !exists {
			continue
		}
		key := fmt.Sprintf("%d:%d", l.Port, l.Port)
		groups = addProto(groups, key, l.Port, l.Port, protoSvc{protocol: l.Protocol, serviceID: l.ServiceID, backendPort: l.Port})
	}

	// ---- Group 2: NAT ingresses → top-level port/range tree.
	for _, ing := range in.NATIngresses {
		if ing.PortStart <= 0 || ing.PortStart > 65535 {
			continue
		}
		if ing.PortEnd < ing.PortStart {
			ing.PortEnd = ing.PortStart
		}
		if _, exists := byID[ing.ServiceID]; !exists {
			continue
		}
		key := fmt.Sprintf("%d:%d", ing.PortStart, ing.PortEnd)
		groups = addProto(groups, key, ing.PortStart, ing.PortEnd, protoSvc{
			protocol:    ing.Protocol,
			serviceID:   ing.ServiceID,
			backendPort: ing.TargetPort,
		})
	}

	// Deterministic ordering: by (start, end) ascending, then TCP before UDP,
	// then service id.
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].start != groups[j].start {
			return groups[i].start < groups[j].start
		}
		if groups[i].end != groups[j].end {
			return groups[i].end < groups[j].end
		}
		return false
	})
	for i := range groups {
		sort.SliceStable(groups[i].protos, func(a, b int) bool {
			if groups[i].protos[a].protocol != groups[i].protos[b].protocol {
				return groups[i].protos[a].protocol < groups[i].protos[b].protocol
			}
			return groups[i].protos[a].serviceID < groups[i].protos[b].serviceID
		})
	}

	// Merge consecutive single-port groups? No — keep explicit (start,end).
	// Deduplicate identical (start,end,protocol,serviceID,backendPort) pairs.
	for i := range groups {
		groups[i].protos = dedupProtos(groups[i].protos)
	}

	if opts.IncludeInternetRoot {
		t.Nodes = append(t.Nodes, state.Node{
			ID:    internetID,
			Type:  state.NodeInternet,
			Label: "Internet",
		})
	}

	emittedDep := map[string]bool{}

	for _, g := range groups {
		if bounded() {
			break
		}
		pid := portNodeID(g.start, g.end)
		label := state.PortLabel(g.start, g.end)

		// Determine exposure + target for this port group (from NAT ingress if
		// this is a range, else from direct listener).
		exposure := state.ExposureDirectPublic
		var targetPort int
		if g.start != g.end {
			exposure = state.ExposureNATIngress
			// Find the NAT ingress for this range to attach the target port.
			for _, ing := range in.NATIngresses {
				if ing.PortStart == g.start && ing.PortEnd == g.end {
					targetPort = ing.TargetPort
					break
				}
			}
		}

		portNode := state.Node{
			ID:         pid,
			Type:       state.NodePort,
			Label:      label,
			PortStart:  g.start,
			PortEnd:    g.end,
			Port:       g.start,
			Exposure:   exposure,
			TargetPort: targetPort,
		}
		t.Nodes = append(t.Nodes, portNode)
		if opts.IncludeInternetRoot {
			evidence := &state.Evidence{Source: state.EvidenceFirewall, Confidence: state.ConfidenceConfirmed}
			if g.start != g.end {
				evidence = &state.Evidence{Source: state.EvidenceIptablesRedirect, Confidence: state.ConfidenceConfirmed}
			}
			t.Edges = append(t.Edges, state.Edge{
				ID:     fmt.Sprintf("e-%s-%s", internetID, pid),
				Source: internetID,
				Target: pid,
				Evidence: evidence,
			})
		}

		for _, ps := range g.protos {
			pID := protocolNodeID(g.start, g.end, ps.protocol)
			t.Nodes = append(t.Nodes, state.Node{
				ID:       pID,
				Type:     state.NodeProtocol,
				Label:    upperProto(ps.protocol),
				Protocol: ps.protocol,
				Port:     g.start,
				PortStart: g.start,
				PortEnd:   g.end,
			})
			t.Edges = append(t.Edges, state.Edge{
				ID:     fmt.Sprintf("e-%s-%s", pid, pID),
				Source: pid,
				Target: pID,
				Evidence: &state.Evidence{
					Source:     state.EvidenceRuntimeListener,
					Confidence: state.ConfidenceConfirmed,
				},
			})

			entry := byID[ps.serviceID]
			instID := serviceInstanceID(ps.serviceID, ps.protocol, ps.backendPort)
			t.Nodes = append(t.Nodes, state.Node{
				ID:        instID,
				Type:      state.NodeService,
				Label:     entry.Name,
				ServiceID: entry.ID,
				Protocol:  ps.protocol,
				Port:      ps.backendPort,
				// The ingress range the service is reached on (for a NAT range
				// this differs from the backend port; the frontend groups by it).
				PortStart: g.start,
				PortEnd:   g.end,
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
		key := fmt.Sprintf("%s/%s/%d", p.protocol, p.serviceID, p.backendPort)
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
