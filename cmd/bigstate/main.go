// Command bigstate generates a large state.json matching the real VPS scale
// (19 services, ~48 topology nodes, ~59 edges) for frontend verification.
// It reuses the real topology generator. Dev/test only.
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
	fmt.Printf("wrote big mock %s\n", *out)
}

// 19 services mirroring a real VPS.
func services() []svc.Service {
	return []svc.Service{
		{ID: "nginx", Name: "Nginx", StatusHint: state.StatusHealthy},
		{ID: "hysteria2", Name: "Hysteria2", StatusHint: state.StatusHealthy},
		{ID: "sing-box", Name: "sing-box", StatusHint: state.StatusHealthy},
		{ID: "tuic", Name: "TUIC", StatusHint: state.StatusHealthy},
		{ID: "shadowtls", Name: "ShadowTLS v3", StatusHint: state.StatusHealthy},
		{ID: "snell", Name: "Snell v5", StatusHint: state.StatusHealthy},
		{ID: "xray", Name: "Xray", StatusHint: state.StatusHealthy},
		{ID: "adguard-home", Name: "AdGuard Home", StatusHint: state.StatusHealthy},
		{ID: "rustdesk-relay", Name: "RustDesk Relay", StatusHint: state.StatusHealthy},
		{ID: "wireguard", Name: "WireGuard", StatusHint: state.StatusHealthy},
		{ID: "tailscale", Name: "Tailscale", StatusHint: state.StatusHealthy},
		{ID: "cloudflared", Name: "cloudflared", StatusHint: state.StatusHealthy},
		{ID: "gost", Name: "gost", StatusHint: state.StatusHealthy},
		{ID: "iptables-nat", Name: "iptables NAT setup", StatusHint: state.StatusHealthy},
		{ID: "ufw-init", Name: "UFW init", StatusHint: state.StatusHealthy},
		{ID: "cron", Name: "cron", StatusHint: state.StatusHealthy},
		{ID: "journald", Name: "journald", StatusHint: state.StatusHealthy},
		{ID: "sshd", Name: "sshd", StatusHint: state.StatusHealthy},
		{ID: "docker", Name: "docker", StatusHint: state.StatusHealthy},
	}
}

func listeners() []topology.Listener {
	out := []topology.Listener{
		{ServiceID: "nginx", Protocol: "tcp", Port: 443, Address: "0.0.0.0", Exposure: state.ExposureDirectPublic},
		{ServiceID: "hysteria2", Protocol: "udp", Port: 443, Address: "::", Exposure: state.ExposureNATIngress},
		{ServiceID: "nginx", Protocol: "tcp", Port: 853, Address: "0.0.0.0", Exposure: state.ExposureDirectPublic},
		{ServiceID: "nginx", Protocol: "udp", Port: 853, Address: "0.0.0.0", Exposure: state.ExposureDirectPublic},
		{ServiceID: "nginx", Protocol: "tcp", Port: 8443, Address: "0.0.0.0", Exposure: state.ExposureDirectPublic},
		{ServiceID: "sing-box", Protocol: "udp", Port: 8443, Address: "::", Exposure: state.ExposureNATIngress},
		{ServiceID: "shadowtls", Protocol: "tcp", Port: 8554, Address: "0.0.0.0", Exposure: state.ExposureDirectPublic},
		{ServiceID: "rustdesk-relay", Protocol: "tcp", Port: 21115, Address: "0.0.0.0", Exposure: state.ExposureDirectPublic},
		{ServiceID: "rustdesk-relay", Protocol: "tcp", Port: 21116, Address: "0.0.0.0", Exposure: state.ExposureDirectPublic},
		{ServiceID: "wireguard", Protocol: "udp", Port: 51820, Address: "0.0.0.0", Exposure: state.ExposureDirectPublic},
		{ServiceID: "gost", Protocol: "tcp", Port: 1080, Address: "0.0.0.0", Exposure: state.ExposureInternal},
		{ServiceID: "xray", Protocol: "tcp", Port: 18444, Address: "127.0.0.1", Internal: true, Exposure: state.ExposureInternal},
		{ServiceID: "snell", Protocol: "udp", Port: 17414, Address: "::", Exposure: state.ExposureNATIngress},
		{ServiceID: "tuic", Protocol: "udp", Port: 9443, Address: "::", Exposure: state.ExposureNATIngress},
	}
	return out
}

func deps() map[string][]topology.Dependency {
	return map[string][]topology.Dependency{
		"nginx": {
			{TargetServiceID: "xray", Source: state.EvidenceNginxProxyPass, Confidence: state.ConfidenceConfigured},
			{TargetServiceID: "adguard-home", Source: state.EvidenceNginxProxyPass, Confidence: state.ConfidenceConfigured},
		},
		"shadowtls": {
			{TargetServiceID: "snell", Source: state.EvidenceManualOverride, Confidence: state.ConfidenceConfigured},
		},
		"hysteria2": {
			{TargetServiceID: "sing-box", Source: state.EvidenceManualOverride, Confidence: state.ConfidenceConfigured},
		},
	}
}

func run(out string) error {
	// Topology input: direct public + NAT ranges.
	natIngresses := []topology.NATIngress{
		{Protocol: "udp", PortStart: 20000, PortEnd: 20099, TargetPort: 443, ServiceID: "hysteria2"},
		{Protocol: "udp", PortStart: 20100, PortEnd: 20199, TargetPort: 8443, ServiceID: "sing-box"},
		{Protocol: "udp", PortStart: 8555, PortEnd: 8555, TargetPort: 17414, ServiceID: "snell"},
	}

	tp, err := topology.Generate(topology.Input{
		Services:     services(),
		Listeners:    listeners(),
		NATIngresses: natIngresses,
		Dependencies: deps(),
	}, topology.Options{IncludeInternetRoot: true})
	if err != nil {
		return err
	}

	st := state.New("bigmock")
	st.Host = state.Host{
		Hostname:      "prod-vps",
		UptimeSeconds: 86400 * 120,
		CPU:           state.CPUInfo{Cores: 4, Percent: 11.2},
		Memory:        state.MemInfo{Total: 8 << 30, Used: 2 << 30, Percent: 25},
		Swap:          state.MemInfo{Total: 0, Used: 0, Percent: 0},
		Disk:          state.DiskInfo{MountPoint: "/", Total: 100 << 30, Used: 40 << 30, Percent: 40},
		Load:          state.LoadInfo{Load1: 0.3, Load5: 0.2, Load15: 0.2},
	}
	for _, s := range services() {
		svcState := state.Service{ID: s.ID, Name: s.Name, Status: state.StatusHealthy, RestartEnabled: true}
		if s.ID != "iptables-nat" && s.ID != "ufw-init" && s.ID != "cron" && s.ID != "journald" {
			svcState.Unit = s.ID + ".service"
			svcState.UnitState = "running"
			svcState.Memory = &state.MemoryInfo{RSSBytes: int64(8 + len(s.ID)) << 20, Source: "cgroup_memory_current"}
			svcState.Version = "v1.0"
		} else {
			svcState.Unit = s.ID + ".service"
			svcState.UnitState = "exited"
		}
		// Attach listeners from the topology listeners.
		for _, l := range listeners() {
			if l.ServiceID == s.ID {
				svcState.Listeners = append(svcState.Listeners, state.Listener{
					Protocol: l.Protocol, Port: l.Port, Address: l.Address, Internal: l.Internal, Exposure: l.Exposure,
				})
			}
		}
		st.Services = append(st.Services, svcState)
	}
	st.Topology = tp
	st.FinalizeHealth(5 * time.Minute)
	st.CollectDurationMs = 42

	if err := state.WriteAtomic(out, st, state.WriteAtomicOptions{FSync: false}); err != nil {
		return err
	}
	fmt.Printf("nodes=%d edges=%d services=%d\n", len(tp.Nodes), len(tp.Edges), len(st.Services))
	return nil
}
