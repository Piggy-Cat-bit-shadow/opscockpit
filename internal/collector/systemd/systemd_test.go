package systemd

import (
	"context"
	"strings"
	"testing"
)

// mockRunner returns canned output per unit.
type mockRunner struct {
	units map[string]string
	callN int
}

func (m *mockRunner) Run(ctx context.Context, argv []string) (string, error) {
	m.callN++
	return strings.Join(argv, " "), nil
}

func (m *mockRunner) RunUnit(ctx context.Context, unit string, properties []string) (string, error) {
	if out, ok := m.units[unit]; ok {
		return out, nil
	}
	return "", nil
}

func TestShowUnitActive(t *testing.T) {
	mock := &mockRunner{units: map[string]string{
		"hysteria-server.service": `ActiveState=active
SubState=running
MainPID=1234
ControlGroup=/system.slice/hysteria-server.service
FragmentPath=/etc/systemd/system/hysteria-server.service
ExecStart={ path=/usr/local/bin/hysteria ; argv[]=/usr/local/bin/hysteria server ; ignore_errors=no ; start_time=[n/a] ; stop_time=[n/a] ; pid=1234 ; code=(null) ; status=0/0 }
Result=success
LoadState=loaded
`,
	}}
	us, err := ShowUnit(context.Background(), mock, "hysteria-server.service")
	if err != nil {
		t.Fatal(err)
	}
	if !us.Found {
		t.Fatal("unit should be found")
	}
	if us.ActiveState != "active" || us.SubState != "running" {
		t.Errorf("state = %s/%s", us.ActiveState, us.SubState)
	}
	if us.MainPID != 1234 {
		t.Errorf("MainPID = %d, want 1234", us.MainPID)
	}
	if us.ControlGroup != "/system.slice/hysteria-server.service" {
		t.Errorf("ControlGroup = %q", us.ControlGroup)
	}
	if us.FragmentPath == "" {
		t.Error("FragmentPath missing")
	}
	if !strings.Contains(us.ExecStart, "hysteria server") {
		t.Errorf("ExecStart = %q", us.ExecStart)
	}
}

func TestShowUnitNotFound(t *testing.T) {
	mock := &mockRunner{units: map[string]string{
		"nonexistent.service": `LoadState=not-found
ActiveState=inactive
`,
	}}
	us, err := ShowUnit(context.Background(), mock, "nonexistent.service")
	if err != nil {
		t.Fatal(err)
	}
	if us.Found {
		t.Fatal("nonexistent unit should not be found")
	}
}

func TestShowUnitEmptyOutput(t *testing.T) {
	mock := &mockRunner{units: map[string]string{}}
	if _, err := ShowUnit(context.Background(), mock, "ghost.service"); err == nil {
		t.Fatal("expected error on empty output")
	}
}

func TestShowUnitInactive(t *testing.T) {
	mock := &mockRunner{units: map[string]string{
		"xray.service": `ActiveState=inactive
SubState=dead
MainPID=0
ControlGroup=
FragmentPath=/etc/systemd/system/xray.service
Result=success
LoadState=loaded
`,
	}}
	us, err := ShowUnit(context.Background(), mock, "xray.service")
	if err != nil {
		t.Fatal(err)
	}
	if us.Found != true {
		t.Fatal("loaded unit should be found even when inactive")
	}
	if us.ActiveState != "inactive" {
		t.Errorf("ActiveState = %q", us.ActiveState)
	}
	if us.MainPID != 0 {
		t.Errorf("MainPID = %d", us.MainPID)
	}
}

func TestShowUnitFailed(t *testing.T) {
	mock := &mockRunner{units: map[string]string{
		"broken.service": `ActiveState=failed
SubState=failed
MainPID=0
ControlGroup=
FragmentPath=/etc/systemd/system/broken.service
Result=exit-code
LoadState=loaded
`,
	}}
	us, err := ShowUnit(context.Background(), mock, "broken.service")
	if err != nil {
		t.Fatal(err)
	}
	if us.ActiveState != "failed" {
		t.Errorf("ActiveState = %q", us.ActiveState)
	}
	if us.Result != "exit-code" {
		t.Errorf("Result = %q", us.Result)
	}
}

func TestParseKVMultiLine(t *testing.T) {
	out := `ActiveState=active
ExecStart={ path=/usr/bin/foo ; argv[]=/usr/bin/foo -c x ; ignore_errors=no }
Result=success
`
	kv := parseKV(out)
	if kv["ActiveState"] != "active" {
		t.Errorf("ActiveState = %q", kv["ActiveState"])
	}
	if !strings.Contains(kv["ExecStart"], "-c x") {
		t.Errorf("ExecStart = %q", kv["ExecStart"])
	}
	if kv["Result"] != "success" {
		t.Errorf("Result = %q", kv["Result"])
	}
}

func TestParseKVEmptyLines(t *testing.T) {
	kv := parseKV("\n\nA=1\n\nB=2\n")
	if len(kv) != 2 {
		t.Errorf("expected 2 keys, got %d: %v", len(kv), kv)
	}
}

func TestUnitPropertiesIncludeControlGroup(t *testing.T) {
	found := false
	for _, p := range unitProperties {
		if p == "ControlGroup" {
			found = true
		}
	}
	if !found {
		t.Fatal("unitProperties must include ControlGroup")
	}
}

func TestIsTemplateUnit(t *testing.T) {
	if !IsTemplateUnit("foo@.service") {
		t.Error("foo@.service is a template")
	}
	if IsTemplateUnit("foo@bar.service") {
		t.Error("foo@bar.service is an instance, not a template")
	}
	if IsTemplateUnit("nginx.service") {
		t.Error("plain unit is not a template")
	}
}

func TestOneshotHealthy(t *testing.T) {
	// active/exited with RemainAfterExit=yes (e.g. apply-rule unit).
	us := UnitStatus{ActiveState: "active", SubState: "exited", RemainAfterExit: true}
	if !us.IsHealthyActive() {
		t.Error("active/exited + RemainAfterExit should be healthy")
	}
}

func TestOneshotWithoutRemainAfterExitNotHealthy(t *testing.T) {
	// active/exited without RemainAfterExit — transient, not a running service.
	us := UnitStatus{ActiveState: "active", SubState: "exited", RemainAfterExit: false}
	if us.IsHealthyActive() {
		t.Error("active/exited without RemainAfterExit must not count as healthy")
	}
}

func TestActiveRunningHealthy(t *testing.T) {
	us := UnitStatus{ActiveState: "active", SubState: "running"}
	if !us.IsHealthyActive() {
		t.Error("active/running should be healthy")
	}
}

func TestInactiveNotHealthy(t *testing.T) {
	us := UnitStatus{ActiveState: "inactive", SubState: "dead"}
	if us.IsHealthyActive() {
		t.Error("inactive must not be healthy")
	}
}

func TestFailedNotHealthy(t *testing.T) {
	us := UnitStatus{ActiveState: "failed", SubState: "failed"}
	if us.IsHealthyActive() {
		t.Error("failed must not be healthy")
	}
}

func TestShowUnitOneshotFields(t *testing.T) {
	mock := &mockRunner{units: map[string]string{
		"nat-setup.service": `ActiveState=active
SubState=exited
MainPID=0
ControlGroup=/system.slice/nat-setup.service
Type=oneshot
RemainAfterExit=yes
LoadState=loaded
`,
	}}
	us, err := ShowUnit(context.Background(), mock, "nat-setup.service")
	if err != nil {
		t.Fatal(err)
	}
	if us.Type != "oneshot" || !us.RemainAfterExit {
		t.Errorf("oneshot fields = type=%q remain_after_exit=%v", us.Type, us.RemainAfterExit)
	}
	if !us.IsHealthyActive() {
		t.Error("nat-setup (oneshot, RemainAfterExit) should be healthy")
	}
	if us.MainPID != 0 {
		t.Errorf("MainPID = %d, want 0", us.MainPID)
	}
}
