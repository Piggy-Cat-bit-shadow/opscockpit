// Package collect provides the production Runner that executes real system
// commands, plus the collect orchestration.
package collect

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// RunUnit runs `systemctl show <unit> --property=...`. When
// OPSCOCKPIT_UNIT_DIR is set (fixture mode), it reads a per-unit text file
// (one `Key=value` per line) from that directory instead of running systemctl.
func (r *ProductionRunner) RunUnit(ctx context.Context, unit string, properties []string) (string, error) {
	if d := os.Getenv("OPSCOCKPIT_UNIT_DIR"); d != "" {
		b, err := os.ReadFile(filepath.Join(d, unit))
		if err != nil {
			return "", err
		}
		// systemctl show requires all requested properties; default empties.
		var out strings.Builder
		for _, p := range properties {
			out.WriteString(p)
			out.WriteString("=")
			out.WriteString(lookupKV(string(b), p))
			out.WriteString("\n")
		}
		return out.String(), nil
	}
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

func lookupKV(content, key string) string {
	for _, line := range strings.Split(content, "\n") {
		kv := strings.SplitN(line, "=", 2)
		if len(kv) == 2 && strings.TrimSpace(kv[0]) == key {
			return strings.TrimSpace(kv[1])
		}
	}
	return ""
}

// SS runs `ss -H -lntup`. When OPSCOCKPIT_SS_FILE is set (fixture mode), it
// reads ss output from that file instead of running the real ss. This lets the
// production binary be exercised against fixture data (mockdemo) with no code
// changes to the collect pipeline.
func (r *ProductionRunner) SS(ctx context.Context) (string, error) {
	if f := os.Getenv("OPSCOCKPIT_SS_FILE"); f != "" {
		b, err := os.ReadFile(f)
		return string(b), err
	}
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

// UFWStatus runs `LC_ALL=C ufw status verbose`. When OPSCOCKPIT_UFW_FILE is
// set (fixture mode), it reads from that file instead.
func (r *ProductionRunner) UFWStatus(ctx context.Context) (string, error) {
	if f := os.Getenv("OPSCOCKPIT_UFW_FILE"); f != "" {
		b, err := os.ReadFile(f)
		return string(b), err
	}
	cmd := exec.CommandContext(ctx, "env", "LC_ALL=C", "ufw", "status", "verbose")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// IptablesNat runs `iptables -t nat -S`. When OPSCOCKPIT_NAT_FILE is set
// (fixture mode), it reads from that file instead.
func (r *ProductionRunner) IptablesNat(ctx context.Context) (string, error) {
	if f := os.Getenv("OPSCOCKPIT_NAT_FILE"); f != "" {
		b, err := os.ReadFile(f)
		return string(b), err
	}
	cmd := exec.CommandContext(ctx, "iptables", "-t", "nat", "-S")
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
