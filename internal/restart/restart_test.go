package restart

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	svc "github.com/opscockpit/opscockpit/internal/collector/config"
)

func testBroker(t *testing.T) (*Broker, *Mock) {
	t.Helper()
	services := []svc.Service{
		{ID: "nginx", Name: "Nginx", RestartEnabled: true, Systemd: &svc.SystemdConfig{Unit: "nginx.service"}},
		{ID: "hysteria2", Name: "Hysteria2", RestartEnabled: true, Systemd: &svc.SystemdConfig{Unit: "hysteria-server.service"}},
		{ID: "xray", Name: "Xray", RestartEnabled: false, Systemd: &svc.SystemdConfig{Unit: "xray.service"}},
	}
	mock := NewMock()
	b := NewBroker(EntriesFromServices(services), mock)
	return b, mock
}

func TestRestartKnownService(t *testing.T) {
	b, mock := testBroker(t)
	if err := b.Restart(context.Background(), "hysteria2"); err != nil {
		t.Fatal(err)
	}
	// The broker hands the backend the service id; the root helper resolves
	// the exact unit from root-owned services.yaml (second boundary).
	got := mock.Restarts()
	if !reflect.DeepEqual(got, []string{"hysteria2"}) {
		t.Fatalf("restarts = %v", got)
	}
}

func TestRestartUnknownService(t *testing.T) {
	b, mock := testBroker(t)
	if err := b.Restart(context.Background(), "ghost"); err != ErrUnknownService {
		t.Fatalf("err = %v, want ErrUnknownService", err)
	}
	if len(mock.Restarts()) != 0 {
		t.Fatal("unknown service must not trigger a restart")
	}
}

func TestRestartDisabled(t *testing.T) {
	b, mock := testBroker(t)
	if err := b.Restart(context.Background(), "xray"); err != ErrRestartDisabled {
		t.Fatalf("err = %v, want ErrRestartDisabled", err)
	}
	if len(mock.Restarts()) != 0 {
		t.Fatal("disabled service must not trigger a restart")
	}
}

func TestRestartMaliciousID(t *testing.T) {
	b, mock := testBroker(t)
	payloads := []string{
		"nginx; rm -rf /",
		"nginx && systemctl stop all",
		"../../etc/passwd",
		"NGINX",            // uppercase — pattern rejects
		"nginx.service",    // unit name submitted as id — rejected
		`nginx$(reboot)`,
		"x ray",
		"",
	}
	for _, p := range payloads {
		if err := b.Restart(context.Background(), p); err != ErrInvalidID && err != ErrUnknownService {
			t.Errorf("payload %q: err = %v, want ErrInvalidID or ErrUnknownService", p, err)
		}
	}
	if len(mock.Restarts()) != 0 {
		t.Fatalf("malicious payloads triggered restarts: %v", mock.Restarts())
	}
}

func TestRestartBackendReceivesOnlyServiceID(t *testing.T) {
	// The backend must only ever receive the validated service id — never a
	// unit name, container name, or anything client-influenced. The root
	// helper resolves the exact unit/container from root-owned services.yaml.
	b, mock := testBroker(t)
	for _, id := range []string{"hysteria2", "nginx"} {
		if err := b.Restart(context.Background(), id); err != nil {
			t.Fatalf("restart %s: %v", id, err)
		}
	}
	for _, got := range mock.Restarts() {
		if !ServiceIDPattern.MatchString(got) {
			t.Errorf("backend received non-service-id %q", got)
		}
		if strings.ContainsAny(got, ".;&|$` /\\\n") {
			t.Errorf("backend received suspicious value %q", got)
		}
	}
}

func TestRestartPatternMatchesValid(t *testing.T) {
	for _, id := range []string{"nginx", "adguard-home", "my.service", "svc_1"} {
		if !ServiceIDPattern.MatchString(id) {
			t.Errorf("id %q should match", id)
		}
	}
	for _, id := range []string{"", "NGINX", "a b", "a;b", "../x", "a$b", "a&b"} {
		if ServiceIDPattern.MatchString(id) {
			t.Errorf("id %q must NOT match", id)
		}
	}
}

func TestMockFailure(t *testing.T) {
	b, mock := testBroker(t)
	mock.FailNext()
	if err := b.Restart(context.Background(), "nginx"); err == nil {
		t.Fatal("expected failure")
	}
	// Next call succeeds.
	if err := b.Restart(context.Background(), "nginx"); err != nil {
		t.Fatal(err)
	}
}

func TestMockContextCancel(t *testing.T) {
	b, mock := testBroker(t)
	mock.SetDelay(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.Restart(ctx, "nginx"); err == nil || errors.Is(err, context.Canceled) == false {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func TestBrokerKnown(t *testing.T) {
	b, _ := testBroker(t)
	if !b.Known("nginx") {
		t.Error("nginx should be known")
	}
	if b.Known("nope") {
		t.Error("nope should be unknown")
	}
	if b.Known("NGINX") {
		t.Error("uppercase id should be rejected")
	}
}

func TestServiceWithoutUnitRejected(t *testing.T) {
	services := []svc.Service{
		{ID: "no-unit", Name: "No Unit", RestartEnabled: true},
	}
	b := NewBroker(EntriesFromServices(services), NewMock())
	if err := b.Restart(context.Background(), "no-unit"); err == nil {
		t.Fatal("expected error for service with no unit")
	}
}

func TestRestartCooldown(t *testing.T) {
	services := []svc.Service{
		{ID: "nginx", Name: "Nginx", RestartEnabled: true, Systemd: &svc.SystemdConfig{Unit: "nginx.service"}},
	}
	mock := NewMock()
	b := NewBrokerCooldown(EntriesFromServices(services), mock, time.Second)
	if err := b.Restart(context.Background(), "nginx"); err != nil {
		t.Fatal(err)
	}
	// Immediate second → cooldown.
	if err := b.Restart(context.Background(), "nginx"); err != ErrCooldown {
		t.Fatalf("second restart err = %v, want ErrCooldown", err)
	}
	if len(mock.Restarts()) != 1 {
		t.Fatalf("cooldown must prevent a second restart, got %d", len(mock.Restarts()))
	}
}

func TestRestartNoCooldownWithoutWindow(t *testing.T) {
	services := []svc.Service{
		{ID: "nginx", Name: "Nginx", RestartEnabled: true, Systemd: &svc.SystemdConfig{Unit: "nginx.service"}},
	}
	mock := NewMock()
	b := NewBroker(EntriesFromServices(services), mock) // no cooldown
	if err := b.Restart(context.Background(), "nginx"); err != nil {
		t.Fatal(err)
	}
	if err := b.Restart(context.Background(), "nginx"); err != nil {
		t.Fatalf("second restart with no cooldown: %v", err)
	}
	if len(mock.Restarts()) != 2 {
		t.Fatalf("no cooldown → 2 restarts, got %d", len(mock.Restarts()))
	}
}
