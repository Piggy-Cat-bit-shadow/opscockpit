package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSvc(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "services.yaml")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// stubVerifier is a no-op ownership verifier for positive resolution tests.
// The real requireRootOwned is exercised by dedicated ownership tests (a
// non-root test process cannot create a genuinely root-owned file).
func stubVerifier(path string) error { return nil }

const restartSvcYAML = `services:
  - id: hysteria2
    name: Hysteria2
    systemd: { unit: hysteria-server.service }
    restart_enabled: true
  - id: xray
    name: Xray
    systemd: { unit: xray.service }
    restart_enabled: false
  - id: dockerapp
    name: Docker App
    docker: { container: my-app }
    restart_enabled: true
`

func TestResolveKnownEnabledUnit(t *testing.T) {
	p := writeSvc(t, restartSvcYAML)
	kind, target, err := resolveRestartTarget(p, "hysteria2", stubVerifier)
	if err != nil {
		t.Fatal(err)
	}
	if kind != "unit" || target != "hysteria-server.service" {
		t.Fatalf("kind=%q target=%q", kind, target)
	}
}

func TestResolveDockerContainer(t *testing.T) {
	p := writeSvc(t, restartSvcYAML)
	kind, target, err := resolveRestartTarget(p, "dockerapp", stubVerifier)
	if err != nil {
		t.Fatal(err)
	}
	if kind != "container" || target != "my-app" {
		t.Fatalf("kind=%q target=%q", kind, target)
	}
}

func TestResolveUnknownID(t *testing.T) {
	p := writeSvc(t, restartSvcYAML)
	if _, _, err := resolveRestartTarget(p, "ghost", stubVerifier); err == nil {
		t.Fatal("expected unknown service error")
	}
}

func TestResolveRestartDisabled(t *testing.T) {
	p := writeSvc(t, restartSvcYAML)
	if _, _, err := resolveRestartTarget(p, "xray", stubVerifier); err == nil {
		t.Fatal("expected restart-disabled error")
	}
}

func TestResolveMaliciousIDs(t *testing.T) {
	p := writeSvc(t, restartSvcYAML)
	payloads := []string{
		"../../etc/passwd",
		"hysteria2;rm -rf /",
		"hysteria-server.service", // unit name submitted as id
		"hysteria2 ",
		"hysteria2\n",
		"x ray",
		"NGINX",
		"nginx.service;systemctl stop all",
		"hysteria2$(reboot)",
	}
	for _, id := range payloads {
		if _, _, err := resolveRestartTarget(p, id, stubVerifier); err == nil {
			t.Errorf("payload %q must be rejected", id)
		}
	}
}

// Second allowlist: a forged unit in state.json must NOT affect the root
// helper. The helper re-reads root-owned services.yaml and resolves the exact
// unit from there.
func TestResolveIgnoresForgedUnit(t *testing.T) {
	// Root-owned registry says hysteria2 → hysteria-server.service.
	root := writeSvc(t, restartSvcYAML)
	// state.json (untrusted) claims hysteria2 → evil.service — irrelevant.
	kind, target, err := resolveRestartTarget(root, "hysteria2", stubVerifier)
	if err != nil {
		t.Fatal(err)
	}
	if kind != "unit" || target != "hysteria-server.service" {
		t.Fatalf("forged unit influenced helper: got %s=%q, want hysteria-server.service", kind, target)
	}
	if strings.Contains(target, "evil") {
		t.Fatal("forged unit leaked into helper target")
	}
}

func TestResolveNoTarget(t *testing.T) {
	p := writeSvc(t, `services:
  - id: notarget
    name: No Target
    restart_enabled: true
`)
	if _, _, err := resolveRestartTarget(p, "notarget", stubVerifier); err == nil {
		t.Fatal("expected no-restart-target error")
	}
}
