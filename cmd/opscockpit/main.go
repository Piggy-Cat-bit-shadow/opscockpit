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

	"github.com/opscockpit/opscockpit/internal/collect"
	"github.com/opscockpit/opscockpit/internal/collector/cgroup"
	svc "github.com/opscockpit/opscockpit/internal/collector/config"
	"github.com/opscockpit/opscockpit/internal/collector/host"
	"github.com/opscockpit/opscockpit/internal/collector/listener"
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
	_ = fs.Parse(args)

	ctx := context.Background()
	pr := collect.NewProductionRunner()

	hs := host.Source{}
	cs := cgroup.Source{}
	if *fixtureRoot != "" {
		hs = host.FromDir(*fixtureRoot)
		cs = cgroup.FromDir(*fixtureRoot)
		// Fixture mode: map PIDs to services by scanning cgroup.procs files.
		pidSvc := map[int]string{}
		if cfg, err := svc.Load(*servicesPath); err == nil {
			for _, s := range cfg.Services {
				unit := s.Unit()
				if unit == "" {
					continue
				}
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
		pr.SetPIDResolver(func(pid int) string { return pidSvc[pid] })
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
				fmt.Printf("  %-16s %s\n", s.ID, s.Name)
			}
		}
	}
	return nil
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	statePath := fs.String("state", "state.json", "path to state.json")
	listenAddr := fs.String("listen", ":8090", "listen address")
	servicesPath := fs.String("services", "services.yaml", "path to services.yaml (for restart allowlist)")
	_ = fs.Parse(args)

	// Load the allowlist for restart.
	broker := restart.NewBroker(nil, restart.NewMock())
	if *servicesPath != "" {
		if cfg, err := svc.Load(*servicesPath); err == nil {
			broker = restart.NewBroker(restart.EntriesFromServices(cfg.Services), restart.NewMock())
		} else {
			fmt.Fprintf(os.Stderr, "warning: could not load %s for restart allowlist: %v\n", *servicesPath, err)
		}
	}

	srv := server.New(
		server.FileStore{Path: *statePath},
		broker,
		web.Handler(),
		web.HasHTML,
		web.Index(),
	)

	ln, err := net.Listen("tcp", *listenAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	fmt.Printf("serving %s at http://localhost%s\n", *statePath, *listenAddr)

	httpServer := &http.Server{Handler: srv}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Println("\nshutting down")
		httpServer.Close()
	}()
	err = httpServer.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
