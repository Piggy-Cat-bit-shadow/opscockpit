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
	"time"

	"github.com/opscockpit/opscockpit/internal/collector/cgroup"
	"github.com/opscockpit/opscockpit/internal/collector/host"
	"github.com/opscockpit/opscockpit/internal/collector/listener"
	"github.com/opscockpit/opscockpit/internal/collector/systemd"
	svc "github.com/opscockpit/opscockpit/internal/collector/config"
	"github.com/opscockpit/opscockpit/internal/state"
	"github.com/opscockpit/opscockpit/internal/topology"
)

// Runner abstracts the systemd + ss + version command execution. It is the
// seam tests mock so CI never needs a real host.
type Runner interface {
	systemd.Runner
	// SS returns `ss -H -lntup` output (or fixture text).
	SS(ctx context.Context) (string, error)
	// VersionCommand runs a version argv and returns output.
	Version(ctx context.Context, argv []string) (string, error)
}

// Options configures a collection run.
type Options struct {
	// HostSource is the pretend "/" for /proc reads. Empty uses the real root.
	HostSource host.Source
	// CgroupSource is the pretend "/" for cgroup reads. Empty uses the real root.
	CgroupSource cgroup.Source
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
				entry.ConfigPath = p
				entry.ConfigExists = configExists(opts, p)
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
	deps := map[string][]topology.Dependency{}
	unitStateByID := map[string]systemd.UnitStatus{}
	activeByID := map[string]bool{}
	requiredListenersByID := map[string][]string{}

	for _, sk := range svcSockets {
		idx, ok := svcIndex[sk.ServiceID]
		if !ok {
			continue
		}
		entry := &st.Services[idx]
		entry.Listeners = append(entry.Listeners, state.Listener{
			Protocol: sk.Protocol,
			Port:     sk.Port,
			Address:  sk.Address,
			Internal: sk.Internal,
			PID:      sk.PID,
			Process:  sk.Process,
		})
		topoListeners = append(topoListeners, topology.Listener{
			ServiceID: sk.ServiceID,
			Protocol:  sk.Protocol,
			Port:      sk.Port,
			Address:   sk.Address,
			Internal:  sk.Internal,
			PID:       sk.PID,
			Process:   sk.Process,
		})
	}

	// Unit state + memory for services with a systemd unit.
	for i := range st.Services {
		entry := &st.Services[i]
		unit := entry.Unit
		if unit == "" {
			continue
		}
		us, err := systemd.ShowUnit(ctx, r, unit)
		if err != nil {
			continue
		}
		entry.UnitState = us.ActiveState
		active := us.ActiveState == "active"
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

	// Final per-service status.
	for i := range st.Services {
		entry := &st.Services[i]
		unit := entry.Unit
		if unit != "" {
			active := activeByID[entry.ID]
			us := unitStateByID[entry.ID]
			status, problems := state.ServiceStatus(active, us.ActiveState, requiredListenersByID[entry.ID], configMissing(opts, entry))
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
		Dependencies: deps,
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

func configExists(opts Options, path string) bool {
	if opts.ConfigExists != nil {
		return opts.ConfigExists(path)
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
