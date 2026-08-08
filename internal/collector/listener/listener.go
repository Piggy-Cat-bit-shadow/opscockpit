// Package listener parses `ss -H -lntup` output to discover runtime listeners.
//
// The parser is pure: Parse takes the exact ss output text and returns typed
// sockets. It never shells out itself — the caller decides how to obtain the
// ss text (real exec at collect time, fixture in tests).
package listener

import (
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

// Socket is one parsed listening socket.
type Socket struct {
	Protocol string // tcp | udp
	Address  string // bind address as printed, e.g. 0.0.0.0, [::], 127.0.0.1
	Port     int
	PID      int
	Process  string
	// ServiceID is populated by the collector via PID → cgroup → unit mapping.
	ServiceID string
	// Internal is true for loopback-only binds (127.0.0.1, ::1, or 127.x).
	Internal bool
	// ProcessCount aggregates how many processes/sockets share this logical
	// listener (Nginx workers, reuseport, IPv4+IPv6 any-binds).
	ProcessCount int
}

// Normalize deduplicates sockets that describe the same logical listener:
// same (protocol, address, port, service_id). Real hosts produce near-duplicate
// rows (multiple Nginx workers sharing UDP/853, reuseport, IPv4+IPv6 any
// binds). Only the first is kept; ProcessCount records how many were merged.
// Deterministic: input order is preserved for the first of each group.
func Normalize(socks []Socket) []Socket {
	seen := map[string]int{} // key → index of the kept socket
	out := []Socket{}
	for _, s := range socks {
		key := s.Protocol + "|" + s.Address + "|" + itoa(s.Port) + "|" + s.ServiceID
		if i, ok := seen[key]; ok {
			out[i].ProcessCount++
			continue
		}
		seen[key] = len(out)
		if s.ProcessCount == 0 {
			s.ProcessCount = 1
		}
		out = append(out, s)
	}
	return out
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

// IsLoopback reports whether an address is loopback-only.
func IsLoopback(addr string) bool {
	a := strings.Trim(addr, "[]")
	if a == "127.0.0.1" || a == "::1" {
		return true
	}
	if ip, err := netip.ParseAddr(a); err == nil {
		return ip.IsLoopback()
	}
	return false
}

// IsPublic reports whether an address can be reached from the internet
// (any-address binds and non-loopback addresses).
func IsPublic(addr string) bool {
	a := strings.Trim(addr, "[]")
	if a == "*" || a == "0.0.0.0" || a == "::" || a == "0:0:0:0:0:0:0:0" {
		return true
	}
	if ip, err := netip.ParseAddr(a); err == nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			// unspecified handled above; private/loopback are not public
		}
		return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast()
	}
	return false
}

// Parse parses `ss -H -lntup` output. Each output line looks like:
//
//	tcp   LISTEN 0   511  0.0.0.0:443   0.0.0.0:*   users:(("nginx",pid=1234,fd=9))
//	udp   UNCONN 0   0    [::]:443      [::]:*      users:(("hysteria",pid=4321,fd=7))
//
// Parse tolerates fields the kernel may omit (e.g. the peer column is present
// on some ss versions and not others) by locating the address column from the
// right.
func Parse(output string) ([]Socket, error) {
	var sockets []Socket
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		s, err := parseLine(line)
		if err != nil {
			// A malformed line shouldn't abort the whole parse; skip and
			// report it so callers can notice.
			return sockets, fmt.Errorf("line %d: %w", i+1, err)
		}
		sockets = append(sockets, s)
	}
	return sockets, nil
}

func parseLine(line string) (Socket, error) {
	fields := strings.Fields(line)
	if len(fields) < 4 {
		return Socket{}, fmt.Errorf("too few fields: %q", line)
	}

	proto := strings.ToLower(fields[0])
	if proto != "tcp" && proto != "udp" && proto != "udplite" && proto != "sctp" {
		proto = "tcp"
	}

	// The bind address is the second-to-last field before the peer column
	// (which is the field right before `users:...` or the last column). We find
	// the local address as the field that contains ':port'.
	local := ""
	for _, f := range fields {
		if strings.Contains(f, ":") && !strings.HasPrefix(f, "users:") {
			local = f
			break
		}
	}
	if local == "" {
		return Socket{}, fmt.Errorf("no local address: %q", line)
	}

	addr, port, err := splitAddrPort(local)
	if err != nil {
		return Socket{}, fmt.Errorf("parse address %q: %w", local, err)
	}

	pid, proc := parseUsers(line)

	return Socket{
		Protocol: proto,
		Address:  addr,
		Port:     port,
		PID:      pid,
		Process:  proc,
		Internal: IsLoopback(addr),
	}, nil
}

// splitAddrPort splits "0.0.0.0:443", "[::]:443", "127.0.0.1:8443".
func splitAddrPort(s string) (addr string, port int, err error) {
	// IPv6 bracket form.
	if strings.HasPrefix(s, "[") {
		idx := strings.Index(s, "]")
		if idx < 0 {
			return "", 0, fmt.Errorf("unterminated ipv6: %q", s)
		}
		addr = s[1:idx]
		rest := s[idx+1:]
		if !strings.HasPrefix(rest, ":") {
			return "", 0, fmt.Errorf("missing port after ipv6: %q", s)
		}
		p, perr := strconv.Atoi(rest[1:])
		return addr, p, perr
	}
	idx := strings.LastIndex(s, ":")
	if idx <= 0 {
		return "", 0, fmt.Errorf("no port separator: %q", s)
	}
	addr = s[:idx]
	p, err := strconv.Atoi(s[idx+1:])
	return addr, p, err
}

// parseUsers extracts the first process name and pid from an ss `users:(...)`
// column. Format examples:
//
//	users:(("nginx",pid=1234,fd=10))
//	users:(("nginx",pid=1234,fd=10),"nginx",pid=1235,fd=11)
func parseUsers(line string) (pid int, proc string) {
	idx := strings.Index(line, "users:")
	if idx < 0 {
		return 0, ""
	}
	rest := line[idx+len("users:"):]

	// First entry name: everything between `(("` and the next `"`.
	if n := strings.Index(rest, `(("`); n >= 0 {
		after := rest[n+3:]
		if end := strings.Index(after, `"`); end >= 0 {
			proc = after[:end]
		}
	}

	// First pid: `pid=` followed by digits.
	pid = 0
	search := rest
	for {
		p := strings.Index(search, "pid=")
		if p < 0 {
			break
		}
		digits := search[p+4:]
		j := 0
		for j < len(digits) && digits[j] >= '0' && digits[j] <= '9' {
			j++
		}
		if j > 0 {
			if v, err := strconv.Atoi(digits[:j]); err == nil {
				pid = v
			}
			break
		}
		search = digits
	}
	return pid, proc
}

// SortByPort sorts sockets by port ascending, then protocol, then address.
// This keeps topology generation deterministic.
func SortByPort(sockets []Socket) {
	sort.SliceStable(sockets, func(i, j int) bool {
		if sockets[i].Port != sockets[j].Port {
			return sockets[i].Port < sockets[j].Port
		}
		if sockets[i].Protocol != sockets[j].Protocol {
			return sockets[i].Protocol < sockets[j].Protocol
		}
		return sockets[i].Address < sockets[j].Address
	})
}

// UniquePublicPorts returns the distinct public ports in ascending order.
func UniquePublicPorts(sockets []Socket) []int {
	seen := map[int]bool{}
	var ports []int
	for _, s := range sockets {
		if s.Internal {
			continue
		}
		if !seen[s.Port] {
			seen[s.Port] = true
			ports = append(ports, s.Port)
		}
	}
	sort.Ints(ports)
	return ports
}
