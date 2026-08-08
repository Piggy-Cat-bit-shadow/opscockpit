package state

import "time"

// StatusRank maps a status to its severity. STALE is the most severe because
// a stale snapshot is untrustworthy: it wins over everything else.
var StatusRank = map[string]int{
	StatusStale:   0, // most severe
	StatusFailed:  1,
	StatusWarning: 2,
	StatusUnknown: 3,
	StatusHealthy: 4,
}

// Resolve returns the most severe status from a list.
func Resolve(statuses ...string) string {
	best := StatusHealthy
	bestRank := StatusRank[best]
	for _, s := range statuses {
		r, ok := StatusRank[s]
		if !ok {
			r = StatusRank[StatusUnknown]
		}
		if r < bestRank {
			bestRank = r
			best = s
		}
	}
	return best
}

// IsKnownStatus reports whether s is one of the model statuses.
func IsKnownStatus(s string) bool {
	_, ok := StatusRank[s]
	return ok
}

// ServiceStatus computes a service's status from unit state and required
// listeners. Returns the status plus a list of problems.
//
// Rules:
//   - Version unknown is NOT a problem.
//   - A service unit that is not active is failed.
//   - An active unit missing a required listener is failed.
//   - Config path override missing is a warning (not a failure).
func ServiceStatus(unitActive bool, unitState string, requiredMissing []string, configOverrideMissing bool) (string, []string) {
	if !unitActive {
		state := unitState
		if state == "" {
			state = "inactive"
		}
		return StatusFailed, []string{"unit not active (" + state + ")"}
	}

	var warnings []string
	for _, r := range requiredMissing {
		warnings = append(warnings, "required listener missing: "+r)
	}
	if len(warnings) > 0 {
		return StatusFailed, warnings
	}

	if configOverrideMissing {
		return StatusWarning, []string{"config path not found"}
	}

	return StatusHealthy, nil
}

// AgeSeconds returns how long ago the state was generated, in seconds.
func (s *State) AgeSeconds() int64 {
	return int64(time.Since(s.GeneratedAt).Seconds())
}

// IsStale reports whether the state is older than maxAge.
func (s *State) IsStale(maxAge time.Duration) bool {
	return s.GeneratedAt.Add(maxAge).Before(time.Now())
}

// FinalizeHealth computes the machine-wide health summary from the service
// list, applying stale detection (stale wins) and setting the message.
func (s *State) FinalizeHealth(maxAge time.Duration) {
	counts := map[string]int{}
	for _, svc := range s.Services {
		counts[svc.Status]++
	}

	h := Health{
		Stale:           s.IsStale(maxAge),
		AgeSeconds:      s.AgeSeconds(),
		ServicesHealthy: counts[StatusHealthy],
		ServicesWarning: counts[StatusWarning],
		ServicesFailed:  counts[StatusFailed],
		ServicesUnknown: counts[StatusUnknown],
	}

	if h.Stale {
		h.Status = StatusStale
		h.Message = "state is stale"
	} else {
		switch {
		case counts[StatusFailed] > 0:
			h.Status = StatusFailed
		case counts[StatusWarning] > 0:
			h.Status = StatusWarning
		case counts[StatusHealthy] > 0:
			h.Status = StatusHealthy
		default:
			h.Status = StatusUnknown
		}
		if h.Status == StatusHealthy {
			h.Message = "all systems nominal"
		} else if h.Status == StatusFailed {
			h.Message = "one or more services failed"
		} else if h.Status == StatusWarning {
			h.Message = "one or more services warning"
		}
	}

	s.Health = h
}
