// Package collect orchestrates all collectors into a single state.State and
// writes it atomically to state.json.
//
// Runtime truth wins: listeners and unit state come from the live host (or a
// fixture root in tests), then services.yaml overrides names, units, config
// paths, version commands and restart permission.
package collect

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opscockpit/opscockpit/internal/collector/cgroup"
	"github.com/opscockpit/opscockpit/internal/collector/deps"
	"github.com/opscockpit/opscockpit/internal/collector/firewall"
	"github.com/opscockpit/opscockpit/internal/collector/host"
	"github.com/opscockpit/opscockpit/internal/collector/listener"
	"github.com/opscockpit/opscockpit/internal/collector/nat"
	"github.com/opscockpit/opscockpit/internal/collector/network"
	"github.com/opscockpit/opscockpit/internal/collector/nginx"
	"github.com/opscockpit/opscockpit/internal/collector/systemd"
	svc "github.com/opscockpit/opscockpit/internal/collector/config"
	"github.com/opscockpit/opscockpit/internal/state"
	"github.com/opscockpit/opscockpit/internal/topology"
)

// Runner abstracts the systemd + ss + version + firewall + nat + network
// command execution. It is the seam tests mock so CI never needs a real host.
type Runner interface {
	systemd.Runner
	// SS returns `ss -H -lntup` output (or fixture text).
	SS(ctx context.Context) (string, error)
	// VersionCommand runs a version argv and returns output.
	Version(ctx context.Context, argv []string) (string, error)
	// UFWStatus returns `LC_ALL=C ufw status verbose` output (or fixture text).
	UFWStatus(ctx context.Context) (string, error)
	// IptablesNat returns `iptables -t nat -S` output (or fixture text).
	IptablesNat(ctx context.Context) (string, error)
	// IPAddrJSON returns `ip -j addr show` output (or fixture text).
	IPAddrJSON(ctx context.Context) (string, error)
	// IPRouteJSON returns `ip -j route show` output (or fixture text).
	IPRouteJSON(ctx context.Context) (string, error)
	// NginxT returns `nginx -T` output (or fixture text). Empty/nil is fine —
	// nginx is optional and its absence never fails a collect.
	NginxT(ctx context.Context) (string, error)
	// DockerPS returns `docker ps -a --format ...` output (or fixture text).
	// Empty/nil is fine — Docker is optional.
	DockerPS(ctx context.Context) (string, error)
}

// Options configures a collection run.
type Options struct {
	// HostSource is the pretend "/" for /proc reads. Empty uses the real root.
	HostSource host.Source
	// CgroupSource is the pretend "/" for cgroup reads. Empty uses the real root.
	CgroupSource cgroup.Source
	// FixtureRoot, when set, prefixes config path existence checks (mock mode).
	FixtureRoot string
	// CPUIntervalMs controls the two-sample CPU window.
	CPUIntervalMs int
	// ServicesPath is the services.yaml path ("" skips registry).
	ServicesPath string
	// StatePath is where state.json is written ("" skips writing).
	StatePath string
	// ConfigFS is an optional override for config-exists checks.
	ConfigExists func(path string) bool
}

// Result is the outcome of a collect run.
type Result struct {
	State *state.State
	Path  string
}

// Collect runs the full pipeline.
func Collect(ctx context.Context, r Runner, opts Options) (Result, error) {
	start := time.Now()

	var servicesCfg *svc.Config
	if opts.ServicesPath != "" {
		cfg, err := svc.Load(opts.ServicesPath)
		if err != nil {
			return Result{}, fmt.Errorf("load services config: %w", err)
		}
		servicesCfg = cfg
	}

	st := state.New(collectorVersion())

	// --- host ---
	if h, err := host.Collect(opts.HostSource, opts.CPUIntervalMs); err == nil {
		st.Host = state.Host{
			Hostname:      h.Hostname,
			UptimeSeconds: h.UptimeSeconds,
			CPU:           state.CPUInfo{Cores: h.CPU.Cores, Percent: h.CPU.Percent},
			Memory:        state.MemInfo{Total: h.Memory.Total, Used: h.Memory.Used, Percent: h.Memory.Percent},
			Swap:          state.MemInfo{Total: h.Swap.Total, Used: h.Swap.Used, Percent: h.Swap.Percent},
			Disk:          state.DiskInfo{MountPoint: h.Disk.MountPoint, Total: h.Disk.Total, Used: h.Disk.Used, Percent: h.Disk.Percent},
			Load:          state.LoadInfo{Load1: h.Load.Load1, Load5: h.Load.Load5, Load15: h.Load.Load15},
		}
	}

	// --- listeners ---
	ssOut, ssErr := r.SS(ctx)
	sockets := []listener.Socket{}
	if ssErr == nil {
		sockets, _ = listener.Parse(ssOut)
	}
	listener.SortByPort(sockets)

	// --- firewall + NAT + network exposure evidence ---
	fw := firewall.Collect(ctx, r)
	hostNet := network.Collect(ctx, r)
	natStatus := nat.Collect(ctx, r, hostNet)

	// --- services ---
	// Map listener → service via PID → cgroup → systemd unit is done by the
	// caller's Runner in production; this collect step accepts a prebuilt
	// mapping from the Runner through the resolve hook below. For this phase
	// the Runner is expected to implement ResolveServiceID.
	svcResolver, _ := r.(interface{ ResolveServiceID(pid int) string })
	svcSockets := make([]listener.Socket, 0, len(sockets))
	for _, s := range sockets {
		if svcResolver != nil {
			if id := svcResolver.ResolveServiceID(s.PID); id != "" {
				s.ServiceID = id
			}
		}
		svcSockets = append(svcSockets, s)
	}
	// Normalize: Nginx workers / reuseport / IPv4+IPv6 any-binds collapse into
	// one logical listener per (proto,addr,port,service), with process_count.
	svcSockets = listener.Normalize(svcSockets)

	// --- service entries ---
	if servicesCfg != nil {
		for _, s := range servicesCfg.Services {
			entry := state.Service{
				ID:             s.ID,
				Name:           s.Name,
				Unit:           s.Unit(),
				RestartEnabled: s.RestartEnabled,
				Status:         state.StatusUnknown,
			}
			if p := s.FirstConfigPath(); p != "" {
				// Canonicalize: require absolute, clean . / .., resolve symlinks.
				canon := svc.ResolveConfigPath(p)
				entry.ConfigPath = canon
				entry.ConfigExists = configExists(opts, canon)
			}
			// Version (best effort; failure → "" = unknown, never a fault).
			if argv := s.VersionCommand(); len(argv) > 0 {
				if out, verr := r.Version(ctx, argv); verr == nil {
					entry.Version = firstLine(out)
				}
			}
			st.Services = append(st.Services, entry)
		}
	}

	// --- per-service runtime facts + topology input ---
	// Map service id → index in st.Services.
	svcIndex := map[string]int{}
	for i := range st.Services {
		svcIndex[st.Services[i].ID] = i
	}

	topoListeners := []topology.Listener{}
	depEdges := map[string][]topology.Dependency{}
	unitStateByID := map[string]systemd.UnitStatus{}
	activeByID := map[string]bool{}
	requiredListenersByID := map[string][]string{}
	natIngresses := []topology.NATIngress{}

	// exposureOverride returns the services.yaml exposure hint for a service.
	exposureOverride := func(id string) *svc.Service {
		if servicesCfg == nil {
			return nil
		}
		return servicesCfg.ByID(id)
	}

	for _, sk := range svcSockets {
		idx, ok := svcIndex[sk.ServiceID]
		if !ok {
			continue
		}
		entry := &st.Services[idx]
		svcCfg := exposureOverride(sk.ServiceID)

		// Classify exposure. A wildcard bind is only a binding scope — whether
		// it is reachable depends on firewall evidence + overrides. IPv4 and
		// IPv6 are judged separately against the host's actual addresses.
		exposure := classifyExposure(sk, fw, hostNet, svcCfg)

		entry.Listeners = append(entry.Listeners, state.Listener{
			Protocol: sk.Protocol,
			Port:     sk.Port,
			Address:  sk.Address,
			Internal: sk.Internal,
			PID:      sk.PID,
			Process:  sk.Process,
			Exposure: exposure,
		})
		topoListeners = append(topoListeners, topology.Listener{
			ServiceID: sk.ServiceID,
			Protocol:  sk.Protocol,
			Port:      sk.Port,
			Address:   sk.Address,
			Internal:  sk.Internal,
			PID:       sk.PID,
			Process:   sk.Process,
			Exposure:  exposure,
		})
	}

	// Build NAT ingress → registered service. A public REDIRECT whose target
	// listener belongs to a registered service becomes a top-level range node.
	// The target listener itself is marked nat_ingress and suppressed as a
	// top-level port unless the service forces direct exposure.
	natTargetByKey := map[string]string{} // "proto:port" → service id
	for _, l := range topoListeners {
		natTargetByKey[fmt.Sprintf("%s:%d", l.Protocol, l.Port)] = l.ServiceID
	}

	for _, ing := range natStatus.PublicRedirects() {
		targetKey := fmt.Sprintf("%s:%d", ing.Protocol, ing.TargetPort)
		svcID, ok := natTargetByKey[targetKey]
		if !ok {
			// No registered service listens on the target; nothing to expose.
			continue
		}
		natIngresses = append(natIngresses, topology.NATIngress{
			Protocol:   ing.Protocol,
			PortStart:  ing.SourcePortStart,
			PortEnd:    ing.SourcePortEnd,
			TargetPort: ing.TargetPort,
			ServiceID:  svcID,
		})

		// Mark the target listener as nat_ingress (so it won't also render as a
		// direct top-level port) unless the service forces direct exposure.
		svcCfg := exposureOverride(svcID)
		if svcCfg != nil && (svcCfg.ForceDirectPublic() || svcCfg.ExposureMode() == "public") {
			continue
		}
		for i := range topoListeners {
			l := &topoListeners[i]
			if l.ServiceID == svcID && l.Protocol == ing.Protocol && l.Port == ing.TargetPort {
				if l.Exposure == state.ExposureDirectPublic {
					l.Exposure = state.ExposureNATIngress
				}
			}
		}
		for i := range st.Services {
			s := &st.Services[i]
			if s.ID == svcID {
				for j := range s.Listeners {
					ll := &s.Listeners[j]
					if ll.Protocol == ing.Protocol && ll.Port == ing.TargetPort && ll.Exposure == state.ExposureDirectPublic {
						ll.Exposure = state.ExposureNATIngress
					}
				}
			}
		}
	}

	// Unit state + memory for services with a systemd unit.
	for i := range st.Services {
		entry := &st.Services[i]
		unit := entry.Unit
		if unit == "" {
			continue
		}
		// Uninstantiated template units (foo@.service) are not runtime services.
		if systemd.IsTemplateUnit(unit) {
			continue
		}
		us, err := systemd.ShowUnit(ctx, r, unit)
		if err != nil {
			continue
		}
		entry.UnitState = us.ActiveState
		active := us.IsHealthyActive()
		activeByID[entry.ID] = active
		unitStateByID[entry.ID] = us

		if us.ControlGroup != "" {
			if res, cerr := opts.CgroupSource.MemoryForControlGroup(us.ControlGroup); cerr == nil {
				entry.Memory = &state.MemoryInfo{RSSBytes: res.RSSBytes, Source: res.Source}
			}
		}
		if entry.Memory == nil && us.MainPID > 0 {
			if res, cerr := opts.CgroupSource.MemoryForMainPID(us.MainPID); cerr == nil {
				entry.Memory = &state.MemoryInfo{RSSBytes: res.RSSBytes, Source: res.Source}
			}
		}

		if servicesCfg != nil {
			if s := servicesCfg.ByID(entry.ID); s != nil && s.Health != nil {
				for _, req := range s.Health.RequiredListeners {
					if !hasListener(entry.Listeners, req.Port, req.Protocol) {
						requiredListenersByID[entry.ID] = append(
							requiredListenersByID[entry.ID],
							fmt.Sprintf("%s/%d", req.Protocol, req.Port),
						)
					}
				}
			}
		}
	}

	// --- Dependency resolution (best effort, never fatal). Runs after unit
	// state so declared upstream endpoints can read ExecStart. Nginx is
	// optional; declared upstreams run regardless. ---
	if ngxOut, err := r.NginxT(ctx); err == nil && ngxOut != "" {
		collectNginxDeps(ctx, r, servicesCfg, topoListeners, st, &depEdges, unitStateByID)
	}
	collectDeclaredUpstreams(servicesCfg, topoListeners, &depEdges, unitStateByID)

	// --- Docker service health (best effort, never fatal). ---
	collectDockerHealth(ctx, r, servicesCfg, st)

	// Final per-service status.
	for i := range st.Services {
		entry := &st.Services[i]
		unit := entry.Unit
		if unit != "" {
			active := activeByID[entry.ID]
			us := unitStateByID[entry.ID]

			// require_listener: does this service's health depend on having a
			// listener at all? auto defers to unit semantics (oneshot → no).
			reqListener := requireListenerFor(servicesCfg, entry.ID, us.Type)

			var missing []string
			if reqListener {
				missing = requiredListenersByID[entry.ID]
				if len(entry.Listeners) == 0 {
					missing = append(missing, "no listener for registered service")
				}
			}

			status, problems := state.ServiceStatus(active, us.ActiveState, missing, configMissing(opts, entry))
			entry.Status = status
			if len(problems) > 0 {
				entry.Health = &state.HealthInfo{Problems: problems}
			}
		} else {
			// No unit configured: keep unknown unless a listener exists.
			if len(entry.Listeners) > 0 {
				entry.Status = state.StatusHealthy
			}
		}
	}

	// Build topology from runtime listeners + registry, with the runtime
	// statuses as hints.
	topoServices := make([]svc.Service, 0, len(st.Services))
	for _, e := range st.Services {
		topoServices = append(topoServices, svc.Service{
			ID:         e.ID,
			Name:       e.Name,
			StatusHint: e.Status,
		})
	}
	tp, terr := topology.Generate(topology.Input{
		Services:     topoServices,
		Listeners:    topoListeners,
		NATIngresses: natIngresses,
		Dependencies: depEdges,
	}, topology.Options{IncludeInternetRoot: true})
	if terr != nil {
		return Result{}, fmt.Errorf("topology: %w", terr)
	}
	st.Topology = tp

	st.FinalizeHealth(2 * time.Minute)
	st.CollectDurationMs = time.Since(start).Milliseconds()

	// Atomic write.
	res := Result{State: st}
	if opts.StatePath != "" {
		if err := state.WriteAtomic(opts.StatePath, st, state.WriteAtomicOptions{FSync: true}); err != nil {
			return Result{}, fmt.Errorf("write state: %w", err)
		}
		res.Path = opts.StatePath
	}
	return res, nil
}

func configMissing(opts Options, entry *state.Service) bool {
	if entry.ConfigPath == "" {
		return false
	}
	return !configExists(opts, entry.ConfigPath)
}

// requireListenerFor resolves health.require_listener (auto/true/false) for a
// service. auto defers to unit semantics: oneshot apply-rule units do not
// require a listener; network daemons (simple/forking/etc) do.
func requireListenerFor(servicesCfg *svc.Config, id, unitType string) bool {
	if servicesCfg != nil {
		if s := servicesCfg.ByID(id); s != nil {
			switch s.RequireListener() {
			case "true":
				return true
			case "false":
				return false
			}
		}
	}
	// auto: oneshot units are apply-rule style, no listener required.
	return unitType != "oneshot"
}

// collectNginxDeps parses nginx -T and adds service dependencies via the
// endpoint resolver. Uses the shared deps.Graph for cycle detection and a depth
// bound. Static-resource targets (a directory) terminate the chain and never
// invent a service.
func collectNginxDeps(ctx context.Context, r Runner, servicesCfg *svc.Config, topoListeners []topology.Listener, st *state.State, out *map[string][]topology.Dependency, unitState map[string]systemd.UnitStatus) {
	ngxOut, err := r.NginxT(ctx)
	if err != nil || ngxOut == "" {
		return
	}
	cfg := nginx.Parse(ngxOut)
	if len(cfg.ProxyPasses) == 0 {
		return
	}

	// Build the endpoint resolver from all listeners that belong to a
	// registered service.
	known := make([]deps.KnownListener, 0, len(topoListeners))
	for _, l := range topoListeners {
		known = append(known, deps.KnownListener{
			Host:      l.Address,
			Port:      l.Port,
			ServiceID: l.ServiceID,
			Loopback:  l.Internal,
		})
	}
	resolver := deps.NewResolver(known)

	graph := deps.NewGraph(5, 200)

	// Map server listen port → owning service id.
	portOwner := map[int]string{}
	for _, l := range topoListeners {
		if _, ok := portOwner[l.Port]; !ok {
			portOwner[l.Port] = l.ServiceID
		}
	}

	// Resolve each proxy_pass to concrete endpoints.
	endpointsByServer := cfg.ResolveProxyTargets()
	for serverPort, endpoints := range endpointsByServer {
		owner := portOwner[serverPort]
		if owner == "" {
			continue
		}
		for _, ep := range endpoints {
			svcID := resolver.Resolve(ep)
			if svcID == "" {
				// Static dirs / unresolved endpoints terminate the chain.
				continue
			}
			graph.AddEdge(owner, svcID, state.EvidenceNginxProxyPass, state.ConfidenceConfigured, ep)
		}
	}

	// Emit edges into the topology dependencies map.
	for src, ds := range graph.Edges {
		for _, d := range ds {
			(*out)[src] = append((*out)[src], topology.Dependency{
				TargetServiceID: d.TargetServiceID,
				Source:          d.Source,
				Confidence:      d.Confidence,
			})
		}
	}
}

// collectDockerHealth merges Docker container health into services that are
// Docker-backed (services.yaml docker.container). Docker is optional; on any
// error it is skipped and never fails a collect.
//
// Health mapping:
//   - container stopped → failed
//   - running + unhealthy → warning
//   - running + starting → unknown
//   - running + healthy (or no HEALTHCHECK) → not a fault
func collectDockerHealth(ctx context.Context, r Runner, servicesCfg *svc.Config, st *state.State) {
	if servicesCfg == nil {
		return
	}
	out, err := r.DockerPS(ctx)
	if err != nil || out == "" {
		return
	}
	containers := dockerExecClient(r).ListFromPS(out)

	// Map container name → health.
	byName := map[string]dockerContainerHealth{}
	for _, c := range containers {
		byName[c.Name] = dockerContainerHealth{Running: c.Running, Health: c.Health}
	}

	for i := range st.Services {
		s := st.Services[i]
		cfg := servicesCfg.ByID(s.ID)
		if cfg == nil || cfg.DockerContainer() == "" {
			continue
		}
		h, ok := byName[cfg.DockerContainer()]
		if !ok {
			continue
		}
		switch {
		case !h.Running:
			st.Services[i].Status = state.StatusFailed
			st.Services[i].Health = &state.HealthInfo{Problems: []string{"container not running"}}
		case h.Health == "unhealthy":
			st.Services[i].Status = state.StatusWarning
			st.Services[i].Health = &state.HealthInfo{Problems: []string{"container unhealthy"}}
		case h.Health == "starting":
			st.Services[i].Status = state.StatusUnknown
			st.Services[i].Health = &state.HealthInfo{Problems: []string{"container starting"}}
		default:
			// healthy or no HEALTHCHECK → not a fault.
			if st.Services[i].Status == state.StatusUnknown {
				st.Services[i].Status = state.StatusHealthy
			}
		}
	}
}

// dockerContainerHealth is a minimal container health snapshot.
type dockerContainerHealth struct {
	Name    string
	Running bool
	Health  string
}

// dockerExecClient adapts a Runner to the docker ExecClient command interface.
func dockerExecClient(r Runner) dockerClientLike { return runnerDocker{r: r} }

// dockerClientLike is the subset of the docker client the collect layer needs.
type dockerClientLike interface {
	ListFromPS(ps string) []dockerContainerHealth
}

// runnerDocker adapts a collect Runner's DockerPS output into container facts.
type runnerDocker struct{ r Runner }

func (rd runnerDocker) ListFromPS(ps string) []dockerContainerHealth {
	var out []dockerContainerHealth
	for _, line := range strings.Split(strings.TrimSpace(ps), "\n") {
		if line == "" {
			continue
		}
		// Format: ID|Names|Image|Status  (no health column — parsed from Status).
		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 4 {
			continue
		}
		name := strings.TrimSpace(parts[1])
		status := strings.TrimSpace(parts[3])
		out = append(out, dockerContainerHealth{
			Name:    name,
			Running: strings.HasPrefix(status, "Up "),
			Health:  dockerHealthFromStatus(status),
		})
	}
	return out
}

// dockerHealthFromStatus parses the Docker health indicator out of a Status
// string. Common formats:
//
//	Up 2 hours
//	Up 2 hours (healthy)
//	Up 2 hours (unhealthy)
//	Up 2 hours (health: starting)
//
// A running container with no health marker has no HEALTHCHECK → health ""
// (not a warning). Parsing is conservative; unknown markers → "".
func dockerHealthFromStatus(status string) string {
	if strings.Contains(status, "(healthy)") {
		return "healthy"
	}
	if strings.Contains(status, "(unhealthy)") {
		return "unhealthy"
	}
	if strings.Contains(status, "(health: starting)") || strings.Contains(status, "(starting)") {
		return "starting"
	}
	return ""
}

// collectDeclaredUpstreams resolves services.yaml topology.upstream_from
// (exec_arg flag) endpoints to downstream services. Runs regardless of nginx.
func collectDeclaredUpstreams(servicesCfg *svc.Config, topoListeners []topology.Listener, out *map[string][]topology.Dependency, unitState map[string]systemd.UnitStatus) {
	if servicesCfg == nil {
		return
	}
	// Build the endpoint resolver.
	known := make([]deps.KnownListener, 0, len(topoListeners))
	for _, l := range topoListeners {
		known = append(known, deps.KnownListener{Host: l.Address, Port: l.Port, ServiceID: l.ServiceID, Loopback: l.Internal})
	}
	resolver := deps.NewResolver(known)
	graph := deps.NewGraph(5, 200)

	for _, s := range servicesCfg.Services {
		if s.Topology == nil {
			continue
		}
		execStart := ""
		if us, ok := unitState[s.ID]; ok {
			execStart = us.ExecStart
		}
		for _, src := range s.Topology.UpstreamFrom {
			if src.Flag == "" {
				continue
			}
			ep := declaredEndpointFromExecStart(execStart, src.Flag)
			if ep == "" {
				continue
			}
			if target := resolver.Resolve(ep); target != "" {
				graph.AddEdge(s.ID, target, state.EvidenceManualOverride, state.ConfidenceConfigured, ep)
			}
		}
	}

	for src, ds := range graph.Edges {
		for _, d := range ds {
			(*out)[src] = append((*out)[src], topology.Dependency{
				TargetServiceID: d.TargetServiceID,
				Source:          d.Source,
				Confidence:      d.Confidence,
			})
		}
	}
}

func configExists(opts Options, path string) bool {
	if opts.ConfigExists != nil {
		return opts.ConfigExists(path)
	}
	if opts.FixtureRoot != "" {
		_, err := os.Stat(filepath.Join(opts.FixtureRoot, path))
		return err == nil
	}
	_, err := os.Stat(path)
	return err == nil
}

func hasListener(listeners []state.Listener, port int, protocol string) bool {
	for _, l := range listeners {
		if l.Port == port && l.Protocol == protocol && !l.Internal {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}

func collectorVersion() string {
	return "dev"
}

// allServices returns the registry services (empty if none).
func allServices(cfg *svc.Config) []svc.Service {
	if cfg == nil {
		return nil
	}
	return cfg.Services
}

// declaredEndpointFromExecStart reads a specific flag's value from an
// ExecStart string — the ONLY safe extraction of a declared upstream endpoint.
// The full ExecStart is never stored or logged. Returns "" when the flag is
// absent or the value is not a plausible endpoint.
func declaredEndpointFromExecStart(execStart, flag string) string {
	if execStart == "" || flag == "" {
		return ""
	}
	fields := strings.Fields(execStart)
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == flag {
			val := strings.TrimSuffix(fields[i+1], ";")
			// Only accept a plausible host:port endpoint — never a raw secret
			// or flag value that isn't an endpoint.
			if strings.Contains(val, ":") {
				return val
			}
			return ""
		}
	}
	return ""
}

// classifyExposure decides a listener's exposure classification.
//
// Rules (highest priority first):
//  1. services.yaml exposure.mode=internal  → internal (override always wins)
//  2. services.yaml exposure.mode=public    → direct_public (override)
//  3. loopback bind (127.0.0.1, ::1)        → internal
//  4. firewall active + explicit public allow for (proto, port) → direct_public
//  5. firewall active + allow but restricted/private source → internal
//  6. firewall active + default deny, no allow            → internal (filtered)
//  7. firewall unknown/inactive, no override              → unknown
//
// Per-family (IPv4/IPv6): an IPv6 wildcard bind ([::]) plus an IPv6 UFW rule
// is NOT a real public service unless the host actually has a global IPv6
// address and a usable IPv6 route. Same logic for IPv4.
func classifyExposure(s listener.Socket, fw firewall.Status, hostNet network.Identity, svcCfg *svc.Service) string {
	// Overrides first.
	if svcCfg != nil {
		switch svcCfg.ExposureMode() {
		case "internal":
			return state.ExposureInternal
		case "public":
			return state.ExposureDirectPublic
		}
	}

	// Loopback is always internal.
	if s.Internal {
		return state.ExposureInternal
	}

	// Per-family reachability: does the host actually have the address family?
	family := "ipv4"
	if s.Address == "::" || strings.Contains(s.Address, ":") {
		family = "ipv6"
	}
	// The socket must bind on an address family the host actually has a global
	// address + default route for; otherwise it is not publicly reachable.
	if !hostNet.HasAddressFamily(family) || !hostNet.HasDefaultRoute(family) {
		// The host has no usable global address/route for this family — a
		// wildcard bind on it cannot be a real public service.
		return state.ExposureInternal
	}

	// Firewall evidence, public-source only.
	switch fw.Visibility {
	case firewall.VisibilityActive:
		if fw.IsPubliclyAllowed(s.Protocol, s.Port) {
			return state.ExposureDirectPublic
		}
		// Allowed but restricted/private source, or not allowed at all under a
		// default-deny policy: filtered.
		return state.ExposureInternal
	default:
		// Firewall inactive/missing/unparseable: we cannot claim public.
		return state.ExposureUnknown
	}
}
