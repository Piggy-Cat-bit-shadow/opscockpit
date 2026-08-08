// Package firewall inspects the host firewall (UFW first) to determine real
// network exposure. A wildcard bind address only means a socket accepts
// connections on any interface; whether that port is reachable from the
// internet depends on firewall policy. This collector supplies that evidence.
//
// Everything is read through a Runner interface so tests feed fixtures and CI
// never needs a real UFW. If UFW is inactive, missing, or unparseable, the
// collector returns VisibilityUnknown and does not crash.
package firewall

import (
	"bufio"
	"context"
	"fmt"
	"regexp"
	"strings"
)

// Visibility describes how confident we are about the firewall policy.
type Visibility string

const (
	// VisibilityActive means UFW is active and rules were parsed.
	VisibilityActive Visibility = "active"
	// VisibilityInactive means UFW exists but is inactive.
	VisibilityInactive Visibility = "inactive"
	// VisibilityUnknown means UFW is missing, unparseable, or the runner failed.
	VisibilityUnknown Visibility = "unknown"
)

// Action is the rule action.
type Action string

const (
	ActionAllow Action = "allow"
	ActionDeny  Action = "deny"
	ActionReject Action = "reject"
	ActionLimit  Action = "limit"
)

// Direction is the traffic direction.
type Direction string

const (
	DirectionIn  Direction = "in"
	DirectionOut Direction = "out"
)

// Rule is one normalized firewall rule relevant to ingress exposure.
type Rule struct {
	Protocol   string    `json:"protocol"`              // tcp | udp ("" = any)
	PortStart  int       `json:"port_start"`            // 0 = not port-scoped (e.g. allow on interface)
	PortEnd    int       `json:"port_end"`              // inclusive
	Action     Action    `json:"action"`
	Direction  Direction `json:"direction"`
	IPVersion  int       `json:"ip_version"`            // 4 | 6 ("" = both/any)
	Interface  string    `json:"interface,omitempty"`   // e.g. eth0, docker0 (empty = any)
	Comment    string    `json:"comment,omitempty"`     // informational only, never used in decisions
	From       string    `json:"from,omitempty"`        // source address or "any"
	To         string    `json:"to,omitempty"`
}

// Status is the normalized firewall snapshot.
type Status struct {
	Visibility Visibility `json:"visibility"`
	Enabled    bool       `json:"enabled"`
	DefaultIn  Action     `json:"default_in,omitempty"` // UFW default incoming policy (deny/reject/allow)
	Rules      []Rule     `json:"rules,omitempty"`      // ingress rules only
}

// DefaultDenyIn reports whether the default incoming policy blocks traffic.
func (s Status) DefaultDenyIn() bool {
	return s.DefaultIn == ActionDeny || s.DefaultIn == ActionReject
}

// AllowedIn reports whether a (protocol, port) pair is explicitly allowed for
// inbound traffic. An explicit allow always wins over the default policy.
func (s Status) AllowedIn(protocol string, port int) bool {
	for _, r := range s.Rules {
		if r.Direction != DirectionIn {
			continue
		}
		if r.Action != ActionAllow && r.Action != ActionLimit {
			continue
		}
		if !protocolMatches(r.Protocol, protocol) {
			continue
		}
		if portInRange(r.PortStart, r.PortEnd, port) {
			return true
		}
	}
	return false
}

func protocolMatches(ruleProto, want string) bool {
	if ruleProto == "" {
		return true
	}
	return ruleProto == want
}

func portInRange(start, end, port int) bool {
	if start <= 0 {
		return false // not a port-scoped rule
	}
	if end < start {
		end = start
	}
	return port >= start && port <= end
}

// Runner executes the firewall query command. The real implementation runs
// `LC_ALL=C ufw status`; tests substitute a mock.
type Runner interface {
	// UFWStatus returns raw `LC_ALL=C ufw status` output.
	UFWStatus(ctx context.Context) (string, error)
}

// Collect reads UFW status and normalizes it. Any failure (binary missing,
// inactive, unparseable) degrades to a safe Unknown status, never an error the
// caller must handle.
func Collect(ctx context.Context, r Runner) Status {
	if r == nil {
		return Status{Visibility: VisibilityUnknown}
	}
	out, err := r.UFWStatus(ctx)
	if err != nil {
		return Status{Visibility: VisibilityUnknown}
	}
	return Parse(out)
}

// Parse parses `LC_ALL=C ufw status verbose` output.
func Parse(out string) Status {
	st := Status{Visibility: VisibilityUnknown}
	sc := bufio.NewScanner(strings.NewReader(out))
	haveHeader := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "Status:"):
			haveHeader = true
			// Extract the value after "Status:" and match exactly (a bare
			// substring match would confuse "inactive" with "active").
			val := strings.TrimSpace(line[len("Status:"):])
			st.Enabled = val == "active"
			if st.Enabled {
				st.Visibility = VisibilityActive
			} else {
				st.Visibility = VisibilityInactive
			}
		case strings.HasPrefix(line, "Default:"):
			// Default: deny (incoming), allow (outgoing), disabled (routed)
			if i := strings.Index(line, "(incoming)"); i >= 0 {
				pol := strings.TrimSpace(line[len("Default:"):i])
				switch strings.ToLower(pol) {
				case "allow":
					st.DefaultIn = ActionAllow
				case "reject":
					st.DefaultIn = ActionReject
				default:
					st.DefaultIn = ActionDeny
				}
			}
		case st.Enabled:
			// Rule lines only matter when UFW is active.
			if r, ok := parseRuleLine(line); ok {
				// Keep only ingress rules (explicit "out" lines are outbound).
				if r.Direction == DirectionIn {
					st.Rules = append(st.Rules, r)
				}
			}
		}
	}
	if !haveHeader {
		// UFW status output without a Status line is unusable.
		st = Status{Visibility: VisibilityUnknown}
	}
	return st
}

var (
	ruleRe    = regexp.MustCompile(`^\s*(?P<num>\d+)?\s*(?P<action>ALLOW|DENY|REJECT|LIMIT)\s+(?P<spec>[^\s].*)$`)
	rangeRe   = regexp.MustCompile(`^(\d+):(\d+)/(\w+)$`)
	singleRe  = regexp.MustCompile(`^(\d+)/(\w+)$`)
	onIfaceRe = regexp.MustCompile(`(?:^|\s)on\s+(\S+)`)
	stripOnRe = regexp.MustCompile(`^on\s+\S+\s*`)
)

// parseRuleLine parses one UFW "To Action From" line, e.g.:
//
//	22/tcp                     ALLOW IN     Anywhere
//	20000:20099/udp            ALLOW IN     203.0.113.10
//	443/tcp (v6)               ALLOW IN     Anywhere (v6)
//	Anywhere                   ALLOW IN     Anywhere
func parseRuleLine(line string) (Rule, bool) {
	// The "To" spec is the first token(s), "ALLOW IN" is the action+dir.
	// We locate the action token (ALLOW|DENY|REJECT|LIMIT) and split around it.
	upper := strings.ToUpper(line)
	idx := -1
	for _, act := range []string{"ALLOW", "DENY", "REJECT", "LIMIT"} {
		if i := strings.Index(upper, act); i >= 0 {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Rule{}, false
	}

	toPart := strings.TrimSpace(line[:idx])
	actionStr := strings.TrimSpace(line[idx:])
	fields := strings.Fields(actionStr)
	action := Action(strings.ToLower(fields[0]))
	dir := DirectionIn
	if len(fields) > 1 {
		d := strings.ToUpper(fields[1])
		if d == "OUT" || d == "IN" {
			if d == "OUT" {
				dir = DirectionOut
			}
		}
	}

	// From address: the last token before any comment.
	comment := ""
	from := "any"
	rest := actionStr
	if i := strings.Index(actionStr, "#"); i >= 0 {
		comment = strings.TrimSpace(actionStr[i+1:])
		rest = strings.TrimSpace(actionStr[:i])
	}
	restFields := strings.Fields(rest)
	if len(restFields) > 0 {
		// The from column is the last non-(v6) token.
		for j := len(restFields) - 1; j >= 0; j-- {
			f := restFields[j]
			if f == "IN" || f == "OUT" {
				continue
			}
			if strings.HasSuffix(f, ")") && strings.Contains(f, "v6") {
				continue
			}
			if strings.HasPrefix(f, "Anywhere") || strings.HasPrefix(f, "Anywhere") {
				from = "any"
				break
			}
			from = f
			break
		}
	}

	r := Rule{
		Action:    action,
		Direction: dir,
		Comment:   comment,
		From:      from,
		IPVersion: 4,
	}

	// IP version from the "(v6)" marker.
	if strings.Contains(line, "(v6)") {
		r.IPVersion = 6
	}

	// Parse the To spec: port/proto, range/proto, or bare interface/address.
	toSpec := toPart
	if i := strings.Index(toSpec, "("); i >= 0 {
		toSpec = strings.TrimSpace(toSpec[:i])
	}
	toSpec = strings.TrimSpace(toSpec)
	if toSpec == "" || strings.EqualFold(toSpec, "anywhere") {
		return r, true // no port scope
	}
	// Interface form like "on eth0" or "Anywhere on eth0".
	if m := onIfaceRe.FindStringSubmatch(toPart); m != nil {
		r.Interface = m[1]
	}
	// Strip a leading "on <iface>" prefix if present.
	toSpec = stripOnRe.ReplaceAllString(toSpec, "")

	// Port range: 20000:20099/udp
	if m := rangeRe.FindStringSubmatch(toSpec); m != nil {
		fmt.Sscanf(m[1], "%d", &r.PortStart)
		fmt.Sscanf(m[2], "%d", &r.PortEnd)
		r.Protocol = strings.ToLower(m[3])
		return r, true
	}
	// Single port: 443/tcp
	if m := singleRe.FindStringSubmatch(toSpec); m != nil {
		fmt.Sscanf(m[1], "%d", &r.PortStart)
		r.PortEnd = r.PortStart
		r.Protocol = strings.ToLower(m[2])
		return r, true
	}
	// Protocol without port (e.g. "/udp" isn't typical UFW; ignore).
	return r, true
}
