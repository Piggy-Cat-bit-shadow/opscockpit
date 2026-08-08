package network

import (
	"context"
	"testing"
)

// fixtures use documentation ranges only (203.0.113.x).
const addrFixture = `[
  {"ifname":"lo","addr_info":[{"family":"inet","local":"127.0.0.1","prefixlen":8,"scope":"host"},{"family":"inet6","local":"::1","prefixlen":128,"scope":"host"}]},
  {"ifname":"eth0","addr_info":[{"family":"inet","local":"203.0.113.10","prefixlen":24,"scope":"global"},{"family":"inet6","local":"2001:db8::10","prefixlen":64,"scope":"global"}]},
  {"ifname":"docker0","addr_info":[{"family":"inet","local":"172.17.0.1","prefixlen":16,"scope":"global"}]}
]`

const routeFixture = `[
  {"dst":"default","dev":"eth0","family":"inet"},
  {"dst":"default","dev":"eth0","family":"inet6"},
  {"dst":"203.0.113.0/24","dev":"eth0","family":"inet"}
]`

type mockRunner struct {
	addr, route string
	err         error
}

func (m *mockRunner) IPAddrJSON(ctx context.Context) (string, error)  { return m.addr, m.err }
func (m *mockRunner) IPRouteJSON(ctx context.Context) (string, error) { return m.route, m.err }

func TestParseAddrJSON(t *testing.T) {
	addrs := parseAddrJSON(addrFixture)
	if len(addrs) != 5 {
		t.Fatalf("addrs = %d, want 5: %+v", len(addrs), addrs)
	}
	// eth0 global v4
	found := false
	for _, a := range addrs {
		if a.Interface == "eth0" && a.Address == "203.0.113.10" {
			found = true
			if a.Family != "ipv4" || a.Scope != "global" || a.IsLoopback {
				t.Errorf("eth0 v4 addr = %+v", a)
			}
		}
	}
	if !found {
		t.Error("eth0 global v4 address not parsed")
	}
	// loopback marked
	for _, a := range addrs {
		if a.Address == "127.0.0.1" && !a.IsLoopback {
			t.Error("127.0.0.1 must be loopback")
		}
		if a.Address == "::1" && !a.IsLoopback {
			t.Error("::1 must be loopback")
		}
	}
}

func TestParseRouteJSON(t *testing.T) {
	routes := parseRouteJSON(routeFixture)
	defaults := 0
	for _, r := range routes {
		if r.IsDefault {
			defaults++
		}
	}
	if defaults != 2 {
		t.Fatalf("default routes = %d, want 2 (v4+v6): %+v", defaults, routes)
	}
}

func TestCollectDerivesIdentity(t *testing.T) {
	id := Collect(context.Background(), &mockRunner{addr: addrFixture, route: routeFixture})
	if !id.HasGlobalIPv4 {
		t.Error("should have global IPv4")
	}
	if !id.HasGlobalIPv6 {
		t.Error("should have global IPv6")
	}
	if !id.HasDefaultIPv4Route || !id.HasDefaultIPv6Route {
		t.Error("should have both default routes")
	}
	if !id.OwnsAddress("203.0.113.10") {
		t.Error("should own eth0 address")
	}
	if id.OwnsAddress("203.0.113.99") {
		t.Error("must not own an address that is not on this host")
	}
}

func TestCollectUnavailable(t *testing.T) {
	id := Collect(context.Background(), &mockRunner{err: errIP})
	if id.HasGlobalIPv4 || id.HasGlobalIPv6 {
		t.Error("unavailable ip must yield empty identity")
	}
	if id.OwnsAddress("203.0.113.10") {
		t.Error("empty identity must not own addresses")
	}
}

func TestHasAddressFamily(t *testing.T) {
	id := Collect(context.Background(), &mockRunner{addr: addrFixture, route: routeFixture})
	if !id.HasAddressFamily("ipv4") || !id.HasAddressFamily("ipv6") {
		t.Error("should have both families")
	}
	// IPv6-only host: no global v6.
	v6only := Collect(context.Background(), &mockRunner{
		addr: `[{"ifname":"eth0","addr_info":[{"family":"inet6","local":"2001:db8::10","prefixlen":64,"scope":"global"}]}]`,
		route: `[{"dst":"default","dev":"eth0","family":"inet6"}]`,
	})
	if v6only.HasAddressFamily("ipv4") {
		t.Error("v6-only host must not claim ipv4")
	}
	if !v6only.HasAddressFamily("ipv6") {
		t.Error("v6-only host must claim ipv6")
	}
}

func TestIPv4OnlyHostNoFalseIPv6(t *testing.T) {
	// Host has only IPv4: [::] bind + v6 UFW rule must NOT create fake v6.
	v4only := Collect(context.Background(), &mockRunner{
		addr:  `[{"ifname":"eth0","addr_info":[{"family":"inet","local":"203.0.113.10","prefixlen":24,"scope":"global"}]}]`,
		route: `[{"dst":"default","dev":"eth0","family":"inet"}]`,
	})
	if v4only.HasGlobalIPv6 {
		t.Error("v4-only host must not have global IPv6")
	}
	if v4only.HasDefaultIPv6Route {
		t.Error("v4-only host must not have a default IPv6 route")
	}
}

type ipErr struct{ s string }

func (e ipErr) Error() string { return e.s }

var errIP = ipErr{s: "ip not found"}
