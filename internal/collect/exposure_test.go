package collect

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/opscockpit/opscockpit/internal/state"
)

// writeServicesYAML writes a services.yaml content to a temp file and returns
// its path.
func writeServicesYAML(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "services.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Exposure fixtures use documentation-range addresses only (203.0.113.x).
const exposureUFW = `Status: active
Logging: on (low)
Default: deny (incoming), allow (outgoing), disabled (routed)
New profiles: skip

To                         Action      From
--                         ------      ----
22/tcp                     ALLOW IN    Anywhere
443/tcp                    ALLOW IN    Anywhere
443/udp                    ALLOW IN    Anywhere
853/tcp                    ALLOW IN    Anywhere
20000:20099/udp            ALLOW IN    203.0.113.10
20100:20199/udp            ALLOW IN    203.0.113.10
8554/udp                   ALLOW IN    203.0.113.10
17414/udp                  ALLOW IN    203.0.113.10
`

const exposureNAT = `-P PREROUTING ACCEPT
-P INPUT ACCEPT
-P OUTPUT ACCEPT
-P POSTROUTING ACCEPT
-A PREROUTING -d 203.0.113.10/32 -p udp --dport 20000:20099 -j REDIRECT --to-ports 443
-A PREROUTING -d 203.0.113.10/32 -p udp --dport 20100:20199 -j REDIRECT --to-ports 8443
-A PREROUTING -d 203.0.113.10/32 -p udp --dport 8554 -j REDIRECT --to-ports 17414
-A PREROUTING -i docker0 -p tcp --dport 3001 -j DNAT --to-destination 172.17.0.2:3001
`

// exposureSS models all listeners including a wildcard AdGuard 18453 that has
// NO firewall allow (scenario C), a Docker-loopback DNAT 3001 (scenario G),
// and the backend targets.
const exposureSS = `tcp   LISTEN 0 511    0.0.0.0:443        0.0.0.0:*  users:(("nginx",pid=1001,fd=10))
udp   UNCONN 0 0      [::]:443          [::]:*     users:(("hysteria",pid=2002,fd=7))
tcp   LISTEN 0 128    0.0.0.0:18453     0.0.0.0:*  users:(("adguard",pid=3003,fd=6))
udp   UNCONN 0 0      [::]:8443         [::]:*     users:(("tuic",pid=4004,fd=9))
udp   UNCONN 0 0      [::]:17414        [::]:*     users:(("snell",pid=5005,fd=11))
tcp   LISTEN 0 128    127.0.0.1:3001    0.0.0.0:*  users:(("docker-proxy",pid=6006,fd=12))
`

func exposureServicesPath(t *testing.T) string {
	t.Helper()
	return writeServicesYAML(t, `services:
  - id: nginx
    name: Nginx
    systemd: { unit: nginx.service }
    restart_enabled: true
  - id: hysteria2
    name: Hysteria2
    systemd: { unit: hysteria-server.service }
    restart_enabled: true
  - id: adguard-home
    name: AdGuard Home
    systemd: { unit: adguard.service }
    restart_enabled: true
  - id: tuic
    name: TUIC
    systemd: { unit: tuic.service }
    restart_enabled: true
  - id: snell
    name: Snell
    systemd: { unit: snell.service }
    restart_enabled: true
`)
}

func exposureRunner(ufw, nat, ss string) *mockRunner {
	return &mockRunner{
		ssText: ss,
		units: map[string]string{
			"nginx.service":           unitShow("active", 1001, "/system.slice/nginx.service"),
			"hysteria-server.service": unitShow("active", 2002, "/system.slice/hysteria-server.service"),
			"adguard.service":         unitShow("active", 3003, "/system.slice/adguard.service"),
			"tuic.service":            unitShow("active", 4004, "/system.slice/tuic.service"),
			"snell.service":           unitShow("active", 5005, "/system.slice/snell.service"),
		},
		pidToSvc: map[int]string{
			1001: "nginx",
			2002: "hysteria2",
			3003: "adguard-home",
			4004: "tuic",
			5005: "snell",
			6006: "docker-proxy",
		},
		ufwText: ufw,
		natText: nat,
	}
}

// topPortLabels returns the port node labels in order.
func topPortLabels(t *testing.T, st *state.State) []string {
	t.Helper()
	var labels []string
	for _, n := range st.Topology.Nodes {
		if n.Type == state.NodePort {
			labels = append(labels, n.Label)
		}
	}
	return labels
}

func exposureCollect(t *testing.T, r *mockRunner) *state.State {
	t.Helper()
	res, err := Collect(context.Background(), r, Options{
		ServicesPath: exposureServicesPath(t),
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	return res.State
}

// Scenario A/B: wildcard Nginx TCP/443 + Hysteria UDP/443.
// Nginx is direct_public (UFW allows 443/tcp). Hysteria 443/udp is the NAT
// backend of 20000-20099 so it becomes nat_ingress. Top-level ports: 443
// (nginx direct) + the three NAT ingress ranges (20000-20099, 20100-20199,
// 8554). 18453 is filtered, 8443/17414 are NAT backends (suppressed).
func TestExposureScenarioAB(t *testing.T) {
	r := exposureRunner(exposureUFW, exposureNAT, exposureSS)
	st := exposureCollect(t, r)

	labels := topPortLabels(t, st)
	want := []string{"443", "8554", "20000–20099", "20100–20199"}
	if len(labels) != len(want) {
		t.Fatalf("port labels = %v, want %v", labels, want)
	}

	// nginx exposure direct_public.
	ng := byID(t, st, "nginx")
	if ng.Listeners[0].Exposure != state.ExposureDirectPublic {
		t.Errorf("nginx exposure = %q, want direct_public", ng.Listeners[0].Exposure)
	}
	hy := byID(t, st, "hysteria2")
	if hy.Listeners[0].Exposure != state.ExposureNATIngress {
		t.Errorf("hysteria exposure = %q, want nat_ingress (backend of 20000-20099)", hy.Listeners[0].Exposure)
	}
}

// Scenario C: AdGuard 18453 wildcard but no firewall allow → internal/filtered,
// never an Internet port.
func TestExposureScenarioC(t *testing.T) {
	r := exposureRunner(exposureUFW, exposureNAT, exposureSS)
	st := exposureCollect(t, r)

	ag := byID(t, st, "adguard-home")
	if ag.Listeners[0].Exposure != state.ExposureInternal {
		t.Errorf("adguard 18453 exposure = %q, want internal (filtered)", ag.Listeners[0].Exposure)
	}
	for _, n := range st.Topology.Nodes {
		if n.Type == state.NodePort && n.Label == "18453" {
			t.Error("filtered 18453 must not be a top-level Internet port")
		}
	}
}

// Scenario D: UDP 20000-20099 → 443 → Hysteria2 renders as one range port.
// The 443/udp backend listener is suppressed (nat_ingress); 443/tcp (nginx)
// remains a direct_public top-level port in its own right.
func TestExposureScenarioD(t *testing.T) {
	r := exposureRunner(exposureUFW, exposureNAT, exposureSS)
	st := exposureCollect(t, r)

	labels := topPortLabels(t, st)
	found := false
	for _, l := range labels {
		if l == "20000–20099" {
			found = true
		}
	}
	if !found {
		t.Fatalf("range 20000-20099 not in ports: %v", labels)
	}

	// The NAT range node carries the backend target port 443.
	for _, n := range st.Topology.Nodes {
		if n.Type == state.NodePort && n.Label == "20000–20099" {
			if n.TargetPort != 443 {
				t.Errorf("range target port = %d, want 443", n.TargetPort)
			}
			if n.Exposure != state.ExposureNATIngress {
				t.Errorf("range exposure = %q, want nat_ingress", n.Exposure)
			}
		}
	}

	// Hysteria's 443/udp listener is the NAT backend → nat_ingress.
	hy := byID(t, st, "hysteria2")
	if len(hy.Listeners) != 1 || hy.Listeners[0].Exposure != state.ExposureNATIngress {
		t.Errorf("hysteria listeners = %+v", hy.Listeners)
	}
}

// Scenario F: 8554 → 17414 → Snell; 17414 firewall-allowed but is a REDIRECT
// target → suppressed. No duplicate Internet → 17414.
func TestExposureScenarioF(t *testing.T) {
	r := exposureRunner(exposureUFW, exposureNAT, exposureSS)
	st := exposureCollect(t, r)

	labels := topPortLabels(t, st)
	found8554, found17414 := false, false
	for _, l := range labels {
		if l == "8554" {
			found8554 = true
		}
		if l == "17414" {
			found17414 = true
		}
	}
	if !found8554 {
		t.Errorf("8554 NAT ingress missing: %v", labels)
	}
	if found17414 {
		t.Error("17414 must be suppressed (NAT target), even though firewall allows it")
	}

	sn := byID(t, st, "snell")
	if sn.Listeners[0].Exposure != state.ExposureNATIngress {
		t.Errorf("snell exposure = %q, want nat_ingress", sn.Listeners[0].Exposure)
	}
}

// Scenario F + force_direct_public: 17414 becomes a top-level port too.
func TestExposureScenarioFForceDirect(t *testing.T) {
	svcPath := writeServicesYAML(t, `services:
  - id: nginx
    name: Nginx
    systemd: { unit: nginx.service }
    restart_enabled: true
  - id: hysteria2
    name: Hysteria2
    systemd: { unit: hysteria-server.service }
    restart_enabled: true
  - id: adguard-home
    name: AdGuard Home
    systemd: { unit: adguard.service }
    restart_enabled: true
  - id: tuic
    name: TUIC
    systemd: { unit: tuic.service }
    restart_enabled: true
  - id: snell
    name: Snell
    systemd: { unit: snell.service }
    restart_enabled: true
    exposure:
      mode: nat-target
      force_direct_public: true
`)
	r := exposureRunner(exposureUFW, exposureNAT, exposureSS)
	res, err := Collect(context.Background(), r, Options{ServicesPath: svcPath})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	labels := topPortLabels(t, res.State)
	found17414 := false
	for _, l := range labels {
		if l == "17414" {
			found17414 = true
		}
	}
	if !found17414 {
		t.Errorf("17414 should be direct_public with force_direct_public override: %v", labels)
	}
}

// Scenario G: Docker loopback DNAT 127.0.0.1:3001 must never be a public port.
func TestExposureScenarioG(t *testing.T) {
	r := exposureRunner(exposureUFW, exposureNAT, exposureSS)
	st := exposureCollect(t, r)

	for _, n := range st.Topology.Nodes {
		if n.Type == state.NodePort && n.Label == "3001" {
			t.Error("docker loopback DNAT 3001 must not be a top-level port")
		}
	}
}

// Runtime change: removing the UDP/443 firewall allow must remove Hysteria's
// direct exposure (topology changes without rebuilding).
func TestExposureRuntimeChangeFirewall(t *testing.T) {
	ufwA := exposureUFW
	ufwB := `Status: active
Logging: on (low)
Default: deny (incoming), allow (outgoing), disabled (routed)
New profiles: skip

To                         Action      From
--                         ------      ----
22/tcp                     ALLOW IN    Anywhere
443/tcp                    ALLOW IN    Anywhere
853/tcp                    ALLOW IN    Anywhere
20000:20099/udp            ALLOW IN    203.0.113.10
20100:20199/udp            ALLOW IN    203.0.113.10
8554/udp                   ALLOW IN    203.0.113.10
17414/udp                  ALLOW IN    203.0.113.10
`
	// A: UDP/443 allowed → 443/udp direct_public listener exists.
	rA := exposureRunner(ufwA, exposureNAT, exposureSS)
	stA := exposureCollect(t, rA)

	// B: UDP/443 allow removed. 443/udp listener now filtered. But 443/tcp
	// (nginx) still allowed. Hysteria 443 listener becomes internal; its NAT
	// range 20000-20099 still points at 443.
	rB := exposureRunner(ufwB, exposureNAT, exposureSS)
	stB := exposureCollect(t, rB)

	hyA := byID(t, stA, "hysteria2")
	hyB := byID(t, stB, "hysteria2")
	if hyA.Listeners[0].Exposure == state.ExposureInternal {
		t.Error("fixture A: hysteria should NOT be internal")
	}
	if hyB.Listeners[0].Exposure != state.ExposureNATIngress && hyB.Listeners[0].Exposure != state.ExposureInternal {
		t.Errorf("fixture B: hysteria exposure = %q, want nat_ingress or internal", hyB.Listeners[0].Exposure)
	}
	if hyA.Listeners[0].Exposure == hyB.Listeners[0].Exposure {
		t.Error("runtime change: hysteria exposure did not change between fixtures")
	}
}

// Runtime change: NAT target moves 20000-20099 → 9443; topology must change.
func TestExposureRuntimeChangeNATTarget(t *testing.T) {
	natA := exposureNAT
	natB := `-P PREROUTING ACCEPT
-P INPUT ACCEPT
-P OUTPUT ACCEPT
-P POSTROUTING ACCEPT
-A PREROUTING -d 203.0.113.10/32 -p udp --dport 20000:20099 -j REDIRECT --to-ports 9443
-A PREROUTING -d 203.0.113.10/32 -p udp --dport 20100:20199 -j REDIRECT --to-ports 8443
-A PREROUTING -d 203.0.113.10/32 -p udp --dport 8554 -j REDIRECT --to-ports 17414
`
	// Fixture A: ss has Hysteria on 443.
	ssA := exposureSS
	// Fixture B: Hysteria actually listens on 9443 (backend moved).
	ssB := `tcp   LISTEN 0 511    0.0.0.0:443        0.0.0.0:*  users:(("nginx",pid=1001,fd=10))
udp   UNCONN 0 0      [::]:9443         [::]:*     users:(("hysteria",pid=2002,fd=7))
tcp   LISTEN 0 128    0.0.0.0:18453     0.0.0.0:*  users:(("adguard",pid=3003,fd=6))
udp   UNCONN 0 0      [::]:8443         [::]:*     users:(("tuic",pid=4004,fd=9))
udp   UNCONN 0 0      [::]:17414        [::]:*     users:(("snell",pid=5005,fd=11))
tcp   LISTEN 0 128    127.0.0.1:3001    0.0.0.0:*  users:(("docker-proxy",pid=6006,fd=12))
`
	rA := exposureRunner(exposureUFW, natA, ssA)
	stA := exposureCollect(t, rA)
	rB := exposureRunner(exposureUFW, natB, ssB)
	stB := exposureCollect(t, rB)

	hyA := byID(t, stA, "hysteria2")
	hyB := byID(t, stB, "hysteria2")
	if hyA.Listeners[0].Port != 443 {
		t.Errorf("fixture A hysteria port = %d, want 443", hyA.Listeners[0].Port)
	}
	if hyB.Listeners[0].Port != 9443 {
		t.Errorf("fixture B hysteria port = %d, want 9443", hyB.Listeners[0].Port)
	}
	// Both fixtures render the same NAT range 20000-20099 but the backend
	// service instance port differs.
	var instA, instB string
	for _, n := range stA.Topology.Nodes {
		if n.Type == state.NodeService && n.ServiceID == "hysteria2" {
			instA = n.ID
		}
	}
	for _, n := range stB.Topology.Nodes {
		if n.Type == state.NodeService && n.ServiceID == "hysteria2" {
			instB = n.ID
		}
	}
	if instA == instB {
		t.Error("runtime change: NAT target change did not change the topology")
	}
}

// Firewall unknown → wildcard listeners must NOT be auto-public.
func TestExposureFirewallUnknown(t *testing.T) {
	// No UFW output at all → visibility unknown.
	r := exposureRunner("", exposureNAT, exposureSS)
	st := exposureCollect(t, r)

	ng := byID(t, st, "nginx")
	if ng.Listeners[0].Exposure == state.ExposureDirectPublic {
		t.Error("nginx must not be direct_public when firewall is unknown")
	}
}
