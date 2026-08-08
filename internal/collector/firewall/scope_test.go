package firewall

import (
	"context"
	"testing"
)

const scopedStatus = `Status: active
Logging: on (low)
Default: deny (incoming), allow (outgoing), disabled (routed)
New profiles: skip

To                         Action      From
--                         ------      ----
443/tcp                    ALLOW IN    Anywhere
8443/udp                   ALLOW IN    203.0.113.10
853/tcp                    ALLOW IN    10.0.0.0/8
22/tcp                     ALLOW IN    192.168.1.0/24
80/tcp                     ALLOW IN    127.0.0.1
443/tcp (v6)               ALLOW IN    Anywhere (v6)
8554/udp                   ALLOW IN    2001:db8::/32 (v6)
`

func TestScopePublicAnywhere(t *testing.T) {
	st := Parse(scopedStatus)
	if r := st.AllowedInScoped("tcp", 443, ScopePublic); r == nil {
		t.Fatal("443/tcp from Anywhere should be public")
	}
	if !st.IsPubliclyAllowed("tcp", 443) {
		t.Error("443/tcp must be publicly allowed")
	}
}

func TestScopeRestrictedPublicCIDR(t *testing.T) {
	st := Parse(scopedStatus)
	// 203.0.113.10 is a documentation (public) address → restricted, not public.
	if r := st.AllowedInScoped("udp", 8443, ScopePublic); r != nil {
		t.Error("8443/udp from a specific public CIDR must NOT match public scope")
	}
	if !st.IsPubliclyAllowed("udp", 8443) {
		t.Error("8443/udp from a public CIDR IS publicly allowed (restricted)")
	}
	// Allowed with a lower bar.
	if r := st.AllowedInScoped("udp", 8443, ScopeRestricted); r == nil {
		t.Error("8443/udp should match restricted scope")
	}
}

func TestScopePrivateInternal(t *testing.T) {
	st := Parse(scopedStatus)
	// 10.0.0.0/8 (RFC1918) and 192.168.1.0/24 → internal, never public.
	if r := st.AllowedInScoped("tcp", 853, ScopeRestricted); r != nil {
		t.Error("853/tcp from RFC1918 must not be public/restricted")
	}
	if st.IsPubliclyAllowed("tcp", 853) {
		t.Error("853/tcp from RFC1918 must not be publicly allowed")
	}
	if st.IsPubliclyAllowed("tcp", 22) {
		t.Error("22/tcp from 192.168.x must not be publicly allowed")
	}
	// Loopback source.
	if st.IsPubliclyAllowed("tcp", 80) {
		t.Error("80/tcp from 127.0.0.1 must not be publicly allowed")
	}
}

func TestScopeIPv6(t *testing.T) {
	st := Parse(scopedStatus)
	if !st.IsPubliclyAllowed("tcp", 443) {
		t.Error("443/tcp v6 from Anywhere (v6) is public")
	}
	// 2001:db8::/32 is documentation IPv6 — restricted, not public.
	if r := st.AllowedInScoped("udp", 8554, ScopePublic); r != nil {
		t.Error("8554/udp from 2001:db8::/32 must not be public scope")
	}
	if r := st.AllowedInScoped("udp", 8554, ScopeRestricted); r == nil {
		t.Error("8554/udp from a public v6 CIDR is restricted")
	}
}

func TestClassifyFromScope(t *testing.T) {
	cases := []struct {
		from string
		v6   int
		want Scope
	}{
		{"Anywhere", 4, ScopePublic},
		{"Anywhere", 6, ScopePublic},
		{"Anywhere (v6)", 6, ScopePublic},
		{"203.0.113.10", 4, ScopeRestricted},
		{"203.0.113.0/24", 4, ScopeRestricted},
		{"2001:db8::/32", 6, ScopeRestricted},
		{"10.0.0.0/8", 4, ScopeInternal},
		{"172.16.0.0/12", 4, ScopeInternal},
		{"192.168.1.0/24", 4, ScopeInternal},
		{"100.64.0.0/10", 4, ScopeInternal},
		{"127.0.0.1", 4, ScopeInternal},
		{"::1", 6, ScopeInternal},
		{"", 4, ScopeUnknown},
		{"garbage!!", 4, ScopeUnknown},
	}
	for _, c := range cases {
		if got := classifyFromScope(c.from, c.v6); got != c.want {
			t.Errorf("classifyFromScope(%q,%d) = %q, want %q", c.from, c.v6, got, c.want)
		}
	}
}

func TestCollectStillDegrades(t *testing.T) {
	st := Collect(context.Background(), &mockRunner{out: "", err: errUFWMissing})
	if st.Visibility != VisibilityUnknown {
		t.Fatalf("visibility = %q", st.Visibility)
	}
}
