// Command mockstate generates a fixture state.json for local development and
// browser smoke tests. It exercises the exact same collect pipeline + topology
// generator as production, fed with mock runtime data. It never touches a real
// host.
//
//   go run ./cmd/mockstate -out state.json
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	svc "github.com/opscockpit/opscockpit/internal/collector/config"
	"github.com/opscockpit/opscockpit/internal/state"
	"github.com/opscockpit/opscockpit/internal/topology"
)

func main() {
	out := flag.String("out", "state.json", "output path")
	flag.Parse()

	if err := run(*out); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("wrote mock %s\n", *out)
}

func run(out string) error {
	// Mock runtime matching the spec testdata:
	//   443/TCP Nginx, 443/UDP Hysteria2, 8443/UDP TUIC,
	//   853/TCP+UDP AdGuard Home, 127.0.0.1:18444 Xray (internal),
	//   Nginx -> Xray dependency.
	services := []svc.Service{
		{ID: "nginx", Name: "Nginx", StatusHint: state.StatusHealthy},
		{ID: "hysteria2", Name: "Hysteria2", StatusHint: state.StatusHealthy},
		{ID: "tuic", Name: "TUIC", StatusHint: state.StatusHealthy},
		{ID: "adguard-home", Name: "AdGuard Home", StatusHint: state.StatusHealthy},
		{ID: "xray", Name: "Xray", StatusHint: state.StatusHealthy},
	}

	listeners := []topology.Listener{
		{ServiceID: "nginx", Protocol: "tcp", Port: 443, Address: "0.0.0.0"},
		{ServiceID: "hysteria2", Protocol: "udp", Port: 443, Address: "::"},
		{ServiceID: "tuic", Protocol: "udp", Port: 8443, Address: "::"},
		{ServiceID: "adguard-home", Protocol: "tcp", Port: 853, Address: "0.0.0.0"},
		{ServiceID: "adguard-home", Protocol: "udp", Port: 853, Address: "0.0.0.0"},
		{ServiceID: "xray", Protocol: "tcp", Port: 18444, Address: "127.0.0.1", Internal: true},
	}

	deps := map[string][]topology.Dependency{
		"nginx": {
			{TargetServiceID: "xray", Source: state.EvidenceNginxProxyPass, Confidence: state.ConfidenceConfigured},
		},
	}

	tp, err := topology.Generate(topology.Input{
		Services:     services,
		Listeners:    listeners,
		Dependencies: deps,
	}, topology.Options{IncludeInternetRoot: true})
	if err != nil {
		return err
	}

	st := state.New("mock")
	st.Host = state.Host{
		Hostname:      "mock-vps",
		UptimeSeconds: 86400 * 3,
		CPU:           state.CPUInfo{Cores: 4, Percent: 12.5},
		Memory:        state.MemInfo{Total: 8 << 30, Used: 2 << 30, Percent: 25},
		Swap:          state.MemInfo{Total: 0, Used: 0, Percent: 0},
		Disk:          state.DiskInfo{MountPoint: "/", Total: 100 << 30, Used: 40 << 30, Percent: 40},
		Load:          state.LoadInfo{Load1: 0.4, Load5: 0.3, Load15: 0.2},
	}
	st.Services = []state.Service{
		{ID: "nginx", Name: "Nginx", Status: state.StatusHealthy, Unit: "nginx.service", UnitState: "running",
			Version: "nginx/1.27.1", Memory: &state.MemoryInfo{RSSBytes: 18 * 1024 * 1024, Source: "proc_rss"},
			ConfigPath: "/etc/nginx/nginx.conf", ConfigExists: true, RestartEnabled: true,
			Listeners: []state.Listener{{Protocol: "tcp", Port: 443, Address: "0.0.0.0"}}},
		{ID: "hysteria2", Name: "Hysteria2", Status: state.StatusHealthy, Unit: "hysteria-server.service", UnitState: "running",
			Version: "Hysteria 2.5.0", Memory: &state.MemoryInfo{RSSBytes: 32 * 1024 * 1024, Source: "cgroup_memory_current"},
			ConfigPath: "/etc/hysteria/config.yaml", ConfigExists: true, RestartEnabled: true,
			Listeners: []state.Listener{{Protocol: "udp", Port: 443, Address: "::"}}},
		{ID: "tuic", Name: "TUIC", Status: state.StatusHealthy, Unit: "tuic.service", UnitState: "running",
			Version: "TUIC v5.1.4", Memory: &state.MemoryInfo{RSSBytes: 12 * 1024 * 1024, Source: "proc_rss"},
			ConfigPath: "/etc/tuic/config.json", ConfigExists: true, RestartEnabled: true,
			Listeners: []state.Listener{{Protocol: "udp", Port: 8443, Address: "::"}}},
		{ID: "adguard-home", Name: "AdGuard Home", Status: state.StatusHealthy, Unit: "adguard.service", UnitState: "running",
			Version: "AdGuard Home v0.107.54", Memory: &state.MemoryInfo{RSSBytes: 48 * 1024 * 1024, Source: "cgroup_memory_current"},
			ConfigPath: "/etc/AdGuardHome/AdGuardHome.yaml", ConfigExists: true, RestartEnabled: true,
			Listeners: []state.Listener{
				{Protocol: "tcp", Port: 853, Address: "0.0.0.0"},
				{Protocol: "udp", Port: 853, Address: "0.0.0.0"},
			}},
		{ID: "xray", Name: "Xray", Status: state.StatusHealthy, Unit: "xray.service", UnitState: "running",
			Version: "Xray 24.9.1", Memory: &state.MemoryInfo{RSSBytes: 24 * 1024 * 1024, Source: "proc_rss"},
			ConfigPath: "/etc/xray/config.json", ConfigExists: true, RestartEnabled: true,
			Listeners: []state.Listener{{Protocol: "tcp", Port: 18444, Address: "127.0.0.1", Internal: true}}},
	}
	st.Topology = tp
	st.FinalizeHealth(5 * time.Minute)
	st.CollectDurationMs = 42

	if err := state.WriteAtomic(out, st, state.WriteAtomicOptions{FSync: false}); err != nil {
		return err
	}
	return nil
}
