package firewall

import (
	"context"
	"testing"
)

// mockRunner returns canned ufw output.
type mockRunner struct {
	out string
	err error
}

func (m *mockRunner) UFWStatus(ctx context.Context) (string, error) { return m.out, m.err }

const activeStatus = `Status: active
Logging: on (low)
Default: deny (incoming), allow (outgoing), disabled (routed)
New profiles: skip

To                         Action      From
--                         ------      ----
22/tcp                     ALLOW IN    Anywhere
443/tcp                    ALLOW IN    Anywhere
443/udp                    ALLOW IN    Anywhere
853/tcp                    ALLOW IN    Anywhere
20000:20099/udp            ALLOW IN    203.0.113.10
8554/udp                   ALLOW IN    Anywhere
20100:20199/udp            ALLOW IN    Anywhere
22/tcp (v6)                ALLOW IN    Anywhere (v6)
443/tcp (v6)               ALLOW IN    Anywhere (v6)
443/udp (v6)               ALLOW IN    Anywhere (v6)
853/tcp (v6)               ALLOW IN    Anywhere (v6)
20000:20099/udp (v6)       ALLOW IN    2001:db8::1 (v6)
`

func TestParseActiveSinglePort(t *testing.T) {
	st := Parse(activeStatus)
	if st.Visibility != VisibilityActive {
		t.Fatalf("visibility = %q, want active", st.Visibility)
	}
	if !st.Enabled {
		t.Error("should be enabled")
	}
	if st.DefaultIn != ActionDeny {
		t.Errorf("default_in = %q, want deny", st.DefaultIn)
	}
	if !st.AllowedIn("tcp", 443) {
		t.Error("tcp/443 should be allowed")
	}
	if !st.AllowedIn("udp", 443) {
		t.Error("udp/443 should be allowed")
	}
	if st.AllowedIn("tcp", 18453) {
		t.Error("tcp/18453 must NOT be allowed (default deny, no rule)")
	}
	if st.AllowedIn("udp", 8443) {
		t.Error("udp/8443 must NOT be allowed")
	}
}

func TestParseActiveRange(t *testing.T) {
	st := Parse(activeStatus)
	if !st.AllowedIn("udp", 20000) || !st.AllowedIn("udp", 20099) || !st.AllowedIn("udp", 20050) {
		t.Error("udp range 20000:20099 should allow ports inside the range")
	}
	// Outside the range (and not covered by another rule) must be denied.
	if st.AllowedIn("udp", 30000) {
		t.Error("udp/30000 must NOT be allowed (outside 20000-20099)")
	}
	if st.AllowedIn("tcp", 20050) {
		t.Error("tcp within a udp-only range must NOT be allowed")
	}
	// Confirm the parsed rule carries a range, not a single port.
	found := false
	for _, r := range st.Rules {
		if r.Protocol == "udp" && r.PortStart == 20000 && r.PortEnd == 20099 {
			found = true
		}
	}
	if !found {
		t.Error("range rule 20000:20099/udp not parsed as a range")
	}
}

func TestParseIPv6(t *testing.T) {
	st := Parse(activeStatus)
	// IPv6 rules must be present and versioned.
	hasV6 := false
	for _, r := range st.Rules {
		if r.IPVersion == 6 {
			hasV6 = true
		}
	}
	if !hasV6 {
		t.Error("expected IPv6 rules")
	}
	// Range rule with v6 marker.
	for _, r := range st.Rules {
		if r.PortStart == 20000 && r.IPVersion == 6 {
			return
		}
	}
	t.Error("expected an IPv6 range rule")
}

func TestParseInactive(t *testing.T) {
	st := Parse(`Status: inactive
`)
	if st.Visibility != VisibilityInactive {
		t.Fatalf("visibility = %q, want inactive", st.Visibility)
	}
	if st.Enabled {
		t.Error("inactive should not be enabled")
	}
	if st.AllowedIn("tcp", 443) {
		t.Error("inactive firewall must not claim allow")
	}
}

func TestCollectUnavailable(t *testing.T) {
	st := Collect(context.Background(), &mockRunner{out: "", err: errUFWMissing})
	if st.Visibility != VisibilityUnknown {
		t.Fatalf("visibility = %q, want unknown", st.Visibility)
	}
	if st.AllowedIn("tcp", 443) {
		t.Error("unknown firewall must not claim allow")
	}
}

type ufwErr struct{ s string }

func (e ufwErr) Error() string { return e.s }

var errUFWMissing = ufwErr{s: "executable file not found"}

func TestCollectNilRunner(t *testing.T) {
	st := Collect(context.Background(), nil)
	if st.Visibility != VisibilityUnknown {
		t.Fatalf("visibility = %q", st.Visibility)
	}
}

func TestParseGarbage(t *testing.T) {
	// Completely unparseable output must not crash; visibility unknown.
	st := Parse("no status header here\njust noise\n")
	if st.Visibility != VisibilityUnknown {
		t.Fatalf("visibility = %q, want unknown", st.Visibility)
	}
}

func TestParseDenyRule(t *testing.T) {
	st := Parse(`Status: active

To                         Action      From
--                         ------      ----
443/tcp                    DENY IN     Anywhere
`)
	// A deny rule must not count as allowed even though there's a rule.
	if st.AllowedIn("tcp", 443) {
		t.Error("DENY IN must not be treated as allow")
	}
}

func TestRuleParsingDetails(t *testing.T) {
	cases := []struct {
		line       string
		proto      string
		start, end int
		dir        Direction
		ipv        int
	}{
		{"443/tcp                     ALLOW IN    Anywhere", "tcp", 443, 443, DirectionIn, 4},
		{"20000:20099/udp            ALLOW IN    203.0.113.10", "udp", 20000, 20099, DirectionIn, 4},
		{"22/tcp (v6)                ALLOW IN    Anywhere (v6)", "tcp", 22, 22, DirectionIn, 6},
	}
	for _, c := range cases {
		r, ok := parseRuleLine(c.line)
		if !ok {
			t.Fatalf("parseRuleLine(%q) failed", c.line)
		}
		if r.Protocol != c.proto || r.PortStart != c.start || r.PortEnd != c.end || r.Direction != c.dir || r.IPVersion != c.ipv {
			t.Errorf("parseRuleLine(%q) = %+v", c.line, r)
		}
	}
}

func TestOutboundIgnored(t *testing.T) {
	st := Parse(`Status: active

To                         Action      From
--                         ------      ----
443/tcp                    ALLOW OUT   Anywhere
`)
	if st.AllowedIn("tcp", 443) {
		t.Error("ALLOW OUT must not count as ingress")
	}
	if len(st.Rules) != 0 {
		t.Errorf("outbound rule should be filtered: %+v", st.Rules)
	}
}

func TestDefaultDenyIn(t *testing.T) {
	st := Parse(`Status: active
Default: deny (incoming), allow (outgoing), disabled (routed)
`)
	if !st.DefaultDenyIn() {
		t.Error("default deny should report DefaultDenyIn true")
	}
	st2 := Parse(`Status: active
Default: allow (incoming), allow (outgoing), disabled (routed)
`)
	if st2.DefaultDenyIn() {
		t.Error("default allow should report DefaultDenyIn false")
	}
}

func TestLimitCountsAsAllow(t *testing.T) {
	st := Parse(`Status: active

To                         Action      From
--                         ------      ----
443/tcp                    LIMIT IN    Anywhere
`)
	if !st.AllowedIn("tcp", 443) {
		t.Error("LIMIT IN should count as allow")
	}
}
