// Package collect provides the production Runner that executes real system
// commands, plus the collect orchestration.
package collect

import (
	"context"
	"os/exec"
	"time"
)

// ProductionRunner executes real commands. It is the production counterpart of
// the mock runners used in tests.
type ProductionRunner struct {
	// SSCommand is the `ss` binary path (default "ss").
	SSCommand string
	// Systemctl is the `systemctl` binary path (default "systemctl").
	Systemctl string
	// Timeout bounds each systemctl show query.
	Timeout time.Duration
	// resolvePID maps a PID to a service id using cgroup + unit lookup.
	resolvePID func(pid int) string
}

// NewProductionRunner builds a runner with no PID→service mapping (the
// production mapping is wired by the caller via SetPIDResolver).
func NewProductionRunner() *ProductionRunner {
	return &ProductionRunner{Timeout: 5 * time.Second}
}

// SetPIDResolver installs the PID → service id mapping function.
func (r *ProductionRunner) SetPIDResolver(fn func(pid int) string) { r.resolvePID = fn }

// Run executes an argv vector directly.
func (r *ProductionRunner) Run(ctx context.Context, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", nil
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// RunUnit runs `systemctl show <unit> --property=...`.
func (r *ProductionRunner) RunUnit(ctx context.Context, unit string, properties []string) (string, error) {
	bin := r.Systemctl
	if bin == "" {
		bin = "systemctl"
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	argv := []string{bin, "show", unit}
	for _, p := range properties {
		argv = append(argv, "--property="+p)
	}
	out, err := exec.CommandContext(cctx, argv[0], argv[1:]...).CombinedOutput()
	return string(out), err
}

// SS runs `ss -H -lntup`.
func (r *ProductionRunner) SS(ctx context.Context) (string, error) {
	bin := r.SSCommand
	if bin == "" {
		bin = "ss"
	}
	cmd := exec.CommandContext(ctx, bin, "-H", "-lntup")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Version runs a version argv.
func (r *ProductionRunner) Version(ctx context.Context, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", nil
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ResolveServiceID implements the collect hook.
func (r *ProductionRunner) ResolveServiceID(pid int) string {
	if r.resolvePID == nil {
		return ""
	}
	return r.resolvePID(pid)
}
