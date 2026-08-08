package nat

import (
	"context"
	"testing"
)

const natFixture = `-P PREROUTING ACCEPT
-P INPUT ACCEPT
-P OUTPUT ACCEPT
-P POSTROUTING ACCEPT
-A PREROUTING -d 203.0.113.10/32 -p udp --dport 20000:20099 -j REDIRECT --to-ports 443
-A PREROUTING -d 203.0.113.10/32 -p udp --dport 20100:20199 -j REDIRECT --to-ports 8443
-A PREROUTING -d 203.0.113.10/32 -p udp --dport 8554 -j REDIRECT --to-ports 17414
-A PREROUTING -i lo -p tcp --dport 8080 -j REDIRECT --to-ports 9090
-A PREROUTING -i docker0 -p tcp --dport 3001 -j DNAT --to-destination 172.17.0.2:3001
-A PREROUTING -i lo -p tcp --dport 12701 -j DNAT --to-destination 127.0.0.1:3001
-A PREROUTING -d 192.168.1.5/32 -p tcp --dport 7000 -j REDIRECT --to-ports 7001
`

func TestParseRedirectRange(t *testing.T) {
	st := Parse(natFixture)
	if !st.Visible {
		t.Fatal("should be visible")
	}
	redirs := st.PublicRedirects()
	// 20000:20099, 20100:20199, 8554 are public. lo/private are excluded.
	if len(redirs) != 3 {
		t.Fatalf("public redirects = %d, want 3: %+v", len(redirs), redirs)
	}

	found := false
	for _, ing := range redirs {
		if ing.Protocol == "udp" && ing.SourcePortStart == 20000 && ing.SourcePortEnd == 20099 && ing.TargetPort == 443 {
			found = true
			if ing.Type != TypeRedirect {
				t.Errorf("type = %q", ing.Type)
			}
			if !ing.Public {
				t.Error("should be public")
			}
		}
	}
	if !found {
		t.Error("range REDIRECT 20000:20099 → 443 not parsed")
	}

	// Single-port 8554 → 17414.
	single := false
	for _, ing := range redirs {
		if ing.Protocol == "udp" && ing.SourcePortStart == 8554 && ing.SourcePortEnd == 8554 && ing.TargetPort == 17414 {
			single = true
		}
	}
	if !single {
		t.Error("single REDIRECT 8554 → 17414 not parsed")
	}
}

func TestLoopbackRedirectIgnored(t *testing.T) {
	st := Parse(natFixture)
	for _, ing := range st.Ingresses {
		if ing.SourcePortStart == 8080 && ing.Public {
			t.Error("loopback -i lo redirect must not be public")
		}
	}
}

func TestDockerLoopbackDNATIgnored(t *testing.T) {
	st := Parse(natFixture)
	// docker0 3001 DNAT is internal mapping.
	for _, ing := range st.Ingresses {
		if ing.SourcePortStart == 3001 && ing.Public {
			t.Error("docker0 DNAT must not be public ingress")
		}
		if ing.SourcePortStart == 12701 && ing.Public {
			t.Error("127.0.0.1 DNAT must not be public ingress")
		}
	}
}

func TestPrivateDestRedirectIgnored(t *testing.T) {
	st := Parse(natFixture)
	for _, ing := range st.Ingresses {
		if ing.SourcePortStart == 7000 && ing.Public {
			t.Error("private destination redirect must not be public ingress")
		}
	}
}

func TestCollectUnavailable(t *testing.T) {
	st := Collect(context.Background(), &mockRunner{err: errIPT})
	if st.Visible {
		t.Fatal("unavailable nat should be invisible")
	}
}

func TestCollectNilRunner(t *testing.T) {
	if st := Collect(context.Background(), nil); st.Visible {
		t.Fatal("nil runner should be invisible")
	}
}

func TestParseEmpty(t *testing.T) {
	st := Parse("")
	if !st.Visible {
		t.Fatal("empty output with Visible set from Collect should be visible")
	}
	if len(st.PublicRedirects()) != 0 {
		t.Fatal("no rules → no redirects")
	}
}

func TestParseMalformed(t *testing.T) {
	// Garbage lines must not crash or produce ingress.
	st := Parse("garbage\n-A PREROUTING -j ACCEPT\n")
	if len(st.Ingresses) != 0 {
		t.Fatalf("expected no ingress from malformed rules, got %+v", st.Ingresses)
	}
}

type mockRunner struct {
	out string
	err error
}

func (m *mockRunner) IptablesNat(ctx context.Context) (string, error) { return m.out, m.err }

type iptErr struct{ s string }

func (e iptErr) Error() string { return e.s }

var errIPT = iptErr{s: "iptables not found"}
