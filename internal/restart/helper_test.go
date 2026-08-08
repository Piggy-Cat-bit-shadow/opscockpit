package restart

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	svc "github.com/opscockpit/opscockpit/internal/collector/config"
)

// writeServices writes a services.yaml and returns its path.
func writeServices(t *testing.T, content string) string {
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

// HelperBackend with a mock runner that records argv.
type recordingRunner struct {
	argv [][]string
	err  error
}

func (r *recordingRunner) Run(ctx context.Context, argv []string) (string, error) {
	r.argv = append(r.argv, append([]string{}, argv...))
	if r.err != nil {
		return "", r.err
	}
	return "ok", nil
}

func TestHelperBackendConstructsFixedArgv(t *testing.T) {
	rec := &recordingRunner{}
	b, err := NewHelperBackend(HelperConfig{
		Helper:       "/opt/opscockpit",
		ServicesPath: "/etc/opscockpit/services.yaml",
		Sudo:         true,
		Runner:       rec.Run,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Restart(context.Background(), "hysteria2"); err != nil {
		t.Fatal(err)
	}
	if len(rec.argv) != 1 {
		t.Fatalf("argv = %v", rec.argv)
	}
	want := []string{"sudo", "-n", "/opt/opscockpit", "restart-helper", "--services", "/etc/opscockpit/services.yaml", "hysteria2"}
	if !equalStrings(rec.argv[0], want) {
		t.Fatalf("argv = %v, want %v", rec.argv[0], want)
	}
}

func TestHelperBackendNoSudo(t *testing.T) {
	rec := &recordingRunner{}
	b, err := NewHelperBackend(HelperConfig{
		ServicesPath: "/etc/opscockpit/services.yaml",
		Sudo:         false,
		Runner:       rec.Run,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = b.Restart(context.Background(), "nginx")
	if len(rec.argv) != 1 || rec.argv[0][0] == "sudo" {
		t.Fatalf("no-sudo backend argv = %v", rec.argv)
	}
}

func TestHelperBackendRejectsInvalidID(t *testing.T) {
	rec := &recordingRunner{}
	b, _ := NewHelperBackend(HelperConfig{ServicesPath: "/etc/x", Sudo: false, Runner: rec.Run})
	if err := b.Restart(context.Background(), "evil;rm -rf /"); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("err = %v, want ErrInvalidID", err)
	}
	if len(rec.argv) != 0 {
		t.Fatal("invalid id must not reach the runner")
	}
}

func TestHelperBackendMissingServicesPath(t *testing.T) {
	if _, err := NewHelperBackend(HelperConfig{}); err == nil {
		t.Fatal("expected error when services path is empty")
	}
}

func TestHelperBackendErrorsNeverLeakOutput(t *testing.T) {
	rec := &recordingRunner{err: errors.New("boom")}
	b, _ := NewHelperBackend(HelperConfig{ServicesPath: "/etc/x", Sudo: false, Runner: rec.Run})
	err := b.Restart(context.Background(), "nginx")
	if err == nil {
		t.Fatal("expected error")
	}
	// The error must not contain the runner's stdout (potential secrets).
	if got := err.Error(); contains(got, "boom") {
		t.Fatalf("error leaked runner output: %q", got)
	}
}

func TestUnavailableBackend(t *testing.T) {
	u := NewUnavailableBackend()
	if err := u.Restart(context.Background(), "nginx"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestHelperBackendTimeoutBounded(t *testing.T) {
	// The backend applies a context timeout around the helper; verify a slow
	// runner is bounded by the config timeout, not left hanging.
	rec := &recordingRunner{}
	b, _ := NewHelperBackend(HelperConfig{
		ServicesPath: "/etc/x",
		Sudo:         false,
		Timeout:      10 * time.Millisecond,
		Runner: func(ctx context.Context, argv []string) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		},
	})
	start := time.Now()
	_ = b.Restart(context.Background(), "nginx")
	if time.Since(start) > 2*time.Second {
		t.Fatalf("helper not bounded by timeout: %v", time.Since(start))
	}
	_ = rec
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

var _ = svc.Service{}
