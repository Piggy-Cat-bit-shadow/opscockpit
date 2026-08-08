package version

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// stub is a Command with canned output/error per call.
type stub struct {
	out string
	err error
	// timeout on the Nth call (1-based); -1 disables
	timeoutOn int
	calls     atomic.Int32
}

func (s *stub) Run(ctx context.Context, argv []string) (string, error) {
	n := s.calls.Add(1)
	if s.timeoutOn > 0 && int(n) == s.timeoutOn {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return s.out, s.err
}

func TestVersionNormal(t *testing.T) {
	c := &stub{out: "Hysteria 2.5.0\nmore lines\n"}
	got, err := Version(context.Background(), c, []string{"/usr/local/bin/hysteria", "version"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "Hysteria 2.5.0" {
		t.Errorf("got %q", got)
	}
}

func TestVersionNonZeroExit(t *testing.T) {
	c := &stub{out: "usage: hysteria [command]\n", err: errors.New("exit status 2")}
	got, err := Version(context.Background(), c, []string{"hysteria", "version"})
	if err == nil {
		t.Fatal("expected error on non-zero exit")
	}
	if got != "" {
		t.Errorf("got %q, want empty on failure", got)
	}
}

func TestVersionTimeout(t *testing.T) {
	// Timeout enforcement lives in the Command implementation (ExecCommand
	// uses context deadline + process kill). Here we simulate a command that
	// blocks forever and assert the deadline propagates.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	c := &stub{out: "", timeoutOn: 1}
	got, err := Version(ctx, c, []string{"hysteria", "version"})
	if err == nil || !IsTimeout(err) {
		t.Fatalf("expected timeout error, got %v", err)
	}
	if got != "" {
		t.Errorf("got %q", got)
	}
}

func TestVersionMissingExecutable(t *testing.T) {
	c := &stub{out: "", err: errors.New("executable file not found in $PATH")}
	got, _ := Version(context.Background(), c, []string{"/nonexistent/binary", "version"})
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestVersionEmptyOutput(t *testing.T) {
	c := &stub{out: "  \n\n"}
	got, err := Version(context.Background(), c, []string{"x", "y"})
	if err == nil {
		t.Fatal("expected error on empty output")
	}
	if got != "" {
		t.Errorf("got %q", got)
	}
}

func TestVersionEmptyArgv(t *testing.T) {
	if _, err := Version(context.Background(), &stub{}, nil); err == nil {
		t.Fatal("expected error for empty argv")
	}
}

func TestExecCommandActuallyTimesOut(t *testing.T) {
	// A command that sleeps past the timeout must be killed. Use `sleep 10`
	// with a 50ms timeout.
	ec := ExecCommand{Timeout: 50 * time.Millisecond}
	start := time.Now()
	_, err := ec.Run(context.Background(), []string{"sleep", "10"})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !IsTimeout(err) {
		t.Errorf("expected timeout error, got %v", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("timeout did not bound execution: %v", elapsed)
	}
}

func TestExecCommandArgvNotShell(t *testing.T) {
	// `echo '$HOME'` must print the literal string — proving no shell is
	// involved. A shell would expand $HOME to a real path.
	ec := ExecCommand{Timeout: 2 * time.Second}
	out, err := ec.Run(context.Background(), []string{"echo", "$HOME"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "$HOME" {
		t.Errorf("output %q — $HOME was expanded, a shell was involved", out)
	}
}
