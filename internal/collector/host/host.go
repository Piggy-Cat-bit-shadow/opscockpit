// Package host collects machine-level snapshots from /proc, /sys, and statfs.
//
// All input is read from a Source interface so tests can feed fixtures instead
// of relying on the CI runner's live machine state.
package host

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Source abstracts the Linux runtime filesystem so tests can substitute
// fixtures.
type Source struct {
	Root string // pretend "/" — everything is read below this root
}

// FromDir builds a Source rooted at dir (for tests).
func FromDir(dir string) Source { return Source{Root: dir} }

func (s Source) path(rel string) string {
	root := s.Root
	if root == "" {
		root = "/"
	}
	return filepath.Join(root, rel)
}

func (s Source) readFile(rel string) (string, error) {
	b, err := os.ReadFile(s.path(rel))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Info is the full host snapshot.
type Info struct {
	Hostname      string
	UptimeSeconds float64
	CPU           CPU
	Memory        Mem
	Swap          Mem
	Disk          Disk
	Load          Load
}

// CPU holds core count and the busy percentage over the last sample window.
type CPU struct {
	Cores   int
	Percent float64
}

// Mem holds memory stats in bytes.
type Mem struct {
	Total   int64
	Used    int64
	Percent float64
}

// Disk holds root filesystem stats.
type Disk struct {
	MountPoint string
	Total      int64
	Used       int64
	Percent    float64
}

// Load holds load averages.
type Load struct {
	Load1  float64
	Load5  float64
	Load15 float64
}

// Hostname reads the kernel hostname.
func (s Source) Hostname() (string, error) {
	h, err := s.readFile("proc/sys/kernel/hostname")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(h), nil
}

// Uptime parses /proc/uptime.
func (s Source) Uptime() (float64, error) {
	raw, err := s.readFile("proc/uptime")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(raw)
	if len(fields) < 1 {
		return 0, fmt.Errorf("uptime: unexpected format %q", raw)
	}
	return strconv.ParseFloat(fields[0], 64)
}

// ParseLoad parses /proc/loadavg content.
func ParseLoad(raw string) (Load, error) {
	fields := strings.Fields(raw)
	if len(fields) < 3 {
		return Load{}, fmt.Errorf("loadavg: unexpected format %q", raw)
	}
	var l Load
	var err error
	if l.Load1, err = strconv.ParseFloat(fields[0], 64); err != nil {
		return Load{}, fmt.Errorf("loadavg load1: %w", err)
	}
	if l.Load5, err = strconv.ParseFloat(fields[1], 64); err != nil {
		return Load{}, fmt.Errorf("loadavg load5: %w", err)
	}
	if l.Load15, err = strconv.ParseFloat(fields[2], 64); err != nil {
		return Load{}, fmt.Errorf("loadavg load15: %w", err)
	}
	return l, nil
}

// Load reads /proc/loadavg.
func (s Source) Load() (Load, error) {
	raw, err := s.readFile("proc/loadavg")
	if err != nil {
		return Load{}, err
	}
	return ParseLoad(raw)
}

type cpuTimes struct {
	idle  float64
	total float64
}

// readCPUTimes parses the aggregate line of /proc/stat.
func readCPUTimes(raw string) (cpuTimes, error) {
	line := strings.TrimSpace(raw)
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuTimes{}, fmt.Errorf("stat: unexpected format %q", raw)
	}
	var vals []float64
	for _, f := range fields[1:] {
		v, err := strconv.ParseFloat(f, 64)
		if err != nil {
			return cpuTimes{}, fmt.Errorf("stat: parse %q: %w", f, err)
		}
		vals = append(vals, v)
	}
	var total float64
	for _, v := range vals {
		total += v
	}
	// idle = idle + iowait (fields 4 and 5).
	idle := vals[3] + vals[4]
	return cpuTimes{idle: idle, total: total}, nil
}

// CPUCores counts the cpuN lines in /proc/stat.
func (s Source) CPUCores() (int, error) {
	raw, err := s.readFile("proc/stat")
	if err != nil {
		return 0, err
	}
	sc := bufio.NewScanner(strings.NewReader(raw))
	cores := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "cpu") && len(line) > 3 && line[3] != ' ' && !strings.HasPrefix(line, "cpu ") {
			cores++
		}
	}
	if cores == 0 {
		return 0, fmt.Errorf("stat: no cpuN lines found")
	}
	return cores, nil
}

// Sample is one /proc/stat CPU sample used to compute a percentage.
type Sample struct {
	Time  cpuTimes
	Clock float64
}

// cpuSample reads the aggregate CPU line and the CLK_TCK-derived time base.
func (s Source) cpuSample() (Sample, error) {
	raw, err := s.readFile("proc/stat")
	if err != nil {
		return Sample{}, err
	}
	first := true
	var ct cpuTimes
	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "cpu ") {
			var err error
			ct, err = readCPUTimes(sc.Text())
			if err != nil {
				return Sample{}, err
			}
			first = false
			break
		}
	}
	if first {
		return Sample{}, fmt.Errorf("stat: aggregate cpu line not found")
	}
	return Sample{Time: ct, Clock: 100}, nil
}

// CPUPercent samples /proc/stat twice, sleeping interval between reads, and
// returns the busy percentage over that window. Sampling twice lets the caller
// control timing (including a zero interval for tests, which yields 0%).
func (s Source) CPUPercent(intervalMs int) (float64, error) {
	before, err := s.cpuSample()
	if err != nil {
		return 0, err
	}
	// Best effort: wait the interval. Tests pass 0 to avoid sleeping.
	if intervalMs > 0 {
		timeSleep(intervalMs)
	}
	after, err := s.cpuSample()
	if err != nil {
		return 0, err
	}
	dTotal := after.Time.total - before.Time.total
	if dTotal <= 0 {
		return 0, nil
	}
	dIdle := after.Time.idle - before.Time.idle
	busy := (dTotal - dIdle) / dTotal * 100
	if busy < 0 {
		busy = 0
	}
	if busy > 100 {
		busy = 100
	}
	return busy, nil
}

// ParseMeminfo parses /proc/meminfo content into total and used bytes.
func ParseMeminfo(raw string) (total, used int64, err error) {
	fields := map[string]int64{}
	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		line := sc.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		valStr := strings.TrimSpace(parts[1])
		valStr = strings.Fields(valStr)[0] // strip " kB" unit
		v, err := strconv.ParseInt(valStr, 10, 64)
		if err != nil {
			continue
		}
		fields[key] = v
	}
	total = fields["MemTotal"] * 1024
	available := fields["MemAvailable"] * 1024
	if total <= 0 {
		return 0, 0, fmt.Errorf("meminfo: MemTotal missing")
	}
	if available > 0 && available <= total {
		used = total - available
	} else {
		// Fallback: used = total - free - buffers - cached (approx).
		used = total - fields["MemFree"]*1024 - fields["Buffers"]*1024 - fields["Cached"]*1024
		if used < 0 {
			used = 0
		}
	}
	return total, used, nil
}

// Memory reads /proc/meminfo for RAM.
func (s Source) Memory() (Mem, error) {
	raw, err := s.readFile("proc/meminfo")
	if err != nil {
		return Mem{}, err
	}
	total, used, err := ParseMeminfo(raw)
	if err != nil {
		return Mem{}, err
	}
	return Mem{Total: total, Used: used, Percent: percent(used, total)}, nil
}

// Swap reads the SwapTotal/SwapFree lines of /proc/meminfo.
func (s Source) Swap() (Mem, error) {
	raw, err := s.readFile("proc/meminfo")
	if err != nil {
		return Mem{}, err
	}
	fields := map[string]int64{}
	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		if key != "SwapTotal" && key != "SwapFree" {
			continue
		}
		valStr := strings.TrimSpace(parts[1])
		valStr = strings.Fields(valStr)[0]
		if v, err := strconv.ParseInt(valStr, 10, 64); err == nil {
			fields[key] = v * 1024
		}
	}
	total := fields["SwapTotal"]
	if total <= 0 {
		return Mem{}, nil // no swap configured
	}
	used := total - fields["SwapFree"]
	if used < 0 {
		used = 0
	}
	return Mem{Total: total, Used: used, Percent: percent(used, total)}, nil
}

// Statfs calls the system statfs; for fixtures it reads total/used from a
// synthetic file under the source root.
func (s Source) Statfs(mount string) (Disk, error) {
	d := Disk{MountPoint: mount}
	if s.Root != "" {
		raw, err := s.readFile("proc/self/fixture_disk")
		if err == nil {
			fields := strings.Fields(raw)
			if len(fields) >= 2 {
				total, e1 := strconv.ParseInt(fields[0], 10, 64)
				used, e2 := strconv.ParseInt(fields[1], 10, 64)
				if e1 == nil && e2 == nil {
					d.Total = total
					d.Used = used
					d.Percent = percent(used, total)
					return d, nil
				}
			}
		}
	}
	// Real statfs path.
	fs, err := statfs(mount)
	if err != nil {
		return Disk{}, err
	}
	d.Total = int64(fs.Blocks) * int64(fs.Bsize)
	d.Used = (int64(fs.Blocks) - int64(fs.Bfree)) * int64(fs.Bsize)
	d.Percent = percent(d.Used, d.Total)
	return d, nil
}

func percent(used, total int64) float64 {
	if total <= 0 {
		return 0
	}
	p := float64(used) / float64(total) * 100
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	return p
}
