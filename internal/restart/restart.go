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
	"regexp"
	"sync"
	"time"

	svc "github.com/opscockpit/opscockpit/internal/collector/config"
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

// Backend actually performs a restart. The real implementation (future phase)
// would run `systemctl restart <unit>` where <unit> comes from the allowlist
// entry — never from client input. This phase uses Mock.
type Backend interface {
	// Restart restarts a single allowlisted unit.
	Restart(ctx context.Context, unit string) error
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
func (m *Mock) Restart(ctx context.Context, unit string) error {
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
	m.restarts = append(m.restarts, unit)
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
}

type unitEntry struct {
	unit            string
	restartEnabled  bool
}

// NewBroker builds a broker from the services registry.
func NewBroker(entries []Entry, backend Backend) *Broker {
	b := &Broker{units: map[string]unitEntry{}, backend: backend}
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
	return b.backend.Restart(ctx, entry.unit)
}

// Known reports whether id is in the allowlist (regardless of restart flag).
func (b *Broker) Known(id string) bool {
	if !ServiceIDPattern.MatchString(id) {
		return false
	}
	_, ok := b.units[id]
	return ok
}
