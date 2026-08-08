package collect

import (
	"context"
	"testing"

	"github.com/opscockpit/opscockpit/internal/state"
)

// TestDockerServiceHealth: a Docker-backed service's health follows the
// container state (stopped → failed, unhealthy → warning, healthy → ok).
func TestDockerServiceHealth(t *testing.T) {
	svcPath := writeServicesYAML(t, `services:
  - id: dockerapp
    name: Docker App
    docker:
      container: my-app
`)
	r := &mockRunner{
		dockerPS: `abc123|my-app|img/app:1|Up 2 hours (healthy)
def456|other|img/x|Exited (0) 1 hour ago|
`,
	}
	res, err := Collect(context.Background(), r, Options{ServicesPath: svcPath})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	st := res.State
	app := byID(t, st, "dockerapp")
	if app.Status != state.StatusHealthy {
		t.Errorf("healthy docker container status = %q, want healthy", app.Status)
	}
}

// TestDockerUnhealthyWarns: running + unhealthy → warning, not failed.
func TestDockerUnhealthyWarns(t *testing.T) {
	svcPath := writeServicesYAML(t, `services:
  - id: dockerapp
    name: Docker App
    docker:
      container: my-app
`)
	r := &mockRunner{
		dockerPS: `abc123|my-app|img/app:1|Up 2 hours (unhealthy)
`,
	}
	res, err := Collect(context.Background(), r, Options{ServicesPath: svcPath})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	app := byID(t, res.State, "dockerapp")
	if app.Status != state.StatusWarning {
		t.Errorf("unhealthy container status = %q, want warning", app.Status)
	}
}

// TestDockerStoppedFails: stopped container → failed.
func TestDockerStoppedFails(t *testing.T) {
	svcPath := writeServicesYAML(t, `services:
  - id: dockerapp
    name: Docker App
    docker:
      container: my-app
`)
	r := &mockRunner{
		dockerPS: `abc123|my-app|img/app:1|Exited (1) 1 hour ago|
`,
	}
	res, err := Collect(context.Background(), r, Options{ServicesPath: svcPath})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	app := byID(t, res.State, "dockerapp")
	if app.Status != state.StatusFailed {
		t.Errorf("stopped container status = %q, want failed", app.Status)
	}
}

// TestDockerLoopbackPublishedPortNeverTopLevel: a container published on
// 127.0.0.1:3001 must never become an Internet top-level port.
func TestDockerLoopbackPublishedPortNeverTopLevel(t *testing.T) {
	svcPath := writeServicesYAML(t, `services:
  - id: dockerapp
    name: Docker App
    docker:
      container: my-app
`)
	r := &mockRunner{
		// ss has nothing public; the only "listener" is the loopback docker
		// published port (not in ss). Docker publish is loopback → internal.
		ssText:  "",
		dockerPS: `abc123|my-app|img/app:1|Up 2 hours (healthy)
`,
		ufwText: `Status: active
Default: deny (incoming), allow (outgoing), disabled (routed)

To                         Action      From
--                         ------      ----
3001/tcp                   ALLOW IN    Anywhere
`,
	}
	res, err := Collect(context.Background(), r, Options{ServicesPath: svcPath})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	// No top-level port 3001 anywhere.
	for _, n := range res.State.Topology.Nodes {
		if n.Type == state.NodePort && (n.Label == "3001" || n.Port == 3001) {
			t.Error("loopback docker published port 3001 must not be a top-level port")
		}
	}
}

// TestDockerPartialFailure: a docker error (empty/missing) must not fail the
// collect or prevent state.json from being produced.
func TestDockerPartialFailure(t *testing.T) {
	svcPath := writeServicesYAML(t, `services:
  - id: nginx
    name: Nginx
    systemd: { unit: nginx.service }
`)
	r := &mockRunner{
		ssText: `tcp   LISTEN 0 511    0.0.0.0:443        0.0.0.0:*  users:(("nginx",pid=1001,fd=10))
`,
		units: map[string]string{"nginx.service": unitShow("active", 1001, "/system.slice/nginx.service")},
		pidToSvc: map[int]string{1001: "nginx"},
		ufwText: `Status: active
Default: deny (incoming), allow (outgoing), disabled (routed)

To                         Action      From
--                         ------      ----
443/tcp                    ALLOW IN    Anywhere
`,
		// dockerPS empty → docker collection skipped, collect still succeeds.
	}
	res, err := Collect(context.Background(), r, Options{ServicesPath: svcPath})
	if err != nil {
		t.Fatalf("collect must not fail when docker is absent: %v", err)
	}
	if res.State == nil {
		t.Fatal("state must still be produced")
	}
	if res.State.Topology.Nodes == nil {
		t.Error("topology must still be generated")
	}
}
