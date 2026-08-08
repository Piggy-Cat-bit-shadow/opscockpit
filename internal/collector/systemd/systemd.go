// Package systemd inspects systemd units via `systemctl show --property=...`.
//
// Everything goes through the Runner interface so tests can supply a mock
// runner and never require a real target unit on the CI host. We parse
// `systemctl show` key=value output — never human-facing `systemctl status`
// text.
package systemd

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Runner runs the systemctl commands. The real implementation shells out to
// systemctl; tests substitute a mock.
type Runner interface {
	// Run executes argv and returns combined stdout/stderr.
	Run(ctx context.Context, argv []string) (string, error)
	// RunUnit executes a unit-scoped query, e.g.
	//   systemctl show <unit> --property=ActiveState ...
	RunUnit(ctx context.Context, unit string, properties []string) (string, error)
}

// ExecRunner is the real systemctl runner.
type ExecRunner struct {
	// Systemctl is the systemctl path; empty means "systemctl" on PATH.
	Systemctl string
	// Timeout bounds every unit query.
	Timeout time.Duration
}

func (r ExecRunner) argv(args ...string) []string {
	bin := r.Systemctl
	if bin == "" {
		bin = "systemctl"
	}
	return append([]string{bin}, args...)
}

// Run executes systemctl with the given args.
func (r ExecRunner) Run(ctx context.Context, argv []string) (string, error) {
	bin := r.Systemctl
	if bin == "" {
		bin = "systemctl"
	}
	args := argv[1:]
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// RunUnit runs `systemctl show <unit> --property=<props...>`.
func (r ExecRunner) RunUnit(ctx context.Context, unit string, properties []string) (string, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	argv := []string{"show", unit}
	for _, p := range properties {
		argv = append(argv, "--property="+p)
	}
	return r.Run(cctx, argv)
}

// UnitStatus is the parsed result of a systemctl show query.
type UnitStatus struct {
	ActiveState   string
	SubState      string
	MainPID       int
	ControlGroup  string
	FragmentPath  string
	ExecStart     string
	Result        string
	LoadState     string
	Found         bool
}

// property names we query.
var unitProperties = []string{
	"ActiveState",
	"SubState",
	"MainPID",
	"ControlGroup",
	"FragmentPath",
	"ExecStart",
	"Result",
	"LoadState",
}

// ShowUnit queries and parses one unit. Returns Found=false when the unit does
// not exist (LoadState=not-found) or the query is empty.
func ShowUnit(ctx context.Context, r Runner, unit string) (UnitStatus, error) {
	us := UnitStatus{}
	out, err := r.RunUnit(ctx, unit, unitProperties)
	if err != nil {
		return us, err
	}
	kv := parseKV(out)
	if len(kv) == 0 {
		return us, fmt.Errorf("systemctl show %s returned no output", unit)
	}

	us.Found = true
	us.ActiveState = kv["ActiveState"]
	us.SubState = kv["SubState"]
	us.FragmentPath = kv["FragmentPath"]
	us.ExecStart = kv["ExecStart"]
	us.Result = kv["Result"]
	us.LoadState = kv["LoadState"]
	us.ControlGroup = kv["ControlGroup"]
	if v, ok := kv["MainPID"]; ok {
		us.MainPID = atoi(v)
	}
	if us.LoadState == "not-found" {
		us.Found = false
	}
	return us, nil
}

// parseKV parses `systemctl show` key=value output. Multi-line values are
// joined with "\n".
func parseKV(out string) map[string]string {
	m := map[string]string{}
	var currentKey string
	var currentVal []string
	flush := func() {
		if currentKey != "" {
			m[currentKey] = strings.Join(currentVal, "\n")
		}
		currentKey = ""
		currentVal = nil
	}
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		idx := strings.Index(line, "=")
		if idx <= 0 {
			continue
		}
		// New key=value pair.
		flush()
		currentKey = line[:idx]
		currentVal = []string{line[idx+1:]}
	}
	flush()
	return m
}

func atoi(s string) int {
	n := 0
	neg := false
	for i, c := range s {
		if i == 0 && c == '-' {
			neg = true
			continue
		}
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	if neg {
		return -n
	}
	return n
}
