// Package collect provides the production Runner that executes real system
// commands, plus the collect orchestration.
package collect

import (
	"context"
	"fmt"
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
	// CmdTimeout bounds every non-systemctl external command (ss, ufw,
	// iptables, ip, docker, nginx). Defaults to 3s.
	CmdTimeout time.Duration
	// MaxOutput caps captured stdout+stderr (default 2 MiB) so a runaway
	// command (huge nginx -T / docker / process list) cannot spike RAM.
	MaxOutput int
	// resolvePID maps a PID to a service id using cgroup + unit lookup.
	resolvePID func(pid int) string
}

// NewProductionRunner builds a runner with no PID→service mapping (the
// production mapping is wired by the caller via SetPIDResolver).
func NewProductionRunner() *ProductionRunner {
	return &ProductionRunner{Timeout: 5 * time.Second, CmdTimeout: 3 * time.Second, MaxOutput: 2 << 20}
}

// SetPIDResolver installs the PID → service id mapping function.
func (r *ProductionRunner) SetPIDResolver(fn func(pid int) string) { r.resolvePID = fn }

// runBounded runs argv with a context timeout and caps combined output.
func (r *ProductionRunner) runBounded(ctx context.Context, argv []string, timeout time.Duration) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	if r.MaxOutput > 0 && len(out) > r.MaxOutput {
		out = out[:r.MaxOutput]
	}
	return string(out), err
}

// Run executes an argv vector directly with a bounded timeout and output cap.
func (r *ProductionRunner) Run(ctx context.Context, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", nil
	}
	return r.runBounded(ctx, argv, r.timeout(r.CmdTimeout))
}

func (r *ProductionRunner) timeout(d time.Duration) time.Duration {
	if d <= 0 {
		return 5 * time.Second
	}
	return d
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
	return r.runBounded(ctx, []string{bin, "-H", "-lntup"}, r.timeout(r.CmdTimeout))
}

// Version runs a version argv.
func (r *ProductionRunner) Version(ctx context.Context, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", nil
	}
	return r.runBounded(ctx, argv, r.timeout(r.CmdTimeout))
}

// UFWStatus runs `LC_ALL=C ufw status verbose`. When OPSCOCKPIT_UFW_FILE is
// set (fixture mode), it reads from that file instead.
func (r *ProductionRunner) UFWStatus(ctx context.Context) (string, error) {
	if f := os.Getenv("OPSCOCKPIT_UFW_FILE"); f != "" {
		b, err := os.ReadFile(f)
		return string(b), err
	}
	return r.runBounded(ctx, []string{"env", "LC_ALL=C", "ufw", "status", "verbose"}, r.timeout(r.CmdTimeout))
}

// IptablesNat runs `iptables -t nat -S`. When OPSCOCKPIT_NAT_FILE is set
// (fixture mode), it reads from that file instead.
func (r *ProductionRunner) IptablesNat(ctx context.Context) (string, error) {
	if f := os.Getenv("OPSCOCKPIT_NAT_FILE"); f != "" {
		b, err := os.ReadFile(f)
		return string(b), err
	}
	return r.runBounded(ctx, []string{"iptables", "-t", "nat", "-S"}, r.timeout(r.CmdTimeout))
}

// IPAddrJSON runs `ip -j addr show`. When OPSCOCKPIT_IPADDR_FILE is set
// (fixture mode), it reads from that file instead.
func (r *ProductionRunner) IPAddrJSON(ctx context.Context) (string, error) {
	if f := os.Getenv("OPSCOCKPIT_IPADDR_FILE"); f != "" {
		b, err := os.ReadFile(f)
		return string(b), err
	}
	return r.runBounded(ctx, []string{"ip", "-j", "addr", "show"}, r.timeout(r.CmdTimeout))
}

// IPRouteJSON runs `ip -j route show`. When OPSCOCKPIT_IPROUTE_FILE is set
// (fixture mode), it reads from that file instead.
func (r *ProductionRunner) IPRouteJSON(ctx context.Context) (string, error) {
	if f := os.Getenv("OPSCOCKPIT_IPROUTE_FILE"); f != "" {
		b, err := os.ReadFile(f)
		return string(b), err
	}
	return r.runBounded(ctx, []string{"ip", "-j", "route", "show"}, r.timeout(r.CmdTimeout))
}

// NginxT runs `nginx -T`. When OPSCOCKPIT_NGINX_FILE is set (fixture mode), it
// reads from that file instead. Nginx is optional: errors return "" + err and
// never fail a collect. Slightly longer budget (config can be large).
func (r *ProductionRunner) NginxT(ctx context.Context) (string, error) {
	if f := os.Getenv("OPSCOCKPIT_NGINX_FILE"); f != "" {
		b, err := os.ReadFile(f)
		return string(b), err
	}
	return r.runBounded(ctx, []string{"nginx", "-T"}, 8*time.Second)
}

// DockerPS runs a docker ps listing. When OPSCOCKPIT_DOCKER_PS is set (fixture
// mode), it reads from that file instead. Docker is optional: errors return ""
// + err and never fail a collect. The format includes the health status column.
func (r *ProductionRunner) DockerPS(ctx context.Context) (string, error) {
	if f := os.Getenv("OPSCOCKPIT_DOCKER_PS"); f != "" {
		b, err := os.ReadFile(f)
		return string(b), err
	}
	return r.runBounded(ctx, []string{"docker", "ps", "-a", "--no-trunc",
		"--format", "{{.ID}}|{{.Names}}|{{.Image}}|{{.Status}}|{{.Health}}"}, 8*time.Second)
}

// ResolveServiceID implements the collect hook.
func (r *ProductionRunner) ResolveServiceID(pid int) string {
	if r.resolvePID == nil {
		return ""
	}
	return r.resolvePID(pid)
}

// ProcCgroupReader abstracts /proc/<pid>/cgroup for worker PID→unit mapping.
type ProcCgroupReader interface {
	// CgroupPath returns the cgroup path for a PID (e.g.
	// /system.slice/nginx.service) or "" if unknown.
	CgroupPath(pid int) string
}

// ProcCgroup is the real /proc reader.
type ProcCgroup struct {
	// Root is the pretend "/" for fixtures.
	Root string
}

// CgroupPath reads /proc/<pid>/cgroup and returns the first path line.
func (p ProcCgroup) CgroupPath(pid int) string {
	rel := fmt.Sprintf("proc/%d/cgroup", pid)
	b, err := os.ReadFile(filepath.Join(p.Root, rel))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: "0::/system.slice/nginx.service" or "2:cpu:/foo".
		// Take the path after the last ':' (cgroup v2 path).
		idx := strings.LastIndex(line, ":")
		if idx < 0 {
			continue
		}
		path := strings.TrimSpace(line[idx+1:])
		if path != "" && path != "/" {
			return path
		}
	}
	return ""
}

// UnitFromCgroup maps a cgroup path to a systemd unit name (e.g.
// /system.slice/nginx.service → nginx.service). Template instances
// (foo@abc.service) are returned as their instance name.
func UnitFromCgroup(cgroupPath string) string {
	trimmed := strings.TrimPrefix(cgroupPath, "/")
	seg := strings.Split(trimmed, "/")
	for i := len(seg) - 1; i >= 0; i-- {
		s := seg[i]
		if strings.HasSuffix(s, ".service") {
			return s
		}
	}
	return ""
}

// BuildPIDResolver builds the PID → service id resolver used by the collect
// layer. It prefers the cgroup-based mapping (supports worker PIDs, where a
// socket is owned by a worker process whose cgroup is the same as the unit).
// A direct pidToSvc map (mock/known) takes precedence.
func BuildPIDResolver(pidToSvc map[int]string, proc ProcCgroupReader, unitToSvc map[string]string) func(pid int) string {
	return func(pid int) string {
		if svcID, ok := pidToSvc[pid]; ok {
			return svcID
		}
		if proc == nil {
			return ""
		}
		cg := proc.CgroupPath(pid)
		if cg == "" {
			return ""
		}
		unit := UnitFromCgroup(cg)
		if unit == "" {
			return ""
		}
		return unitToSvc[unit]
	}
}
