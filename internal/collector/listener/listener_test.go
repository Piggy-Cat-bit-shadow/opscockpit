package listener

import (
	"reflect"
	"testing"
)

const ssFixture = `tcp   LISTEN 0      511    0.0.0.0:443        0.0.0.0:*      users:(("nginx",pid=1001,fd=10))
udp   UNCONN 0      0      [::]:443          [::]:*         users:(("hysteria",pid=2002,fd=7))
udp   UNCONN 0      0      [::]:8443         [::]:*         users:(("tuic",pid=3003,fd=9))
tcp   LISTEN 0      128    0.0.0.0:853       0.0.0.0:*      users:(("adguard",pid=4004,fd=6))
udp   UNCONN 0      0      0.0.0.0:853       0.0.0.0:*      users:(("adguard",pid=4004,fd=8))
tcp   LISTEN 0      128    127.0.0.1:18444   0.0.0.0:*      users:(("xray",pid=5005,fd=12))
tcp   LISTEN 0      128    [::1]:9050        [::]:*         users:(("tor",pid=6006,fd=5))
`

func TestParseSSFixture(t *testing.T) {
	socks, err := Parse(ssFixture)
	if err != nil {
		t.Fatal(err)
	}
	if len(socks) != 7 {
		t.Fatalf("parsed %d sockets, want 7: %+v", len(socks), socks)
	}

	// Index 0: tcp 443 nginx
	s := socks[0]
	if s.Protocol != "tcp" || s.Port != 443 || s.Process != "nginx" || s.PID != 1001 {
		t.Errorf("socket[0] = %+v", s)
	}
	if s.Internal {
		t.Error("0.0.0.0:443 must not be internal")
	}

	// Index 1: udp 443 hysteria
	s = socks[1]
	if s.Protocol != "udp" || s.Port != 443 {
		t.Errorf("socket[1] = %+v", s)
	}
	if s.Internal {
		t.Error("::443 must not be internal")
	}

	// Index 5: xray on loopback
	s = socks[5]
	if s.Port != 18444 || s.Process != "xray" {
		t.Errorf("socket[5] = %+v", s)
	}
	if !s.Internal {
		t.Error("127.0.0.1:18444 must be internal")
	}

	// Index 6: ::1 loopback
	if !socks[6].Internal {
		t.Error("::1:9050 must be internal")
	}
}

func TestIsLoopback(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1": true,
		"127.0.0.2": true,
		"::1":       true,
		"0.0.0.0":   false,
		"::":        false,
		"10.0.0.5":  false,
		"172.16.0.1": false,
	}
	for addr, want := range cases {
		if got := IsLoopback(addr); got != want {
			t.Errorf("IsLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestIsPublic(t *testing.T) {
	cases := map[string]bool{
		"0.0.0.0":   true,
		"::":        true,
		"*":         true,
		"127.0.0.1": false,
		"::1":       false,
		"10.0.0.5":  false,
		"8.8.8.8":   true,
	}
	for addr, want := range cases {
		if got := IsPublic(addr); got != want {
			t.Errorf("IsPublic(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestParseLineInternal(t *testing.T) {
	s, err := parseLine(`tcp   LISTEN 0 511  127.0.0.1:80  0.0.0.0:*  users:(("apache2",pid=77,fd=3))`)
	if err != nil {
		t.Fatal(err)
	}
	if s.Port != 80 || s.Process != "apache2" || s.PID != 77 {
		t.Errorf("socket = %+v", s)
	}
	if !s.Internal {
		t.Error("127.0.0.1:80 should be internal")
	}
}

func TestParseLineIPv6(t *testing.T) {
	s, err := parseLine(`udp   UNCONN 0 0  [::]:5353  [::]:*  users:(("avahi",pid=9,fd=14))`)
	if err != nil {
		t.Fatal(err)
	}
	if s.Port != 5353 || s.Process != "avahi" || s.Protocol != "udp" {
		t.Errorf("socket = %+v", s)
	}
	if s.Address != "::" {
		t.Errorf("address = %q", s.Address)
	}
}

func TestParseUsersEmpty(t *testing.T) {
	pid, proc := parseUsers(`tcp LISTEN 0 511 0.0.0.0:443 0.0.0.0:*`)
	if pid != 0 || proc != "" {
		t.Errorf("expected empty users, got pid=%d proc=%q", pid, proc)
	}
}

func TestParseUsersMultiple(t *testing.T) {
	pid, proc := parseUsers(`tcp LISTEN 0 511 0.0.0.0:443 0.0.0.0:* users:(("nginx",pid=1001,fd=10,"foo",pid=1002))`)
	if pid != 1001 {
		t.Errorf("pid = %d, want 1001 (first pid wins)", pid)
	}
	if proc != "nginx" {
		t.Errorf("proc = %q", proc)
	}
}

func TestSplitAddrPort(t *testing.T) {
	cases := []struct {
		in   string
		addr string
		port int
	}{
		{"0.0.0.0:443", "0.0.0.0", 443},
		{"[::]:443", "::", 443},
		{"127.0.0.1:8443", "127.0.0.1", 8443},
		{"[2001:db8::1]:8080", "2001:db8::1", 8080},
		{"*:80", "*", 80},
	}
	for _, c := range cases {
		addr, port, err := splitAddrPort(c.in)
		if err != nil {
			t.Errorf("splitAddrPort(%q): %v", c.in, err)
			continue
		}
		if addr != c.addr || port != c.port {
			t.Errorf("splitAddrPort(%q) = %q,%d want %q,%d", c.in, addr, port, c.addr, c.port)
		}
	}
}

func TestUniquePublicPorts(t *testing.T) {
	socks, _ := Parse(ssFixture)
	ports := UniquePublicPorts(socks)
	want := []int{443, 853, 8443}
	if !reflect.DeepEqual(ports, want) {
		t.Errorf("ports = %v, want %v", ports, want)
	}
}

func TestSortByPortDeterministic(t *testing.T) {
	unsorted := []Socket{
		{Protocol: "udp", Port: 853, Address: "0.0.0.0"},
		{Protocol: "tcp", Port: 443, Address: "0.0.0.0"},
		{Protocol: "udp", Port: 443, Address: "::"},
		{Protocol: "tcp", Port: 853, Address: "0.0.0.0"},
	}
	SortByPort(unsorted)
	want := []Socket{
		{Protocol: "tcp", Port: 443, Address: "0.0.0.0"},
		{Protocol: "udp", Port: 443, Address: "::"},
		{Protocol: "tcp", Port: 853, Address: "0.0.0.0"},
		{Protocol: "udp", Port: 853, Address: "0.0.0.0"},
	}
	if !reflect.DeepEqual(unsorted, want) {
		t.Errorf("sort result = %+v, want %+v", unsorted, want)
	}
}

func TestParseMalformedLine(t *testing.T) {
	_, err := Parse("garbage line without enough fields")
	if err == nil {
		t.Fatal("expected error on malformed line")
	}
}

func TestNormalizeDedup(t *testing.T) {
	socks := []Socket{
		{Protocol: "udp", Address: "0.0.0.0", Port: 853, ServiceID: "adguard", PID: 101},
		{Protocol: "udp", Address: "0.0.0.0", Port: 853, ServiceID: "adguard", PID: 102},
		{Protocol: "udp", Address: "0.0.0.0", Port: 853, ServiceID: "adguard", PID: 103},
		{Protocol: "tcp", Address: "0.0.0.0", Port: 853, ServiceID: "adguard", PID: 101},
		{Protocol: "udp", Address: "0.0.0.0", Port: 853, ServiceID: "xray", PID: 201},
	}
	out := Normalize(socks)
	if len(out) != 3 {
		t.Fatalf("normalized = %d, want 3 (dedup 3×udp adguard, keep tcp + different svc): %+v", len(out), out)
	}
	if out[0].ProcessCount != 3 {
		t.Errorf("udp 853 adguard process_count = %d, want 3", out[0].ProcessCount)
	}
	if out[1].ProcessCount != 1 || out[2].ProcessCount != 1 {
		t.Errorf("other process counts wrong: %+v", out)
	}
}

func TestNormalizePreservesOrder(t *testing.T) {
	socks := []Socket{
		{Protocol: "tcp", Address: "0.0.0.0", Port: 443, ServiceID: "nginx", PID: 1},
		{Protocol: "udp", Address: "::", Port: 443, ServiceID: "hysteria", PID: 2},
		{Protocol: "tcp", Address: "0.0.0.0", Port: 443, ServiceID: "nginx", PID: 5},
	}
	out := Normalize(socks)
	if out[0].PID != 1 || out[1].PID != 2 || len(out) != 2 {
		t.Errorf("normalize order wrong: %+v", out)
	}
	if out[0].ProcessCount != 2 {
		t.Errorf("nginx worker count = %d, want 2", out[0].ProcessCount)
	}
}
