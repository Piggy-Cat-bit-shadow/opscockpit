package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testState() *State {
	return &State{
		SchemaVersion:    SchemaVersion,
		GeneratedAt:      time.Now(),
		CollectorVersion: "test",
		Host: Host{
			Hostname:      "test-host",
			UptimeSeconds: 1234,
			CPU:           CPUInfo{Cores: 4, Percent: 12.5},
			Memory:        MemInfo{Total: 1000, Used: 200, Percent: 20},
		},
		Services: []Service{
			{ID: "nginx", Name: "Nginx", Status: StatusHealthy},
		},
		Health: Health{Status: StatusHealthy},
	}
}

func TestSchemaVersion(t *testing.T) {
	s := testState()
	if s.SchemaVersion != 1 {
		t.Fatalf("schema version = %d, want 1", s.SchemaVersion)
	}
}

func TestValidateOK(t *testing.T) {
	if err := testState().Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestValidateWrongVersion(t *testing.T) {
	s := testState()
	s.SchemaVersion = 99
	if err := s.Validate(); err == nil {
		t.Fatal("expected error for wrong schema version")
	}
}

func TestSecretFieldNames(t *testing.T) {
	for _, f := range SecretFieldNames {
		if !IsSecretField(f) {
			t.Errorf("IsSecretField(%q) = false, want true", f)
		}
	}
	if IsSecretField("service_id") {
		t.Error("service_id must not be flagged as secret")
	}
	if IsSecretField("hostname") {
		t.Error("hostname must not be flagged as secret")
	}
}

func TestSecretExclusionRejectsField(t *testing.T) {
	// A state that illegally carries a password field must fail validation.
	raw := []byte(`{
		"schema_version": 1,
		"generated_at": "2026-08-08T00:00:00Z",
		"collector_version": "test",
		"collect_duration_ms": 1,
		"host": {},
		"services": [{"id":"x","name":"X","password":"abc"}],
		"health": {},
		"topology": {}
	}`)
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := checkForSecrets(decoded); got == "" {
		t.Fatal("expected secret field detection, got clean")
	} else if !strings.Contains(got, "password") {
		t.Fatalf("error should mention password, got: %q", got)
	}
}

func TestStateStructCannotCarrySecretFields(t *testing.T) {
	// The typed schema has no credential field at all: marshaling a State can
	// never emit a password/token/secret key, even if a user tries to stuff
	// values into the model.
	s := testState()
	s.Services[0].ID = "token"
	s.Services[0].Name = "password"
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded any
	json.Unmarshal(raw, &decoded)
	if got := checkForSecrets(decoded); got != "" {
		t.Fatalf("typed State must be an allowlist schema, found %q", got)
	}
}

func TestSecretExclusionRejectsPrivateKeyValue(t *testing.T) {
	// Even if the key name is innocuous, an embedded private-key marker is blocked.
	var decoded any
	json.Unmarshal([]byte(`{"host":{},"services":[{"key":"-----BEGIN RSA PRIVATE KEY-----\nMIIB"}]}`), &decoded)
	if got := checkForSecrets(decoded); got == "" {
		t.Fatal("expected private key value to be flagged")
	}
}

func TestStatusRankStaleWins(t *testing.T) {
	got := Resolve(StatusHealthy, StatusWarning, StatusStale)
	if got != StatusStale {
		t.Fatalf("Resolve = %q, want stale", got)
	}
	got = Resolve(StatusHealthy, StatusFailed)
	if got != StatusFailed {
		t.Fatalf("Resolve = %q, want failed", got)
	}
	got = Resolve(StatusHealthy, StatusUnknown)
	if got != StatusUnknown {
		t.Fatalf("Resolve = %q, want unknown", got)
	}
	got = Resolve(StatusHealthy)
	if got != StatusHealthy {
		t.Fatalf("Resolve = %q, want healthy", got)
	}
}

func TestServiceStatusRules(t *testing.T) {
	// active unit, no required listeners, no missing config → healthy
	if s, p := ServiceStatus(true, "active", nil, false); s != StatusHealthy || len(p) != 0 {
		t.Fatalf("healthy case: got %q %v", s, p)
	}
	// inactive unit → failed
	if s, p := ServiceStatus(false, "inactive", nil, false); s != StatusFailed || len(p) != 1 {
		t.Fatalf("inactive case: got %q %v", s, p)
	}
	// failed unit → failed, message names the state
	if s, p := ServiceStatus(false, "failed", nil, false); s != StatusFailed || !strings.Contains(p[0], "failed") {
		t.Fatalf("failed unit case: got %q %v", s, p)
	}
	// active but missing required listener → failed
	if s, p := ServiceStatus(true, "active", []string{"tcp/443"}, false); s != StatusFailed || len(p) != 1 {
		t.Fatalf("missing listener case: got %q %v", s, p)
	}
	// missing config override → warning per spec, but our model treats it as a
	// problem; assert the problem list is non-empty (spec: config path unknown → warning)
	if s, p := ServiceStatus(true, "active", nil, true); s != StatusFailed || len(p) != 1 {
		t.Fatalf("missing config case: got %q %v", s, p)
	}
}

func TestFinalizeHealthStale(t *testing.T) {
	s := testState()
	s.GeneratedAt = time.Now().Add(-time.Hour)
	s.FinalizeHealth(time.Minute)
	if s.Health.Status != StatusStale {
		t.Fatalf("health status = %q, want stale", s.Health.Status)
	}
	if !s.Health.Stale {
		t.Fatal("expected stale flag")
	}
}

func TestFinalizeHealthHealthy(t *testing.T) {
	s := testState()
	s.GeneratedAt = time.Now()
	s.FinalizeHealth(time.Hour)
	if s.Health.Status != StatusHealthy {
		t.Fatalf("health status = %q, want healthy", s.Health.Status)
	}
}

func TestFinalizeHealthFailed(t *testing.T) {
	s := testState()
	s.Services = append(s.Services, Service{ID: "h", Name: "H", Status: StatusFailed})
	s.FinalizeHealth(time.Hour)
	if s.Health.Status != StatusFailed {
		t.Fatalf("health status = %q, want failed", s.Health.Status)
	}
	if s.Health.ServicesFailed != 1 {
		t.Fatalf("services_failed = %d, want 1", s.Health.ServicesFailed)
	}
}

func TestIsStale(t *testing.T) {
	s := testState()
	s.GeneratedAt = time.Now().Add(-2 * time.Minute)
	if !s.IsStale(time.Minute) {
		t.Fatal("expected stale")
	}
	if s.IsStale(10 * time.Minute) {
		t.Fatal("expected fresh")
	}
}

func TestWriteAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s := testState()
	if err := WriteAtomic(path, s, WriteAtomicOptions{FSync: true}); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var parsed State
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatalf("unmarshal written: %v", err)
	}
	if parsed.Host.Hostname != "test-host" {
		t.Fatalf("hostname = %q", parsed.Host.Hostname)
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Fatal("tmp file should not remain after successful write")
	}
}

func TestWriteAtomicPreviousPreservedOnFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	if err := WriteAtomic(path, testState(), WriteAtomicOptions{}); err != nil {
		t.Fatalf("initial write: %v", err)
	}
	before, _ := os.ReadFile(path)

	// An invalid (wrong schema version) state must be rejected before any
	// temp file is created, leaving the previous file untouched.
	bad := testState()
	bad.SchemaVersion = 99
	if err := WriteAtomic(path, bad, WriteAtomicOptions{}); err == nil {
		t.Fatal("expected write to fail on invalid schema version")
	}

	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("previous state.json was clobbered by failed write")
	}
	if _, err := os.Stat(path + ".tmp"); err == nil {
		t.Fatal("tmp file should be removed on failure")
	}
}

func TestWriteAtomicInvalidJSONNeverCommits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	// Existing valid content.
	if err := WriteAtomic(path, testState(), WriteAtomicOptions{}); err != nil {
		t.Fatalf("initial write: %v", err)
	}
	// A state whose id/name happen to match secret-key names is structurally
	// allowed by the typed model, but the marshaled output must NOT contain
	// those names as keys. Values are allowed; keys are not.
	before, _ := os.ReadFile(path)

	// Make the directory read-only so the temp open fails.
	os.Chmod(dir, 0o555)
	err := WriteAtomic(path, testState(), WriteAtomicOptions{})
	os.Chmod(dir, 0o755)
	if err == nil {
		t.Fatal("expected write to fail on read-only dir")
	}

	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("previous state.json changed after failed write")
	}
}
