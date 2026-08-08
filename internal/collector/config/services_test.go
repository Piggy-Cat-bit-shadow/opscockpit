package services

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestParseValidConfig(t *testing.T) {
	data := []byte(`services:
  - id: hysteria2
    name: Hysteria2
    systemd:
      unit: hysteria-server.service
    config_paths:
      - /etc/hysteria/config.yaml
    version:
      command:
        - /usr/local/bin/hysteria
        - version
      timeout: 5s
    restart_enabled: true
`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Services) != 1 {
		t.Fatalf("services = %d", len(cfg.Services))
	}
	s := cfg.Services[0]
	if s.ID != "hysteria2" || s.Name != "Hysteria2" {
		t.Errorf("service = %+v", s)
	}
	if s.Unit() != "hysteria-server.service" {
		t.Errorf("unit = %q", s.Unit())
	}
	if s.FirstConfigPath() != "/etc/hysteria/config.yaml" {
		t.Errorf("config path = %q", s.FirstConfigPath())
	}
	vc := s.VersionCommand()
	if len(vc) != 2 || vc[0] != "/usr/local/bin/hysteria" {
		t.Errorf("version command = %v", vc)
	}
	if s.VersionTimeout() != 5*time.Second {
		t.Errorf("timeout = %v", s.VersionTimeout())
	}
	if !s.RestartEnabled {
		t.Error("restart_enabled should be true")
	}
}

func TestParseDuplicateID(t *testing.T) {
	data := []byte(`services:
  - id: a
    name: A
  - id: a
    name: B
`)
	if _, err := Parse(data); err == nil {
		t.Fatal("expected duplicate id error")
	}
}

func TestParseMissingName(t *testing.T) {
	data := []byte(`services:
  - id: a
`)
	if _, err := Parse(data); err == nil {
		t.Fatal("expected missing name error")
	}
}

func TestParseInvalidID(t *testing.T) {
	data := []byte(`services:
  - id: "A B"
    name: X
`)
	if _, err := Parse(data); err == nil {
		t.Fatal("expected invalid id error")
	}
}

func TestParseEmptyUnit(t *testing.T) {
	data := []byte(`services:
  - id: a
    name: A
    systemd:
      unit: ""
`)
	if _, err := Parse(data); err == nil {
		t.Fatal("expected empty unit error")
	}
}

func TestVersionCommandIsArgv(t *testing.T) {
	// Version commands are argv vectors. The schema never stores a shell
	// string; the executor runs the vector directly (see the version
	// collector's no-shell test).
	data := []byte(`services:
  - id: a
    name: A
    version:
      command:
        - /usr/local/bin/hysteria
        - version
`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	cmd := cfg.Services[0].VersionCommand()
	if len(cmd) != 2 || cmd[0] != "/usr/local/bin/hysteria" || cmd[1] != "version" {
		t.Errorf("version command = %v, want argv [hysteria version]", cmd)
	}
}

func TestVersionCommandMustBeNonEmpty(t *testing.T) {
	data := []byte(`services:
  - id: a
    name: A
    version:
      command: []
`)
	if _, err := Parse(data); err == nil {
		t.Fatal("expected error for empty version command")
	}
}

func TestParseHealthRequiredListener(t *testing.T) {
	data := []byte(`services:
  - id: a
    name: A
    health:
      required_listeners:
        - port: 443
          protocol: udp
`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Services[0].Health == nil {
		t.Fatal("health should be set")
	}
	r := cfg.Services[0].Health.RequiredListeners[0]
	if r.Port != 443 || r.Protocol != "udp" {
		t.Errorf("required listener = %+v", r)
	}
}

func TestParseInvalidRequiredListenerPort(t *testing.T) {
	data := []byte(`services:
  - id: a
    name: A
    health:
      required_listeners:
        - port: 99999
          protocol: tcp
`)
	if _, err := Parse(data); err == nil {
		t.Fatal("expected invalid port error")
	}
}

func TestDurationParsing(t *testing.T) {
	cases := map[string]time.Duration{
		"5s":    5 * time.Second,
		"1m":    time.Minute,
		"500ms": 500 * time.Millisecond,
		"3":     3 * time.Second,
		"":      0,
	}
	for in, want := range cases {
		var d DurationOpts
		if err := yaml.Unmarshal([]byte(in), &d); err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if time.Duration(d) != want {
			t.Errorf("%q = %v, want %v", in, time.Duration(d), want)
		}
	}
}

func TestDefaultConfigParses(t *testing.T) {
	cfg, err := Parse(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Services) < 1 {
		t.Fatal("default config has no services")
	}
}

func TestByID(t *testing.T) {
	cfg, _ := Parse([]byte(`services:
  - id: a
    name: A
`))
	if cfg.ByID("a") == nil || cfg.ByID("nope") != nil {
		t.Fatal("ByID lookup wrong")
	}
}

func TestCanonicalizeConfigPaths(t *testing.T) {
	s := Service{ConfigPaths: []string{"/etc/nginx/nginx.conf", "relative/path", "/etc/xray/../xray/config.json", "  "}}
	canon := s.CanonicalizeConfigPaths()
	// Relative and blank paths are dropped; absolute paths are cleaned.
	if len(canon) != 2 {
		t.Fatalf("canonical = %v, want 2 entries", canon)
	}
	if canon[0] != "/etc/nginx/nginx.conf" {
		t.Errorf("canon[0] = %q", canon[0])
	}
	if canon[1] != "/etc/xray/config.json" {
		t.Errorf("canon[1] = %q (should clean ..)", canon[1])
	}
	// Reject a relative path entirely.
	if got := canonicalConfigPath("etc/foo.conf"); got != "" {
		t.Errorf("relative path must be rejected, got %q", got)
	}
}
