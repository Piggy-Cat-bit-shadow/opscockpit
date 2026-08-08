// Package restart implements the allowlist restart broker.
//
// The HTTP API only ever hands the broker a service id — never a unit name,
// container name, or shell command. The broker looks the id up in the
// services.yaml allowlist and, if restart is enabled for that service, runs a
// restart action that is constrained to that unit.
//
// This phase runs against a Mock backend only; nothing talks to systemd or a
// production host.
package restart

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sync"
	"time"

	svc "github.com/opscockpit/opscockpit/internal/collector/config"
	"github.com/opscockpit/opscockpit/internal/state"
)

// ServiceIDPattern bounds what a service id may look like. The API validates
// the id against this before any lookup, so injection payloads never reach the
// allowlist logic.
var ServiceIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// ErrUnknownService is returned when the id is not in the allowlist.
var ErrUnknownService = errors.New("unknown service")

// ErrRestartDisabled is returned when the service exists but restart is off.
var ErrRestartDisabled = errors.New("restart disabled for service")

// ErrInvalidID is returned when the id fails the pattern check.
var ErrInvalidID = errors.New("invalid service id")

// ErrCooldown is returned when a restart is attempted within the cooldown
// window for the same service.
var ErrCooldown = errors.New("restart requested too soon; cooldown active")

// Backend actually performs a restart. The production implementation does NOT
// run systemctl/docker directly — it invokes the fixed root helper argv:
//
//	sudo -n /usr/local/bin/opscockpit restart-helper --services <path> <id>
//
// The helper (running as root) re-reads the root-owned services.yaml and
// resolves the exact unit/container itself. The web layer never supplies a
// unit/container/command; only the service id.
type Backend interface {
	// Restart restarts a single allowlisted service id.
	Restart(ctx context.Context, serviceID string) error
}

// Mock is the phase-1 backend. It records restarts and can be programmed to
// fail. It never touches a real service.
type Mock struct {
	mu        sync.Mutex
	restarts  []string
	fail      bool
	delay     time.Duration
}

// NewMock returns a Mock backend.
func NewMock() *Mock { return &Mock{} }

// FailNext makes the next Restart call return an error (for tests).
func (m *Mock) FailNext() { m.mu.Lock(); m.fail = true; m.mu.Unlock() }

// SetDelay adds a delay before each restart (for tests).
func (m *Mock) SetDelay(d time.Duration) { m.mu.Lock(); m.delay = d; m.mu.Unlock() }

// Restarts returns a copy of the recorded unit restarts.
func (m *Mock) Restarts() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.restarts))
	copy(out, m.restarts)
	return out
}

// Restart implements Backend.
func (m *Mock) Restart(ctx context.Context, serviceID string) error {
	m.mu.Lock()
	if m.fail {
		m.fail = false
		m.mu.Unlock()
		return errors.New("mock restart failed")
	}
	d := m.delay
	m.mu.Unlock()

	if d > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}

	m.mu.Lock()
	m.restarts = append(m.restarts, serviceID)
	m.mu.Unlock()
	return nil
}

// Broker resolves service ids to allowlisted units and hands restart actions
// to a backend.
type Broker struct {
	// units maps service id → (unit, restartEnabled).
	units map[string]unitEntry
	// backend performs the restart.
	backend Backend
	// cooldown bounds how often the same service may be restarted.
	cooldown time.Duration
	// lastRestart tracks the last restart time per service id.
	lastRestart map[string]time.Time
	// mu guards lastRestart (restart can be triggered concurrently).
	mu sync.Mutex
}

type unitEntry struct {
	unit            string
	restartEnabled  bool
}

// NewBroker builds a broker from the services registry.
func NewBroker(entries []Entry, backend Backend) *Broker {
	return NewBrokerCooldown(entries, backend, 0)
}

// NewBrokerCooldown builds a broker with a per-service restart cooldown.
func NewBrokerCooldown(entries []Entry, backend Backend, cooldown time.Duration) *Broker {
	b := &Broker{
		units:       map[string]unitEntry{},
		backend:     backend,
		cooldown:    cooldown,
		lastRestart: map[string]time.Time{},
	}
	for _, e := range entries {
		b.units[e.ID] = unitEntry{unit: e.Unit, restartEnabled: e.RestartEnabled}
	}
	return b
}

// Entry is one allowlist entry derived from services.yaml.
type Entry struct {
	ID             string
	Unit           string
	RestartEnabled bool
}

// EntriesFromServices converts registry services to allowlist entries.
func EntriesFromServices(services []svc.Service) []Entry {
	out := make([]Entry, 0, len(services))
	for _, s := range services {
		out = append(out, Entry{ID: s.ID, Unit: s.Unit(), RestartEnabled: s.RestartEnabled})
	}
	return out
}

// EntriesFromStateServices converts state.json service entries into allowlist
// entries. This is what `serve` uses — it reads only state.json (which carries
// restart_enabled + unit), never the root-owned services.yaml. The unit is the
// state's (pre-resolved) unit, so serve never parses systemd or Docker.
func EntriesFromStateServices(services []state.Service) []Entry {
	out := make([]Entry, 0, len(services))
	for _, s := range services {
		out = append(out, Entry{ID: s.ID, Unit: s.Unit, RestartEnabled: s.RestartEnabled})
	}
	return out
}

// Restart resolves id and restarts the allowlisted unit. The unit string used
// is the allowlist's, never client-supplied.
func (b *Broker) Restart(ctx context.Context, id string) error {
	if !ServiceIDPattern.MatchString(id) {
		return ErrInvalidID
	}
	entry, ok := b.units[id]
	if !ok {
		return ErrUnknownService
	}
	if !entry.restartEnabled {
		return ErrRestartDisabled
	}
	if entry.unit == "" {
		return fmt.Errorf("service %q has no unit configured", id)
	}
	// In-memory cooldown: same service cannot be restarted twice within the
	// window (double-click / duplicate submit / rapid repeat).
	if b.cooldown > 0 {
		b.mu.Lock()
		if last, ok := b.lastRestart[id]; ok && time.Since(last) < b.cooldown {
			b.mu.Unlock()
			return ErrCooldown
		}
		b.lastRestart[id] = time.Now()
		b.mu.Unlock()
	}
	// The backend receives only the service id. It resolves the exact
	// unit/container from the root-owned registry (second trust boundary).
	return b.backend.Restart(ctx, id)
}

// Known reports whether id is in the allowlist (regardless of restart flag).
func (b *Broker) Known(id string) bool {
	if !ServiceIDPattern.MatchString(id) {
		return false
	}
	_, ok := b.units[id]
	return ok
}

// ErrHelperUnavailable is returned when a helper backend is configured but the
// helper argv could not be constructed (or the helper binary is missing).
var ErrHelperUnavailable = errors.New("restart helper not available")

// ErrUnavailable is returned when the restart API is served without a
// configured helper (production must never silently mock a restart).
var ErrUnavailable = errors.New("restart unavailable: no helper configured")

// UnavailableBackend is the production fallback when no helper is configured.
// It never pretends a restart happened — every call fails explicitly.
type UnavailableBackend struct{}

// NewUnavailableBackend returns a backend that always reports unavailable.
func NewUnavailableBackend() *UnavailableBackend { return &UnavailableBackend{} }

// Restart implements Backend.
func (u *UnavailableBackend) Restart(ctx context.Context, serviceID string) error {
	return ErrUnavailable
}

// HelperConfig configures the production restart backend.
type HelperConfig struct {
	// Helper is the opscockpit binary path used as the helper (default
	// /usr/local/bin/opscockpit). The helper itself resolves the fixed
	// /etc/opscockpit/services.yaml internally — serve never passes a path.
	Helper string
	// Sudo forces invocation via `sudo -n` (production). Empty disables sudo
	// (dev/test direct helper).
	Sudo bool
	// Timeout bounds the helper execution.
	Timeout time.Duration
	// MaxOutput caps helper stdout/stderr (2 MiB default).
	MaxOutput int
	// Runner executes the helper argv; nil uses exec.CommandContext. Tests
	// inject a mock.
	Runner func(ctx context.Context, argv []string) (string, error)
}

// HelperBackend is the production Backend. It does NOT run systemctl/docker
// itself — it invokes the fixed helper argv and lets the root helper resolve
// the exact unit/container from root-owned services.yaml.
type HelperBackend struct {
	cfg HelperConfig
}

// NewHelperBackend builds the production backend.
func NewHelperBackend(cfg HelperConfig) (*HelperBackend, error) {
	if cfg.Helper == "" {
		cfg.Helper = "/usr/local/bin/opscockpit"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 20 * time.Second
	}
	if cfg.MaxOutput <= 0 {
		cfg.MaxOutput = 2 << 20
	}
	return &HelperBackend{cfg: cfg}, nil
}

// Restart invokes the fixed helper argv:
//
//	[sudo -n] <helper> restart-helper <serviceID>
//
// No --services, --timeout, --trigger-collect, or any other caller-influenced
// flag is passed. The root helper resolves the fixed services path internally.
func (h *HelperBackend) Restart(ctx context.Context, serviceID string) error {
	if !ServiceIDPattern.MatchString(serviceID) {
		return ErrInvalidID
	}
	argv := []string{h.cfg.Helper, "restart-helper", serviceID}
	if h.cfg.Sudo {
		argv = append([]string{"sudo", "-n"}, argv...)
	}
	// The timeout bounds BOTH the exec path and a custom Runner.
	cctx, cancel := context.WithTimeout(ctx, h.cfg.Timeout)
	defer cancel()
	var out string
	var err error
	if h.cfg.Runner != nil {
		out, err = h.cfg.Runner(cctx, argv)
	} else {
		out, err = h.runExec(cctx, argv)
	}
	if err != nil {
		// Never leak helper stdout/stderr (may contain sensitive context) —
		// return a generic error.
		if out != "" && len(out) > 120 {
			out = out[:120]
		}
		return fmt.Errorf("restart helper failed")
	}
	return nil
}

// runExec executes argv directly (no shell) with a timeout and output cap.
func (h *HelperBackend) runExec(ctx context.Context, argv []string) (string, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	raw, err := cmd.CombinedOutput()
	if len(raw) > h.cfg.MaxOutput {
		raw = raw[:h.cfg.MaxOutput]
	}
	return string(raw), err
}
