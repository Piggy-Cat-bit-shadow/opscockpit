// Package nat inspects iptables nat-table rules to find public REDIRECT
// ingresses — cases where the internet-facing port has no local listener
// because traffic is redirected to a backend port.
//
// Input: `iptables -t nat -S` (or `iptables-save -t nat`), obtained through a
// Runner abstraction so tests never need real iptables. No shell string is ever
// constructed; the runner executes argv directly.
//
// The parser deliberately distinguishes true PREROUTING public ingress from
// Docker's 127.0.0.1 loopback DNAT: a rule bound to 127.0.0.1 is an internal
// mapping, never a public entry point.
package nat

import (
	"bufio"
	"context"
	"fmt"
	"regexp"
	"strings"
)

// IngressType describes the NAT kind.
type IngressType string

const (
	// TypeRedirect is a REDIRECT rule (public ingress → backend port).
	TypeRedirect IngressType = "redirect"
	// TypeDNAT is a DNAT rule (public ingress → external target). Parsed and
	// recorded but not treated as an OpsCockpit service ingress unless it is
	// public; loopback DNAT is classified internal.
	TypeDNAT IngressType = "dnat"
)

// Ingress is one normalized public NAT ingress.
type Ingress struct {
	Protocol        string      `json:"protocol"` // tcp | udp
	SourcePortStart int         `json:"source_port_start"`
	SourcePortEnd   int         `json:"source_port_end"` // inclusive
	TargetPort      int         `json:"target_port"`
	Type            IngressType `json:"type"`
	Public          bool        `json:"public"`          // false = loopback/internal mapping
	TargetAddress   string      `json:"target_address,omitempty"` // for DNAT (e.g. container IP)
}

// Status is the normalized NAT snapshot.
type Status struct {
	Ingresses []Ingress `json:"ingresses,omitempty"`
	// Visible is false when the nat table could not be read at all.
	Visible bool `json:"visible"`
}

// PublicRedirects returns only public REDIRECT ingresses.
func (s Status) PublicRedirects() []Ingress {
	var out []Ingress
	for _, ing := range s.Ingresses {
		if ing.Public && ing.Type == TypeRedirect {
			out = append(out, ing)
		}
	}
	return out
}

// Runner executes the nat-table query. Tests substitute a mock.
type Runner interface {
	// IptablesNat returns `iptables -t nat -S` output.
	IptablesNat(ctx context.Context) (string, error)
}

// Collect reads the nat table. Any failure degrades to Visible=false, never an
// error the caller must handle.
func Collect(ctx context.Context, r Runner) Status {
	if r == nil {
		return Status{Visible: false}
	}
	out, err := r.IptablesNat(ctx)
	if err != nil {
		return Status{Visible: false}
	}
	st := Parse(out)
	st.Visible = true
	return st
}

var (
	// -A PREROUTING -d 203.0.113.10/32 -p udp --dport 20000:20099 -j REDIRECT --to-ports 443
	// -A PREROUTING -d 203.0.113.10/32 -p udp --dport 8554 -j REDIRECT --to-ports 17414
	// -A PREROUTING -i docker0 -p tcp --dport 3001 -j DNAT --to-destination 172.17.0.2:3001
	// -A PREROUTING -i lo -p tcp --dport 8080 -j REDIRECT --to-ports 9090
	chainRe   = regexp.MustCompile(`^-A\s+(PREROUTING|OUTPUT|DOCKER)\s+`)
	protoRe   = regexp.MustCompile(`-p\s+(\w+)`)
	dportRe   = regexp.MustCompile(`--dport\s+(\d+)(?::(\d+))?`)
	dstRe     = regexp.MustCompile(`-d\s+(\S+)`)
	ifaceRe   = regexp.MustCompile(`-i\s+(\S+)`)
	jumpRe    = regexp.MustCompile(`-j\s+(\w+)`)
	toPortsRe = regexp.MustCompile(`--to-ports\s+(\d+)`)
	toDstRe   = regexp.MustCompile(`--to-destination\s+(\S+)`)
	loRe      = regexp.MustCompile(`^127\.|^::1$|^localhost$`)
)

// Parse parses `iptables -t nat -S` output.
func Parse(out string) Status {
	st := Status{Visible: true}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		// Only consider rules (skip -P policy lines and comments).
		if !strings.HasPrefix(line, "-A ") {
			continue
		}
		if !chainRe.MatchString(line) {
			continue
		}
		ing, ok := parseRule(line)
		if ok {
			st.Ingresses = append(st.Ingresses, ing)
		}
	}
	return st
}

func parseRule(line string) (Ingress, bool) {
	mj := jumpRe.FindStringSubmatch(line)
	if mj == nil {
		return Ingress{}, false
	}
	jump := strings.ToUpper(mj[1])
	if jump != "REDIRECT" && jump != "DNAT" {
		return Ingress{}, false
	}

	ing := Ingress{Protocol: "tcp"}
	if mp := protoRe.FindStringSubmatch(line); mp != nil {
		ing.Protocol = strings.ToLower(mp[1])
	}

	if md := dportRe.FindStringSubmatch(line); md != nil {
		fmt.Sscanf(md[1], "%d", &ing.SourcePortStart)
		ing.SourcePortEnd = ing.SourcePortStart
		if md[2] != "" {
			fmt.Sscanf(md[2], "%d", &ing.SourcePortEnd)
		}
	} else {
		return Ingress{}, false
	}

	// Determine publicness from the destination/interface, not from dport.
	ing.Public = isPublicIngress(line)
	ing.Type = TypeDNAT
	if jump == "REDIRECT" {
		ing.Type = TypeRedirect
		if mt := toPortsRe.FindStringSubmatch(line); mt != nil {
			fmt.Sscanf(mt[1], "%d", &ing.TargetPort)
		}
	}
	if jump == "DNAT" {
		if mt := toDstRe.FindStringSubmatch(line); mt != nil {
			ing.TargetAddress = mt[1]
			// A DNAT to 127.0.0.1 is a loopback mapping (e.g. Docker), not a
			// public ingress.
			host := mt[1]
			if i := strings.LastIndex(host, ":"); i >= 0 {
				host = host[:i]
			}
			if loRe.MatchString(strings.Trim(host, "[]")) {
				ing.Public = false
			}
		}
	}

	return ing, true
}

// isPublicIngress classifies a PREROUTING rule as public ingress vs internal.
// A rule bound to loopback (127.0.0.1, ::1, lo interface) is internal. Docker
// bridge rules are internal. Only rules targeting the host's public address
// (any, or a non-loopback destination) are public ingress.
func isPublicIngress(line string) bool {
	// Interface -i lo means loopback only.
	if mi := ifaceRe.FindStringSubmatch(line); mi != nil {
		if strings.EqualFold(mi[1], "lo") {
			return false
		}
		// docker0/docker bridge interfaces are internal mapping, not public.
		if strings.Contains(strings.ToLower(mi[1]), "docker") {
			return false
		}
	}
	// Destination address present.
	if md := dstRe.FindStringSubmatch(line); md != nil {
		host := md[1]
		if i := strings.Index(host, "/"); i >= 0 {
			host = host[:i]
		}
		host = strings.Trim(host, "[]")
		if loRe.MatchString(host) {
			return false
		}
		// Private / documentation ranges are not public internet ingress.
		if isPrivateAddr(host) {
			return false
		}
		return true
	}
	// No explicit destination (matches any): public ingress.
	return true
}

func isPrivateAddr(host string) bool {
	// 10/8, 172.16/12, 192.168/16, 100.64/10, 198.18/15, 169.254/16, fc00::/7
	first, second := 0, 0
	parts := strings.Split(host, ".")
	if len(parts) == 4 {
		fmt.Sscanf(parts[0], "%d", &first)
		fmt.Sscanf(parts[1], "%d", &second)
	}
	switch {
	case first == 10:
		return true
	case first == 172 && second >= 16 && second <= 31:
		return true
	case first == 192 && second == 168:
		return true
	case first == 100 && second >= 64 && second <= 127:
		return true
	case first == 169 && second == 254:
		return true
	case first == 198 && second >= 18 && second <= 19:
		return true
	}
	// IPv6 unique-local / link-local.
	if strings.Contains(host, ":") {
		if strings.HasPrefix(host, "fc") || strings.HasPrefix(host, "fd") || strings.HasPrefix(host, "fe8") {
			return true
		}
	}
	return false
}
