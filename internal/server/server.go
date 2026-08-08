// Package server exposes the tiny HTTP API and serves the embedded frontend.
//
// Endpoints:
//
//	GET  /api/state                   — full Runtime Digital Twin
//	GET  /api/healthz                 — health liveness probe
//	POST /api/services/{id}/restart   — allowlist restart (id only, no unit/shell)
//
// Everything else falls through to the embedded frontend static assets.
package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/opscockpit/opscockpit/internal/restart"
	"github.com/opscockpit/opscockpit/internal/state"
)

// StateStore is how the server reads the current state. The collect command
// writes state.json; the serve command loads it on demand.
type StateStore interface {
	// LoadState returns the current state and its hash for ETag purposes.
	LoadState() (*state.State, string, error)
}

// FileStore loads state.json from a path, hashing the raw bytes for ETags.
type FileStore struct {
	Path string
}

// LoadState implements StateStore.
func (f FileStore) LoadState() (*state.State, string, error) {
	raw, err := readFile(f.Path)
	if err != nil {
		return nil, "", err
	}
	var st state.State
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	return &st, hex.EncodeToString(sum[:]), nil
}

// Server is the HTTP handler.
type Server struct {
	Store   StateStore
	Restart *restart.Broker
	// StaleAfter is the age after which /api/state reports stale (2.5× interval).
	StaleAfter time.Duration

	mu      sync.RWMutex
	static  http.Handler
	index   []byte
	hasHTML bool
}

// New builds a Server with the embedded frontend assets.
func New(store StateStore, broker *restart.Broker, static http.Handler, hasHTML bool, index []byte) *Server {
	s := &Server{
		Store:      store,
		Restart:    broker,
		StaleAfter: 5 * time.Minute,
		static:     static,
		index:      index,
		hasHTML:    hasHTML,
	}
	if s.StaleAfter <= 0 {
		s.StaleAfter = 5 * time.Minute
	}
	return s
}

// noStore disables caching on API responses.
func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}

// HandleState serves GET /api/state.
func (s *Server) HandleState(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	st, etag, err := s.Store.LoadState()
	if err != nil {
		http.Error(w, "state unavailable", http.StatusServiceUnavailable)
		return
	}

	// ETag / 304.
	if inm := r.Header.Get("If-None-Match"); inm != "" && etag != "" {
		if inm == etag || strings.Contains(inm, etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.Header().Set("ETag", `"`+etag+`"`)
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(st)
}

// HandleHealthz serves GET /api/healthz.
func (s *Server) HandleHealthz(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	w.Header().Set("Content-Type", "application/json")
	status := "ok"
	code := http.StatusOK
	if st, _, err := s.Store.LoadState(); err == nil {
		if st.Health.Status == state.StatusStale {
			status = "stale"
			code = http.StatusServiceUnavailable
		}
	}
	w.WriteHeader(code)
	_, _ = w.Write([]byte(`{"status":"` + status + `"}`))
}

// HandleRestart serves POST /api/services/{id}/restart.
func (s *Server) HandleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	noStore(w)

	id := strings.TrimPrefix(r.URL.Path, "/api/services/")
	id = strings.TrimSuffix(id, "/restart")
	if id == "" {
		http.Error(w, "service id required", http.StatusBadRequest)
		return
	}

	// The client may submit nothing but the id — reject any body outright so
	// nothing client-controlled (unit, container, shell) can be smuggled in.
	if r.Body != nil {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if len(strings.TrimSpace(string(body))) > 0 {
			http.Error(w, "request body must be empty", http.StatusBadRequest)
			return
		}
	}

	ctx := r.Context()
	err := s.Restart.Restart(ctx, id)
	switch {
	case errors.Is(err, restart.ErrInvalidID):
		http.Error(w, "invalid service id", http.StatusBadRequest)
	case errors.Is(err, restart.ErrUnknownService):
		http.Error(w, "unknown service", http.StatusNotFound)
	case errors.Is(err, restart.ErrRestartDisabled):
		http.Error(w, "restart disabled for service", http.StatusForbidden)
	case err != nil:
		http.Error(w, "restart failed", http.StatusInternalServerError)
	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"restarting","service_id":"` + id + `"}`))
	}
}

// ServeHTTP routes API paths and falls through to static assets.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case p == "/api/state":
		s.HandleState(w, r)
	case p == "/api/healthz":
		s.HandleHealthz(w, r)
	case strings.HasPrefix(p, "/api/services/") && strings.HasSuffix(p, "/restart"):
		s.HandleRestart(w, r)
	case strings.HasPrefix(p, "/api/restart"):
		// legacy alias guard
		http.Error(w, "not found", http.StatusNotFound)
	default:
		s.serveStatic(w, r)
	}
}

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if s.static == nil {
		http.NotFound(w, r)
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" {
		p = "index.html"
	}
	if strings.HasSuffix(p, ".html") {
		// SPA: return the committed index for any HTML route.
		if s.hasHTML {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(s.index)
			return
		}
	}
	// Try static file; fall back to SPA index for unknown routes.
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/" + p
	if s.static != nil {
		s.static.ServeHTTP(w, r2)
		return
	}
	http.NotFound(w, r)
}
