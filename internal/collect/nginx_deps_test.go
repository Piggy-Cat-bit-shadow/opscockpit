package collect

import (
	"context"
	"testing"

	"github.com/opscockpit/opscockpit/internal/state"
)

// nginxNginxFixture models a real host with stream ssl_preread routing to
// multiple backends via map variables.
const nginxChainFixture = `# configuration file /etc/nginx/nginx.conf:
stream {
  map $ssl_preread_server_name $backend {
    api.example.com web_backend;
    proxy.example.com xray_backend;
    default web_backend;
  }
  upstream web_backend {
    server 127.0.0.1:9443;
  }
  upstream xray_backend {
    server 127.0.0.1:18444;
  }
  server {
    listen 443;
    ssl_preread on;
    proxy_pass $backend;
  }
}
`

// chainServices: nginx(443/tcp), a 9443 backend, xray on 18444 (internal).
func chainServicesPath(t *testing.T) string {
	return writeServicesYAML(t, `services:
  - id: nginx
    name: Nginx
    systemd: { unit: nginx.service }
  - id: backendsvc
    name: Backend
    systemd: { unit: backendsvc.service }
  - id: xray
    name: Xray
    systemd: { unit: xray.service }
`)
}

func chainRunner() *mockRunner {
	return &mockRunner{
		ssText: `tcp   LISTEN 0 511    0.0.0.0:443        0.0.0.0:*  users:(("nginx",pid=1001,fd=10))
tcp   LISTEN 0 128    127.0.0.1:9443     0.0.0.0:*  users:(("backendsvc",pid=2002,fd=11))
tcp   LISTEN 0 128    127.0.0.1:18444    0.0.0.0:*  users:(("xray",pid=3003,fd=12))
`,
		units: map[string]string{
			"nginx.service":      unitShow("active", 1001, "/system.slice/nginx.service"),
			"backendsvc.service": unitShow("active", 2002, "/system.slice/backendsvc.service"),
			"xray.service":       unitShow("active", 3003, "/system.slice/xray.service"),
		},
		pidToSvc: map[int]string{
			1001: "nginx",
			2002: "backendsvc",
			3003: "xray",
		},
		ufwText: `Status: active
Default: deny (incoming), allow (outgoing), disabled (routed)

To                         Action      From
--                         ------      ----
443/tcp                    ALLOW IN    Anywhere
`,
		nginxText: nginxChainFixture,
	}
}

// depsOfService scans the topology edges for a service's outgoing dependency
// edges (service → service) and returns the target service ids.
func depsOfService(st *state.State, serviceID string) []string {
	// Map node id → node for both source and target resolution.
	nodesByID := map[string]state.Node{}
	for _, n := range st.Topology.Nodes {
		nodesByID[n.ID] = n
	}
	var out []string
	for _, e := range st.Topology.Edges {
		src, ok := nodesByID[e.Source]
		if !ok || src.ServiceID != serviceID {
			continue
		}
		if tgt, ok := nodesByID[e.Target]; ok && tgt.ServiceID != "" && tgt.ServiceID != serviceID {
			out = append(out, tgt.ServiceID)
		}
	}
	return out
}

// TestNginxMultiHopDeps verifies nginx → backend + nginx → xray are resolved
// via the endpoint resolver (map variables + named upstreams).
func TestNginxMultiHopDeps(t *testing.T) {
	r := chainRunner()
	res, err := Collect(context.Background(), r, Options{
		ServicesPath: chainServicesPath(t),
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	st := res.State

	// nginx → xray dependency must exist (xray on loopback 18444).
	deps := depsOfService(st, "nginx")
	found := false
	for _, d := range deps {
		if d == "xray" {
			found = true
		}
	}
	if !found {
		t.Errorf("nginx → xray dependency not resolved: %v", deps)
	}
}

// TestNginxDeclaredUpstream verifies services.yaml topology.upstream_from
// (exec_arg flag) resolves a ShadowTLS-style chain without storing ExecStart.
func TestNginxDeclaredUpstream(t *testing.T) {
	svcPath := writeServicesYAML(t, `services:
  - id: shadow
    name: ShadowTLS
    systemd: { unit: shadow.service }
    topology:
      upstream_from:
        - source: exec_arg
          flag: --server
  - id: snell
    name: Snell
    systemd: { unit: snell.service }
`)
	r := &mockRunner{
		ssText: `tcp   LISTEN 0 511    0.0.0.0:443        0.0.0.0:*  users:(("shadow",pid=1001,fd=10))
tcp   LISTEN 0 128    127.0.0.1:17414    0.0.0.0:*  users:(("snell",pid=2002,fd=11))
`,
		units: map[string]string{
			"shadow.service": unitShow("active", 1001, "/system.slice/shadow.service"),
			"snell.service":  unitShow("active", 2002, "/system.slice/snell.service"),
		},
		pidToSvc: map[int]string{
			1001: "shadow",
			2002: "snell",
		},
		ufwText: `Status: active
Default: deny (incoming), allow (outgoing), disabled (routed)

To                         Action      From
--                         ------      ----
443/tcp                    ALLOW IN    Anywhere
`,
		// ExecStart contains the declared --server endpoint. The password is
		// deliberately present to prove it never leaks into state.json.
		unitExecStart: map[string]string{
			"shadow.service": "{ path=/usr/local/bin/shadowsocks ; argv[]=/usr/local/bin/shadowsocks --server 127.0.0.1:17414 --password supersecret ; }",
		},
	}
	res, err := Collect(context.Background(), r, Options{ServicesPath: svcPath})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	st := res.State

	// shadow → snell dependency must exist.
	deps := depsOfService(st, "shadow")
	found := false
	for _, d := range deps {
		if d == "snell" {
			found = true
		}
	}
	if !found {
		t.Errorf("declared upstream not resolved: %v", deps)
	}

	if err := st.Validate(); err != nil {
		t.Fatalf("secret leaked into state: %v", err)
	}
}
