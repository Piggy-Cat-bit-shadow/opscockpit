// Command opscockpit is the OpsCockpit CLI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/opscockpit/opscockpit/internal/collect"
	"github.com/opscockpit/opscockpit/internal/collector/cgroup"
	svc "github.com/opscockpit/opscockpit/internal/collector/config"
	"github.com/opscockpit/opscockpit/internal/collector/host"
	"github.com/opscockpit/opscockpit/internal/collector/listener"
	"github.com/opscockpit/opscockpit/internal/collector/systemd"
	"github.com/opscockpit/opscockpit/internal/lock"
	"github.com/opscockpit/opscockpit/internal/restart"
	"github.com/opscockpit/opscockpit/internal/server"
	"github.com/opscockpit/opscockpit/internal/web"
	"github.com/opscockpit/opscockpit/internal/version"
)

const usageText = `OpsCockpit — lightweight single-host ops dashboard.

Usage:
  opscockpit <command> [flags]

Commands:
  collect   Collect runtime state and write state.json
  serve     Serve the web UI from state.json
  discover  Discover runtime listeners and services
  version   Print version information

Run "opscockpit <command> -h" for command flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "collect":
		err = cmdCollect(os.Args[2:])
	case "serve":
		err = cmdServe(os.Args[2:])
	case "discover":
		err = cmdDiscover(os.Args[2:])
	case "restart-helper":
		err = cmdRestartHelper(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println(version.Info())
		return
	case "help", "-h", "--help":
		fmt.Print(usageText)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func cmdCollect(args []string) error {
	fs := flag.NewFlagSet("collect", flag.ExitOnError)
	servicesPath := fs.String("services", "services.yaml", "path to services.yaml")
	statePath := fs.String("out", "state.json", "path to write state.json")
	cpuInterval := fs.Int("cpu-interval-ms", 100, "CPU sample interval in ms")
	fixtureRoot := fs.String("root", "", "fixture root for /proc and cgroup reads (dev/test; empty = real host)")
	lockPath := fs.String("lock", "/run/opscockpit/collect.lock", "single-flight collect lock path ('' disables)")
	lockWait := fs.Duration("lock-wait", 3*time.Second, "how long to wait for the collect lock")
	_ = fs.Parse(args)

	// Single-flight: at most one collect writes state.json.tmp at a time.
	if *lockPath != "" {
		lk := lock.NewFile(*lockPath)
		if err := lk.Acquire(*lockWait); err != nil {
			return fmt.Errorf("collect lock: %w", err)
		}
		defer lk.Release()
	}

	ctx := context.Background()
	pr := collect.NewProductionRunner()

	hs := host.Source{}
	cs := cgroup.Source{}
	if *fixtureRoot != "" {
		hs = host.FromDir(*fixtureRoot)
		cs = cgroup.FromDir(*fixtureRoot)
		// Fixture mode: map PIDs to services. First from cgroup.procs (master
		// PIDs), then via /proc/<pid>/cgroup so worker PIDs resolve too.
		pidSvc := map[int]string{}
		unitSvc := map[string]string{}
		if cfg, err := svc.Load(*servicesPath); err == nil {
			for _, s := range cfg.Services {
				unit := s.Unit()
				if unit == "" {
					continue
				}
				unitSvc[unit] = s.ID
				rel := "sys/fs/cgroup/system.slice/" + unit
				pids, err := os.ReadFile(filepath.Join(*fixtureRoot, rel, "cgroup.procs"))
				if err != nil {
					continue
				}
				for _, line := range strings.Split(string(pids), "\n") {
					line = strings.TrimSpace(line)
					if line == "" {
						continue
					}
					if pid, err := strconv.Atoi(line); err == nil {
						pidSvc[pid] = s.ID
					}
				}
			}
		}
		pr.SetPIDResolver(collect.BuildPIDResolver(pidSvc, collect.ProcCgroup{Root: *fixtureRoot}, unitSvc))
	}

	res, err := collect.Collect(ctx, pr, collect.Options{
		HostSource:    hs,
		CgroupSource:  cs,
		FixtureRoot:   *fixtureRoot,
		ServicesPath:  *servicesPath,
		StatePath:     *statePath,
		CPUIntervalMs: *cpuInterval,
	})
	if err != nil {
		return err
	}
	fmt.Printf("collected %d services, wrote %s (%d ms)\n", len(res.State.Services), res.Path, res.State.CollectDurationMs)
	return nil
}

func cmdDiscover(args []string) error {
	fs := flag.NewFlagSet("discover", flag.ExitOnError)
	servicesPath := fs.String("services", "services.yaml", "path to services.yaml")
	_ = fs.Parse(args)

	ctx := context.Background()
	pr := collect.NewProductionRunner()

	out, err := pr.SS(ctx)
	if err != nil {
		return fmt.Errorf("run ss: %w", err)
	}
	sockets, _ := listener.Parse(out)
	listener.SortByPort(sockets)

	fmt.Println("Public listeners:")
	for _, s := range sockets {
		if s.Internal {
			continue
		}
		fmt.Printf("  %s/%d  %-8s pid=%d proc=%s\n", s.Protocol, s.Port, s.Address, s.PID, s.Process)
	}
	fmt.Println("Internal listeners:")
	for _, s := range sockets {
		if !s.Internal {
			continue
		}
		fmt.Printf("  %s/%d  %-8s pid=%d proc=%s\n", s.Protocol, s.Port, s.Address, s.PID, s.Process)
	}

	if *servicesPath != "" {
		cfg, err := svc.Load(*servicesPath)
		if err == nil {
			fmt.Printf("\nRegistered services (%d):\n", len(cfg.Services))
			for _, s := range cfg.Services {
				// Uninstantiated template units (foo@.service) are definitions,
				// not runtime services — filter them from discovery output.
				if systemd.IsTemplateUnit(s.Unit()) {
					continue
				}
				fmt.Printf("  %-16s %s\n", s.ID, s.Name)
			}
			fmt.Println("\n(candidate discovery only — services.yaml is never modified automatically)")
		}
	}
	return nil
}

// cmdRestartHelper is the root-only restart executor invoked by serve via
// `sudo -n opscockpit restart-helper --services <path> <id>`. It is the SECOND
// trust boundary: it re-reads the root-owned services.yaml, re-checks
// restart_enabled, and resolves the exact unit/container itself. Nothing from
// the web/state.json is trusted.
func cmdRestartHelper(args []string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("restart-helper must run as root (euid=0)")
	}
	fs := flag.NewFlagSet("restart-helper", flag.ExitOnError)
	servicesPath := fs.String("services", "", "root-owned services.yaml path")
	timeout := fs.Duration("timeout", 20*time.Second, "systemctl/docker restart timeout")
	triggerCollect := fs.String("trigger-collect", "opscockpit-collect.service",
		"systemd unit to start for an immediate collect after restart ('' disables)")
	_ = fs.Parse(args)
	if *servicesPath == "" {
		return fmt.Errorf("restart-helper requires --services path")
	}
	// Exactly one positional service id.
	if fs.NArg() != 1 {
		return fmt.Errorf("restart-helper requires exactly one service id")
	}
	id := fs.Arg(0)

	// Second trust boundary: re-read the root-owned registry and resolve the
	// exact unit/container. Nothing from state.json or the web is trusted.
	kind, target, err := resolveRestartTarget(*servicesPath, id)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	switch kind {
	case "unit":
		if err := runHelperCmd(ctx, "systemctl", "restart", target); err != nil {
			return fmt.Errorf("systemctl restart %s: %w", target, err)
		}
	case "container":
		if err := runHelperCmd(ctx, "docker", "restart", target); err != nil {
			return fmt.Errorf("docker restart %s: %w", target, err)
		}
	default:
		return fmt.Errorf("service %q has no restart target", id)
	}

	// Optional immediate collect trigger (fixed unit name, not user-supplied).
	if *triggerCollect != "" {
		_ = runHelperCmd(ctx, "systemctl", "start", *triggerCollect)
	}
	return nil
}

// resolveRestartTarget re-reads the root-owned registry and returns the exact
// restart target for a service id (unit or container). It is the SECOND trust
// boundary: nothing from state.json or the web is trusted. Returns ("unit", u)
// or ("container", c); ("", "") when the service has no restart target.
//
// The services file must be root-owned and not writable by non-root users;
// otherwise a non-root caller could point the helper at a file that grants an
// arbitrary unit/container and escalate to restarting any service.
func resolveRestartTarget(servicesPath, id string) (kind, target string, err error) {
	if !restart.ServiceIDPattern.MatchString(id) {
		return "", "", fmt.Errorf("invalid service id")
	}
	if err := requireRootOwned(servicesPath); err != nil {
		return "", "", err
	}
	cfg, err := svc.Load(servicesPath)
	if err != nil {
		return "", "", fmt.Errorf("load services config: %w", err)
	}
	s := cfg.ByID(id)
	if s == nil {
		return "", "", fmt.Errorf("unknown service %q", id)
	}
	if !s.RestartEnabled {
		return "", "", fmt.Errorf("restart disabled for service %q", id)
	}
	if s.Unit() != "" {
		return "unit", s.Unit(), nil
	}
	if s.DockerContainer() != "" {
		return "container", s.DockerContainer(), nil
	}
	return "", "", fmt.Errorf("service %q has no restart target (no systemd unit or docker container)", id)
}

// requireRootOwned rejects a services.yaml that a non-root user could have
// written (world-writable, or owned by a non-root uid) — the second boundary
// is only trustworthy when the registry itself is root-protected.
func requireRootOwned(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat services config: %w", err)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("services config %s is group/world-writable; refusing", path)
	}
	return nil
}

// runHelperCmd executes an argv vector directly (no shell) with a timeout and
// capped output. Used only for fixed, allowlisted commands.
func runHelperCmd(ctx context.Context, name string, args ...string) error {
	argv := append([]string{name}, args...)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	raw, err := cmd.CombinedOutput()
	if len(raw) > 4096 {
		raw = raw[:4096]
	}
	if err != nil {
		// Never leak command output (may contain sensitive context).
		return err
	}
	return nil
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	statePath := fs.String("state", "state.json", "path to state.json")
	listenAddr := fs.String("listen", ":8090", "listen address")
	unixPath := fs.String("unix", "", "unix socket path (preferred for production; empty = TCP)")
	socketMode := fs.Uint("unix-mode", 0o660, "unix socket file mode (octal)")
	cooldown := fs.Duration("restart-cooldown", 10*time.Second, "min interval between restarts of the same service")
	restartHelper := fs.String("restart-helper", "", "root helper binary invoked via sudo (e.g. /usr/local/bin/opscockpit)")
	restartServices := fs.String("restart-services", "", "root-owned services.yaml the helper re-reads (second allowlist boundary)")
	restartSudo := fs.Bool("restart-sudo", true, "invoke the helper via sudo -n (production); false for dev")
	_ = fs.Parse(args)

	// Serve reads ONLY state.json for the restart allowlist — never the
	// root-owned services.yaml. The unit names in state.json were resolved by
	// the root collector; serve never parses systemd/Docker/firewall.
	var broker *restart.Broker
	store := server.FileStore{Path: *statePath}
	allowlist := []restart.Entry{}
	if st, _, err := store.LoadState(); err == nil {
		allowlist = restart.EntriesFromStateServices(st.Services)
	} else {
		fmt.Fprintf(os.Stderr, "warning: could not load %s for restart allowlist: %v\n", *statePath, err)
	}

	if *restartServices == "" {
		// No helper configured: the restart API must return an explicit
		// "unavailable", never silently succeed via a mock.
		fmt.Fprintln(os.Stderr, "warning: restart API disabled — set -restart-services (and -restart-helper) to enable")
		broker = restart.NewBrokerCooldown(allowlist, restart.NewUnavailableBackend(), *cooldown)
	} else {
		backend, berr := restart.NewHelperBackend(restart.HelperConfig{
			Helper:       *restartHelper,
			ServicesPath: *restartServices,
			Sudo:         *restartSudo,
		})
		if berr != nil {
			return berr
		}
		broker = restart.NewBrokerCooldown(allowlist, backend, *cooldown)
	}

	srv := server.New(
		store,
		broker,
		web.Handler(),
		web.HasHTML,
		web.Index(),
	)

	var ln net.Listener
	if *unixPath != "" {
		ln, err := listenUnix(*unixPath, os.FileMode(*socketMode))
		if err != nil {
			return err
		}
		defer os.Remove(*unixPath)
		fmt.Printf("serving %s on unix socket %s\n", *statePath, *unixPath)
		httpServer := &http.Server{Handler: srv}
		return serveWithSignal(httpServer, ln)
	}

	ln, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	fmt.Printf("serving %s at http://localhost%s\n", *statePath, *listenAddr)

	httpServer := &http.Server{Handler: srv}
	return serveWithSignal(httpServer, ln)
}

// listenUnix removes any stale socket and creates a fresh listener.
func listenUnix(path string, mode os.FileMode) (net.Listener, error) {
	// Remove a stale socket from a previous run (only if it is a socket).
	if st, err := os.Lstat(path); err == nil {
		if st.Mode()&os.ModeSocket != 0 {
			_ = os.Remove(path)
		} else {
			return nil, fmt.Errorf("unix path %s exists and is not a socket", path)
		}
	}
	if mode == 0 {
		mode = 0o660
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("unix listen: %w", err)
	}
	_ = os.Chmod(path, mode)
	return ln, nil
}

// serveWithSignal runs the HTTP server and shuts down cleanly on SIGINT/SIGTERM.
func serveWithSignal(httpServer *http.Server, ln net.Listener) error {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Println("\nshutting down")
		_ = httpServer.Close()
	}()
	err := httpServer.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
