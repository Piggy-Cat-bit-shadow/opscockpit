// Package version runs a service's version command (argv only, never a shell
// string) with a hard timeout. Failures are reported but never treated as a
// service fault by the health model — the UI just shows "—".
package version

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// Command runs an argv vector. The real implementation uses exec.CommandContext;
// tests substitute a stub.
type Command interface {
	Run(ctx context.Context, argv []string) (string, error)
}

// ExecCommand is the real command runner.
type ExecCommand struct {
	// Timeout bounds every version command.
	Timeout time.Duration
}

// Run executes argv directly (no shell), capturing stdout+stderr.
func (e ExecCommand) Run(ctx context.Context, argv []string) (string, error) {
	timeout := e.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// ErrNotFound is returned when the executable does not exist.
var ErrNotFound = errors.New("executable not found")

// IsTimeout reports whether err is a deadline-exceeded error from the command.
// Go's exec.CommandContext may surface a killed process as "signal: killed"
// rather than wrapping context.DeadlineExceeded, so both are treated as
// timeouts here.
func IsTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if err != nil && strings.Contains(err.Error(), "signal: killed") {
		return true
	}
	return false
}

// Version runs argv and returns the first non-empty line of output. On any
// failure (missing binary, timeout, non-zero exit, empty output) it returns ""
// plus the error. Callers treat "" as "version unknown".
func Version(ctx context.Context, c Command, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", errors.New("empty version command")
	}
	out, err := c.Run(ctx, argv)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line, nil
		}
	}
	return "", errors.New("empty version output")
}
