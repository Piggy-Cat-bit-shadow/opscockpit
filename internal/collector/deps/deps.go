// Package deps resolves "host:port/protocol" endpoints to registered services
// and builds dependency edges (Nginx → downstream, Docker → container, etc.)
// with cycle detection and a bounded depth.
//
// The resolver is shared by Nginx, NAT, and Docker adapters — no per-product
// hardcoding. Endpoints never invent services; an unresolved endpoint
// terminates a chain.
package deps

import (
	"strconv"
	"strings"
)

// Endpoint is a resolved "host:port" pair.
type Endpoint struct {
	Host  string
	Port  int
	Proto string // tcp | udp ("" = tcp)
}

// ParseEndpoint parses "host:port", "[v6]:port", "127.0.0.1:18444", or a
// "udp:host:port" prefixed form.
func ParseEndpoint(s string) (Endpoint, bool) {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/"); i >= 0 {
		s = s[:i]
	}
	proto := "tcp"
	if strings.HasPrefix(s, "udp:") {
		proto = "udp"
		s = strings.TrimPrefix(s, "udp:")
	}
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end < 0 || end+1 >= len(s) || s[end+1] != ':' {
			return Endpoint{}, false
		}
		port, err := strconv.Atoi(s[end+2:])
		if err != nil {
			return Endpoint{}, false
		}
		return Endpoint{Host: s[1:end], Port: port, Proto: proto}, true
	}
	idx := strings.LastIndex(s, ":")
	if idx <= 0 {
		return Endpoint{}, false
	}
	port, err := strconv.Atoi(s[idx+1:])
	if err != nil {
		return Endpoint{}, false
	}
	return Endpoint{Host: s[:idx], Port: port, Proto: proto}, true
}

// KnownListener is a runtime listener the resolver can match endpoints to.
type KnownListener struct {
	Host      string
	Port      int
	ServiceID string
	Loopback  bool
}

// Resolver maps an endpoint to a registered service id.
type Resolver struct {
	byEndpoint     map[string]string // "host:port" → service id
	byLoopbackPort map[string]string // "port" → service id (127.0.0.1 bindings)
}

// NewResolver builds a resolver from known listeners.
func NewResolver(listeners []KnownListener) *Resolver {
	r := &Resolver{byEndpoint: map[string]string{}, byLoopbackPort: map[string]string{}}
	for _, l := range listeners {
		r.byEndpoint[l.Host+":"+strconv.Itoa(l.Port)] = l.ServiceID
		if l.Host == "127.0.0.1" || l.Host == "::1" || l.Host == "localhost" || strings.HasPrefix(l.Host, "127.") || l.Loopback {
			r.byLoopbackPort[strconv.Itoa(l.Port)] = l.ServiceID
		}
	}
	return r
}

// Resolve maps an endpoint string to a service id ("" if unknown). Loopback
// endpoints resolve by port even when the host literal differs.
func (r *Resolver) Resolve(ep string) string {
	e, ok := ParseEndpoint(ep)
	if !ok {
		return ""
	}
	key := e.Host + ":" + strconv.Itoa(e.Port)
	if id, ok := r.byEndpoint[key]; ok {
		return id
	}
	if e.Host == "127.0.0.1" || e.Host == "::1" || e.Host == "localhost" || strings.HasPrefix(e.Host, "127.") {
		if id, ok := r.byLoopbackPort[strconv.Itoa(e.Port)]; ok {
			return id
		}
	}
	return ""
}

// Dependency is one directed dependency edge.
type Dependency struct {
	TargetServiceID string
	Source          string // evidence kind
	Confidence      string
	Endpoint        string // the host:port that linked them
}

// Graph tracks dependency edges with cycle detection and depth bounds.
type Graph struct {
	MaxDepth int
	MaxNodes int
	Edges    map[string][]Dependency
	Cycles   []string
}

// NewGraph returns a bounded graph. MaxDepth caps chain traversal (4–6),
// MaxNodes caps total services (explosion protection).
func NewGraph(maxDepth, maxNodes int) *Graph {
	return &Graph{MaxDepth: maxDepth, MaxNodes: maxNodes, Edges: map[string][]Dependency{}}
}

// AddEdge records serviceID → dep (deduped, cycle-checked, node-bounded).
func (g *Graph) AddEdge(serviceID, depServiceID, source, confidence, endpoint string) {
	if g.Full() {
		return // budget exhausted
	}
	if serviceID == "" || depServiceID == "" || serviceID == depServiceID {
		return
	}
	for _, d := range g.Edges[serviceID] {
		if d.TargetServiceID == depServiceID {
			return // already recorded
		}
	}
	// Cycle: dep → ... → serviceID already exists.
	if g.reaches(depServiceID, serviceID) {
		g.Cycles = append(g.Cycles, serviceID+"→"+depServiceID)
		return
	}
	g.Edges[serviceID] = append(g.Edges[serviceID], Dependency{
		TargetServiceID: depServiceID,
		Source:          source,
		Confidence:      confidence,
		Endpoint:        endpoint,
	})
}

// reaches reports whether there is a path from → to in the graph.
func (g *Graph) reaches(from, to string) bool {
	if from == to {
		return true
	}
	seen := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, d := range g.Edges[cur] {
			if d.TargetServiceID == to {
				return true
			}
			if !seen[d.TargetServiceID] {
				seen[d.TargetServiceID] = true
				stack = append(stack, d.TargetServiceID)
			}
		}
	}
	return false
}

// Full reports whether the dependency budget is exhausted. The budget bounds
// the total number of dependency edges across all services (explosion guard).
func (g *Graph) Full() bool {
	total := 0
	for _, deps := range g.Edges {
		total += len(deps)
	}
	return total >= g.MaxNodes
}
