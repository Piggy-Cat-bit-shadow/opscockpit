package deps

import (
	"testing"
)

func TestParseEndpoint(t *testing.T) {
	cases := []struct {
		in       string
		host     string
		port     int
		proto    string
		ok       bool
	}{
		{"127.0.0.1:18444", "127.0.0.1", 18444, "tcp", true},
		{"http://127.0.0.1:18444", "127.0.0.1", 18444, "tcp", true},
		{"127.0.0.1:9443/", "127.0.0.1", 9443, "tcp", true},
		{"[::1]:9050", "::1", 9050, "tcp", true},
		{"udp:127.0.0.1:5353", "127.0.0.1", 5353, "udp", true},
		{"garbage", "", 0, "tcp", false},
		{"host:notaport", "", 0, "tcp", false},
	}
	for _, c := range cases {
		e, ok := ParseEndpoint(c.in)
		if ok != c.ok {
			t.Errorf("ParseEndpoint(%q) ok = %v, want %v", c.in, ok, c.ok)
			continue
		}
		if ok && (e.Host != c.host || e.Port != c.port || e.Proto != c.proto) {
			t.Errorf("ParseEndpoint(%q) = %+v", c.in, e)
		}
	}
}

func TestResolverLoopback(t *testing.T) {
	r := NewResolver([]KnownListener{
		{Host: "127.0.0.1", Port: 3001, ServiceID: "some-container"},
		{Host: "0.0.0.0", Port: 443, ServiceID: "nginx"},
	})
	// Exact loopback match.
	if got := r.Resolve("127.0.0.1:3001"); got != "some-container" {
		t.Errorf("resolve 127.0.0.1:3001 = %q", got)
	}
	// Loopback resolves by port too.
	if got := r.Resolve("localhost:3001"); got != "some-container" {
		t.Errorf("resolve localhost:3001 = %q", got)
	}
	// Non-loopback unresolved.
	if got := r.Resolve("10.0.0.5:3001"); got != "" {
		t.Errorf("resolve 10.0.0.5:3001 = %q, want empty", got)
	}
	if got := r.Resolve("127.0.0.1:9999"); got != "" {
		t.Errorf("resolve unknown loopback port = %q", got)
	}
}

func TestGraphDedupAndCycle(t *testing.T) {
	g := NewGraph(5, 50)
	g.AddEdge("nginx", "xray", "nginx_proxy_pass", "configured", "127.0.0.1:18444")
	g.AddEdge("nginx", "xray", "nginx_proxy_pass", "configured", "127.0.0.1:18444") // dup
	if len(g.Edges["nginx"]) != 1 {
		t.Fatalf("duplicate edge not deduped: %+v", g.Edges["nginx"])
	}
	// Cycle: xray → nginx while nginx → xray exists.
	g.AddEdge("xray", "nginx", "dependency", "inferred", "")
	if len(g.Cycles) != 1 {
		t.Fatalf("cycle not detected: %+v", g.Cycles)
	}
}

func TestGraphSelfEdgeIgnored(t *testing.T) {
	g := NewGraph(5, 50)
	g.AddEdge("nginx", "nginx", "x", "y", "")
	if len(g.Edges) != 0 {
		t.Error("self edge must be ignored")
	}
}

func TestGraphNodeBudget(t *testing.T) {
	g := NewGraph(5, 2)
	g.AddEdge("a", "b", "s", "c", "")
	g.AddEdge("a", "c", "s", "c", "")
	g.AddEdge("a", "d", "s", "c", "")
	if !g.Full() {
		t.Error("node budget should be full after 2 edges with maxNodes=2")
	}
}
