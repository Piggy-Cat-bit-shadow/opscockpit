// Package network reads the host's own network identity via `ip -j addr show`
// and `ip -j route show` — which network addresses this machine actually has,
// which are global, and which address families have a usable default route.
//
// Exposure classification needs this: a firewall rule or NAT rule may mention
// an address that is not actually owned by this host, and an IPv6 bind with an
// IPv6 UFW rule is not a real public service unless the host actually has a
// global IPv6 address + route.
//
// Input is read through a Runner abstraction so tests feed fixtures and never
// need `ip`. No netlink dependency.
package network

import (
	"context"
	"encoding/json"
	"strings"
)

// Addr is one normalized address on one interface.
type Addr struct {
	Interface string `json:"interface"`
	Address   string `json:"address"` // without prefix length
	PrefixLen int    `json:"prefix_len"`
	Family    string `json:"family"` // ipv4 | ipv6
	Scope     string `json:"scope"`  // global | link | host
	IsLoopback bool  `json:"is_loopback"`
}

// Route is one normalized route entry.
type Route struct {
	Dest     string `json:"dest"`
	Family   string `json:"family"`   // ipv4 | ipv6
	IsDefault bool  `json:"is_default"`
	Dev      string `json:"dev,omitempty"`
}

// Identity is the host's network identity.
type Identity struct {
	Addrs []Addr  `json:"addrs,omitempty"`
	Routes []Route `json:"routes,omitempty"`

	// Derived facts used by exposure classification.
	HasGlobalIPv4 bool `json:"has_global_ipv4"`
	HasGlobalIPv6 bool `json:"has_global_ipv6"`
	HasDefaultIPv4Route bool `json:"has_default_ipv4_route"`
	HasDefaultIPv6Route bool `json:"has_default_ipv6_route"`
}

// HasAddressFamily reports whether the host owns any address in the family
// (ipv4|ipv6) that is not loopback.
func (id Identity) HasAddressFamily(family string) bool {
	for _, a := range id.Addrs {
		if a.Family == family && !a.IsLoopback && a.Scope != "host" {
			return true
		}
	}
	return false
}

// HasDefaultRoute reports whether the host has a default route for the family.
func (id Identity) HasDefaultRoute(family string) bool {
	for _, r := range id.Routes {
		if r.Family == family && r.IsDefault {
			return true
		}
	}
	return false
}

// OwnsAddress reports whether `addr` (host part, no prefix) is one of the
// host's non-loopback addresses.
func (id Identity) OwnsAddress(addr string) bool {
	a := strings.Trim(addr, "[]")
	for _, x := range id.Addrs {
		if x.Address == a && !x.IsLoopback {
			return true
		}
	}
	return false
}

// Runner executes the `ip` queries.
type Runner interface {
	// IPAddrJSON returns `ip -j addr show` output.
	IPAddrJSON(ctx context.Context) (string, error)
	// IPRouteJSON returns `ip -j route show` output.
	IPRouteJSON(ctx context.Context) (string, error)
}

// Collect reads the host network identity. Any failure degrades to an empty
// identity (no error) so exposure falls back to unknown.
func Collect(ctx context.Context, r Runner) Identity {
	id := Identity{}
	if r == nil {
		return id
	}
	if out, err := r.IPAddrJSON(ctx); err == nil {
		id.Addrs = parseAddrJSON(out)
	}
	if out, err := r.IPRouteJSON(ctx); err == nil {
		id.Routes = parseRouteJSON(out)
	}
	id.derive()
	return id
}

// rawAddr is a subset of `ip -j addr show` output.
type rawAddr struct {
	Ifname    string     `json:"ifname"`
	AddrInfo  []addrInfo `json:"addr_info"`
}

type addrInfo struct {
	Local     string `json:"local"`
	Prefixlen int    `json:"prefixlen"`
	Family    string `json:"family"`
	Scope     string `json:"scope"`
}

// rawRoute is a subset of `ip -j route show` output.
type rawRoute struct {
	Dst    string `json:"dst"`
	Dev    string `json:"dev"`
	Family string `json:"family"`
}

// parseAddrJSON parses `ip -j addr show`.
func parseAddrJSON(out string) []Addr {
	var raws []rawAddr
	if err := json.Unmarshal([]byte(out), &raws); err != nil {
		return nil
	}
	var addrs []Addr
	for _, ra := range raws {
		for _, ai := range ra.AddrInfo {
			fam := ai.Family
			if fam == "inet" {
				fam = "ipv4"
			} else if fam == "inet6" {
				fam = "ipv6"
			}
			addrs = append(addrs, Addr{
				Interface:  ra.Ifname,
				Address:    ai.Local,
				PrefixLen:  ai.Prefixlen,
				Family:     fam,
				Scope:      ai.Scope,
				IsLoopback: ai.Scope == "host" || ai.Local == "127.0.0.1" || ai.Local == "::1" || strings.HasPrefix(ai.Local, "127."),
			})
		}
	}
	return addrs
}

// parseRouteJSON parses `ip -j route show`.
func parseRouteJSON(out string) []Route {
	var raws []rawRoute
	if err := json.Unmarshal([]byte(out), &raws); err != nil {
		return nil
	}
	var routes []Route
	for _, rr := range raws {
		fam := rr.Family
		if fam == "" {
			// Family may be absent; infer from dst.
			if strings.Contains(rr.Dst, ":") {
				fam = "ipv6"
			} else {
				fam = "ipv4"
			}
		} else if fam == "inet" {
			fam = "ipv4"
		} else if fam == "inet6" {
			fam = "ipv6"
		}
		routes = append(routes, Route{
			Dest:      rr.Dst,
			Family:    fam,
			IsDefault: rr.Dst == "default" || rr.Dst == "::/0" || rr.Dst == "0.0.0.0/0" || strings.HasPrefix(rr.Dst, "default "),
			Dev:       rr.Dev,
		})
	}
	return routes
}

func (id *Identity) derive() {
	for _, a := range id.Addrs {
		switch {
		case a.Family == "ipv4" && !a.IsLoopback && a.Scope == "global":
			id.HasGlobalIPv4 = true
		case a.Family == "ipv6" && !a.IsLoopback && a.Scope == "global":
			id.HasGlobalIPv6 = true
		}
	}
	for _, r := range id.Routes {
		switch {
		case r.IsDefault && r.Family == "ipv4":
			id.HasDefaultIPv4Route = true
		case r.IsDefault && r.Family == "ipv6":
			id.HasDefaultIPv6Route = true
		}
	}
}
