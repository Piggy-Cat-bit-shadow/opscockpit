// Package host collects machine-level snapshots from /proc, /sys, and statfs.
//
// All input is read from a Source interface so tests can feed fixtures instead
// of relying on the CI runner's live machine state.
package host

// Collect returns a full host snapshot. On platforms without real statfs
// (non-linux), disk stats require a fixture.
func Collect(s Source, cpuIntervalMs int) (Info, error) {
	info := Info{}

	if h, err := s.Hostname(); err == nil {
		info.Hostname = h
	}
	if u, err := s.Uptime(); err == nil {
		info.UptimeSeconds = u
	}
	if l, err := s.Load(); err == nil {
		info.Load = l
	}
	if cores, err := s.CPUCores(); err == nil {
		info.CPU.Cores = cores
	}
	if pct, err := s.CPUPercent(cpuIntervalMs); err == nil {
		info.CPU.Percent = pct
	}
	if m, err := s.Memory(); err == nil {
		info.Memory = m
	}
	if sw, err := s.Swap(); err == nil {
		info.Swap = sw
	}
	if d, err := s.Statfs("/"); err == nil {
		info.Disk = d
	}

	return info, nil
}
