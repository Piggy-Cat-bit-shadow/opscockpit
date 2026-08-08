// Package cgroup reads per-service memory via cgroup v2 memory.current, with a
// /proc/<pid>/status VmRSS fallback.
//
// Order of preference for a systemd unit:
//  1. ControlGroup → cgroup v2 memory.current (sum over all PIDs in the group)
//  2. cgroup PIDs → /proc/<pid>/status VmRSS (sum over all PIDs)
//  3. MainPID → /proc/<pid>/status VmRSS (single process)
package cgroup

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Source abstracts the runtime filesystem. Root is the pretend "/" — tests
// point it at a fixture directory.
type Source struct {
	Root string
}

// FromDir builds a Source rooted at dir.
func FromDir(dir string) Source { return Source{Root: dir} }

func (s Source) path(rel string) string { return filepath.Join(s.Root, rel) }

// Result is a memory readout.
type Result struct {
	RSSBytes int64
	// Source is one of "cgroup_memory_current", "proc_rss".
	Source string
}

// PIDSet lists the PIDs in a cgroup (cgroup.procs file).
func (s Source) PIDSet(cgroupPath string) ([]int, error) {
	// cgroupPath is the cgroup v2 relative path, e.g.
	//   /system.slice/hysteria-server.service
	rel := filepath.Join("sys/fs/cgroup", strings.TrimPrefix(cgroupPath, "/"))
	f, err := os.Open(s.path(rel) + "/cgroup.procs")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var pids []int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if pid, err := strconv.Atoi(line); err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids, sc.Err()
}

// memoryCurrent reads cgroup v2 memory.current (bytes).
func (s Source) memoryCurrent(cgroupPath string) (int64, error) {
	rel := filepath.Join("sys/fs/cgroup", strings.TrimPrefix(cgroupPath, "/"))
	b, err := os.ReadFile(s.path(rel) + "/memory.current")
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
}

// vmsRSS reads VmRSS from /proc/<pid>/status.
func (s Source) vmsRSS(pid int) (int64, error) {
	b, err := os.ReadFile(s.path(fmt.Sprintf("proc/%d/status", pid)))
	if err != nil {
		return 0, err
	}
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, fmt.Errorf("proc/%d/status: malformed VmRSS %q", pid, line)
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, err
		}
		return kb * 1024, nil
	}
	return 0, fmt.Errorf("proc/%d/status: VmRSS not found", pid)
}

// SumRSS sums VmRSS across a set of PIDs, skipping PIDs that are gone.
func (s Source) SumRSS(pids []int) int64 {
	var total int64
	for _, pid := range pids {
		if v, err := s.vmsRSS(pid); err == nil {
			total += v
		}
	}
	return total
}

// ErrNoCgroup is returned when neither the cgroup path nor any PID yields data.
var ErrNoCgroup = errors.New("no cgroup memory data available")

// MemoryForControlGroup reads memory for a unit's ControlGroup path. If the
// cgroup path is empty, it returns ErrNoCgroup so callers can fall back.
func (s Source) MemoryForControlGroup(cgroupPath string) (Result, error) {
	if cgroupPath == "" {
		return Result{}, ErrNoCgroup
	}

	// Primary: cgroup v2 memory.current.
	if cur, err := s.memoryCurrent(cgroupPath); err == nil && cur >= 0 {
		return Result{RSSBytes: cur, Source: "cgroup_memory_current"}, nil
	}

	// Fallback 1: sum VmRSS over all PIDs in the cgroup.
	if pids, err := s.PIDSet(cgroupPath); err == nil && len(pids) > 0 {
		total := s.SumRSS(pids)
		if total > 0 {
			return Result{RSSBytes: total, Source: "proc_rss"}, nil
		}
	}

	return Result{}, ErrNoCgroup
}

// MemoryForMainPID reads a single process's VmRSS.
func (s Source) MemoryForMainPID(pid int) (Result, error) {
	if pid <= 0 {
		return Result{}, ErrNoCgroup
	}
	v, err := s.vmsRSS(pid)
	if err != nil {
		return Result{}, err
	}
	return Result{RSSBytes: v, Source: "proc_rss"}, nil
}
