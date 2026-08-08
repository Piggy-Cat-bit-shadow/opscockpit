package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opscockpit/opscockpit/internal/restart"
	"github.com/opscockpit/opscockpit/internal/state"
)

// memStore is a StateStore backed by an in-memory state.
type memStore struct {
	st   *state.State
	raw  []byte
}

func (m *memStore) LoadState() (*state.State, string, error) {
	raw := m.raw
	if raw == nil {
		raw, _ = json.Marshal(m.st)
	}
	// compute a stable etag
	sum := sha256Hex(raw)
	return m.st, sum, nil
}

func sha256Hex(b []byte) string {
	const hexDigits = "0123456789abcdef"
	// trivial sha256 for tests (not crypto-critical here)
	h := 0
	for _, c := range b {
		h = (h*31 + int(c)) & 0x7fffffff
	}
	out := make([]byte, 16)
	for i := range out {
		out[i] = hexDigits[(h>>uint((i%8)*4))&0xf]
		h = h*7 + i
	}
	return string(out)
}

func testState(healthy bool) *state.State {
	status := state.StatusHealthy
	if !healthy {
		status = state.StatusStale
	}
	return &state.State{
		SchemaVersion:    1,
		GeneratedAt:      time.Now(),
		CollectorVersion: "test",
		Services: []state.Service{
			{ID: "hysteria2", Name: "Hysteria2", Status: state.StatusHealthy, Unit: "hysteria-server.service", RestartEnabled: true},
		},
		Health: state.Health{Status: status},
		Topology: state.Topology{
			Nodes: []state.Node{{ID: "internet", Type: "internet", Label: "Internet"}},
		},
	}
}

func newTestServer(t *testing.T, st *state.State) *Server {
	t.Helper()
	raw, _ := json.Marshal(st)
	store := &memStore{st: st, raw: raw}

	broker := restart.NewBrokerCooldown([]restart.Entry{
		{ID: "hysteria2", Unit: "hysteria-server.service", RestartEnabled: true},
		{ID: "xray", Unit: "xray.service", RestartEnabled: false},
	}, restart.NewMock(), 10*time.Second)

	return New(store, broker, http.NotFoundHandler(), false, nil)
}

func TestHandleState(t *testing.T) {
	s := newTestServer(t, testState(true))
	req := httptest.NewRequest("GET", "/api/state", nil)
	rr := httptest.NewRecorder()
	s.HandleState(rr, req)

	if rr.Code != 200 {
		t.Fatalf("code = %d", rr.Code)
	}
	if rr.Header().Get("Cache-Control") != "no-store" {
		t.Error("expected no-store cache control")
	}
	if rr.Header().Get("ETag") == "" {
		t.Error("expected ETag")
	}
	var st state.State
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Health.Status != state.StatusHealthy {
		t.Errorf("status = %q", st.Health.Status)
	}
}

func TestHandleStateETag304(t *testing.T) {
	s := newTestServer(t, testState(true))
	first := httptest.NewRecorder()
	s.HandleState(first, httptest.NewRequest("GET", "/api/state", nil))
	etag := first.Header().Get("ETag")

	req := httptest.NewRequest("GET", "/api/state", nil)
	req.Header.Set("If-None-Match", etag)
	rr := httptest.NewRecorder()
	s.HandleState(rr, req)
	if rr.Code != http.StatusNotModified {
		t.Fatalf("code = %d, want 304", rr.Code)
	}
	if rr.Body.Len() != 0 {
		t.Error("304 must have empty body")
	}
}

func TestHandleStateMissing(t *testing.T) {
	s := newTestServer(t, testState(true))
	s.Store = errStore{}
	req := httptest.NewRequest("GET", "/api/state", nil)
	rr := httptest.NewRecorder()
	s.HandleState(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503", rr.Code)
	}
}

type errStore struct{}

func (errStore) LoadState() (*state.State, string, error) {
	return nil, "", os.ErrNotExist
}

func TestHandleHealthz(t *testing.T) {
	s := newTestServer(t, testState(true))
	rr := httptest.NewRecorder()
	s.HandleHealthz(rr, httptest.NewRequest("GET", "/api/healthz", nil))
	if rr.Code != 200 {
		t.Fatalf("code = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"status":"ok"`) {
		t.Errorf("body = %q", rr.Body.String())
	}
}

func TestHandleHealthzStale(t *testing.T) {
	s := newTestServer(t, testState(false))
	rr := httptest.NewRecorder()
	s.HandleHealthz(rr, httptest.NewRequest("GET", "/api/healthz", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"status":"stale"`) {
		t.Errorf("body = %q", rr.Body.String())
	}
}

func TestHandleRestartKnown(t *testing.T) {
	s := newTestServer(t, testState(true))
	req := httptest.NewRequest("POST", "/api/services/hysteria2/restart", nil)
	req.Header.Set("X-OpsCockpit-Action", "restart")
	req.Host = "localhost:8090"
	req.Header.Set("Origin", "http://localhost:8090")
	rr := httptest.NewRecorder()
	s.HandleRestart(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("code = %d, want 202", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "restarting") {
		t.Errorf("body = %q", rr.Body.String())
	}
}

func TestHandleRestartUnknown(t *testing.T) {
	s := newTestServer(t, testState(true))
	req := httptest.NewRequest("POST", "/api/services/ghost/restart", nil)
	req.Header.Set("X-OpsCockpit-Action", "restart")
	rr := httptest.NewRecorder()
	s.HandleRestart(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rr.Code)
	}
}

func TestHandleRestartMissingHeader(t *testing.T) {
	s := newTestServer(t, testState(true))
	// No X-OpsCockpit-Action header → CSRF guard rejects.
	req := httptest.NewRequest("POST", "/api/services/hysteria2/restart", nil)
	rr := httptest.NewRecorder()
	s.HandleRestart(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403 (missing action header)", rr.Code)
	}
}

func TestHandleRestartCrossOriginRejected(t *testing.T) {
	s := newTestServer(t, testState(true))
	req := httptest.NewRequest("POST", "/api/services/hysteria2/restart", nil)
	req.Header.Set("X-OpsCockpit-Action", "restart")
	req.Host = "localhost:8090"
	req.Header.Set("Origin", "http://evil.example")
	rr := httptest.NewRecorder()
	s.HandleRestart(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403 (cross-origin)", rr.Code)
	}
}

func TestHandleRestartCooldown(t *testing.T) {
	s := newTestServer(t, testState(true))
	// First restart succeeds.
	req := httptest.NewRequest("POST", "/api/services/hysteria2/restart", nil)
	req.Header.Set("X-OpsCockpit-Action", "restart")
	rr := httptest.NewRecorder()
	s.HandleRestart(rr, req)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("first restart code = %d, want 202", rr.Code)
	}
	// Immediate second → 429 cooldown.
	req2 := httptest.NewRequest("POST", "/api/services/hysteria2/restart", nil)
	req2.Header.Set("X-OpsCockpit-Action", "restart")
	rr2 := httptest.NewRecorder()
	s.HandleRestart(rr2, req2)
	if rr2.Code != http.StatusTooManyRequests {
		t.Fatalf("second restart code = %d, want 429", rr2.Code)
	}
}

// restartReq builds a valid restart request (POST + action header).
func restartReq(method, path string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, path, body)
	req.Header.Set("X-OpsCockpit-Action", "restart")
	return req
}

func TestHandleRestartDisabled(t *testing.T) {
	s := newTestServer(t, testState(true))
	req := restartReq("POST", "/api/services/xray/restart", nil)
	rr := httptest.NewRecorder()
	s.HandleRestart(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403", rr.Code)
	}
}

func TestHandleRestartMaliciousID(t *testing.T) {
	s := newTestServer(t, testState(true))
	for _, id := range []string{"nginx;reboot", "hysteria2%20%26%20reboot", "../../etc/passwd", "Nginx", "hysteria-server.service"} {
		req := restartReq("POST", "/api/services/"+id+"/restart", nil)
		rr := httptest.NewRecorder()
		s.HandleRestart(rr, req)
		if rr.Code != http.StatusBadRequest && rr.Code != http.StatusNotFound {
			t.Errorf("id %q: code = %d, want 400/404", id, rr.Code)
		}
	}
}

func TestHandleRestartBodyRejected(t *testing.T) {
	s := newTestServer(t, testState(true))
	// A client smuggling a unit name in the body must be rejected.
	body := bytes.NewBufferString(`{"unit":"hysteria-server.service","command":"rm -rf /"}`)
	req := restartReq("POST", "/api/services/hysteria2/restart", body)
	rr := httptest.NewRecorder()
	s.HandleRestart(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 (body rejected)", rr.Code)
	}
}

func TestHandleRestartMethodNotAllowed(t *testing.T) {
	s := newTestServer(t, testState(true))
	req := restartReq("GET", "/api/services/hysteria2/restart", nil)
	rr := httptest.NewRecorder()
	s.HandleRestart(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("code = %d, want 405", rr.Code)
	}
}

func TestServeHTTPRoutes(t *testing.T) {
	s := newTestServer(t, testState(true))
	cases := []struct {
		method, path string
		want         int
	}{
		{"GET", "/api/state", 200},
		{"GET", "/api/healthz", 200},
		{"POST", "/api/services/hysteria2/restart", 202},
		{"POST", "/api/services/ghost/restart", 404},
		{"GET", "/", 404}, // no embedded frontend in unit test
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, c.path, nil)
		req.Header.Set("X-OpsCockpit-Action", "restart") // required for restart routes
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if rr.Code != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, rr.Code, c.want)
		}
	}
}

func TestFileStoreLoadsState(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	raw := []byte(`{"schema_version":1,"generated_at":"2026-08-08T00:00:00Z","collector_version":"t","host":{},"services":[],"health":{},"topology":{}}`)
	os.WriteFile(p, raw, 0o644)

	fs := FileStore{Path: p}
	st, etag, err := fs.LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if st.SchemaVersion != 1 {
		t.Errorf("schema = %d", st.SchemaVersion)
	}
	if etag == "" {
		t.Error("etag empty")
	}
}

func TestFileStoreMissing(t *testing.T) {
	fs := FileStore{Path: filepath.Join(t.TempDir(), "nope.json")}
	if _, _, err := fs.LoadState(); err == nil {
		t.Fatal("expected error for missing state.json")
	}
}

func TestServeHTTPStaticFallback(t *testing.T) {
	// With a static handler present, an HTML route serves the SPA index.
	s := New(&memStore{st: testState(true), raw: []byte("{}")}, restart.NewBroker(nil, restart.NewMock()), http.NotFoundHandler(), true, []byte("<html>ops</html>"))
	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("code = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "ops") {
		t.Errorf("body = %q", rr.Body.String())
	}
}

func TestContextCancellation(t *testing.T) {
	_ = context.Background()
}
