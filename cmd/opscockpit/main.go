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

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	statePath := fs.String("state", "state.json", "path to state.json")
	listenAddr := fs.String("listen", ":8090", "listen address")
	unixPath := fs.String("unix", "", "unix socket path (preferred for production; empty = TCP)")
	socketMode := fs.Uint("unix-mode", 0o660, "unix socket file mode (octal)")
	cooldown := fs.Duration("restart-cooldown", 10*time.Second, "min interval between restarts of the same service")
	_ = fs.Parse(args)

	// Serve reads ONLY state.json for the restart allowlist — never the
	// root-owned services.yaml. The unit names in state.json were resolved by
	// the root collector; serve never parses systemd/Docker/firewall.
	var broker *restart.Broker
	store := server.FileStore{Path: *statePath}
	if st, _, err := store.LoadState(); err == nil {
		broker = restart.NewBrokerCooldown(restart.EntriesFromStateServices(st.Services), restart.NewMock(), *cooldown)
	} else {
		fmt.Fprintf(os.Stderr, "warning: could not load %s for restart allowlist: %v\n", *statePath, err)
		broker = restart.NewBroker(nil, restart.NewMock())
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
