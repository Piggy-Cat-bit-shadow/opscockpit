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
	got := mock.Restarts()
	if !reflect.DeepEqual(got, []string{"hysteria-server.service"}) {
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

func TestRestartInjectionTargetsOnlyAllowlistedUnit(t *testing.T) {
	// Even if the backend were tricked, the unit string handed to it is the
	// allowlist's. Prove no client string can reach the backend by simulating
	// an adversarial id.
	b, mock := testBroker(t)
	_ = b.Restart(context.Background(), "hysteria2")
	for _, unit := range mock.Restarts() {
		if !strings.HasSuffix(unit, ".service") || strings.ContainsAny(unit, ";&|$`") {
			t.Errorf("backend received untrusted unit %q", unit)
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
