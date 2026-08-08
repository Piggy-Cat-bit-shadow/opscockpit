# Architecture

OpsCockpit is a single Go binary that observes one Linux host and renders its
current state as a port-centric topology in a browser. It has no database, no
long-running companion services, and no hardcoded topology.

## The core idea

```
Linux Runtime
     │  (ss, /proc, cgroup, systemd, Docker, config files)
     ▼
opscockpit collect
     │  (gather + map + generate)
     ▼
state.json          ← the Runtime Digital Twin (current state only)
     │
opscockpit serve
     │  (embedded React frontend, reads GET /api/state)
     ▼
Web UI
```

### Binary is a fixed program
`opscockpit` knows nothing about any specific server. It ships with generic
collectors, a generic topology generator, and a generic frontend.

### Topology is runtime data
Every poll, the collectors read the live host: listening sockets via `ss`,
memory via cgroup v2 / proc, unit state via `systemctl show`, process mapping
via PID → cgroup → unit. Those facts produce `state.json`.

### Frontend is data-driven
The React frontend only understands `PortNode`, `ProtocolNode`, `ServiceNode`
and edges. It renders whatever `state.json` contains. A brand-new service that
appears at runtime (say, a RustDesk relay) shows up with zero frontend changes.

### services.yaml is an override, not a topology database
It declares which business services are worth showing and supplies hints:
friendly name, systemd unit, config path, version command, restart permission,
health requirements. It does NOT declare ports or listeners — those come from
the runtime. If a service moves from UDP/443 to UDP/9443, nothing in the code,
frontend, or CI changes; the collector discovers it automatically.

### state.json is a Runtime Digital Twin
A single JSON document holding only the current state:

```jsonc
{
  "schema_version": 1,
  "generated_at": "...",
  "collector_version": "...",
  "collect_duration_ms": 42,
  "host": { "hostname": "...", "cpu": {...}, "memory": {...}, "disk": {...}, "load": {...} },
  "services": [ { "id": "...", "name": "...", "status": "...", "listeners": [...] } ],
  "health": { "status": "...", "stale": false, ... },
  "topology": { "nodes": [...], "edges": [...] }
}
```

## Runtime truth priority

1. **Linux Runtime Truth** — actual listeners (`ss`), actual memory (cgroup),
   actual unit state (`systemctl show`).
2. **Current effective config** — config file names/paths as they exist.
3. **services.yaml override** — friendly names, units, restart permission.

A listener is mapped to a service through the chain:

```
Socket  →  PID  →  cgroup  →  systemd unit  →  service id
UDP/443 →  1234 →  hysteria-server.service  →  hysteria2
```

## Collector modules

| Module | Source | Purpose |
|---|---|---|
| `host` | `/proc/stat`, `/proc/meminfo`, `/proc/loadavg`, `/proc/uptime`, `statfs` | CPU %, RAM, swap, disk, load, uptime, hostname |
| `systemd` | `systemctl show --property=...` | ActiveState, SubState, MainPID, ControlGroup, ExecStart |
| `cgroup` | cgroup v2 `memory.current`, `/proc/<pid>/status` VmRSS | per-service memory (cgroup → PID-sum → MainPID fallback) |
| `listener` | `ss -H -lntup` | protocol, bind address, port, pid, process; public vs internal |
| `docker` | `docker ps` / `docker inspect` | container id, name, image, status, published ports, memory, labels |
| `version` | configured argv (never a shell string), with timeout | service version string |
| `nginx` | `nginx -T` (minimal parser) | listen + proxy_pass → dependency edges |

Every collector reads through an interface (a `Source` or `Runner`) so tests
feed fixtures instead of depending on the CI runner's live state.

## Topology generator

The generator is pure and deterministic: the same runtime + services always
produce the same nodes and edges in the same order. Ports sort ascending; a
port's protocols sort TCP before UDP.

```
Internet
   │
  443
 ┌─┴─────────┐
TCP          UDP
 │            │
Nginx      Hysteria2
 │
Xray   ← dependency edge (Nginx proxy_pass → 127.0.0.1:18444)
```

- Internal (loopback) listeners never become top-level Internet ports.
- A service may appear as several node instances (e.g.
  `adguard-home@tcp:853`, `adguard-home@udp:853`) but remains a single
  service object.
- Dependency edges carry `evidence.source` and `evidence.confidence`
  (`runtime_listener`, `nginx_proxy_pass`, `docker_port`, `manual_override`,
  …). Dependencies are never guessed from service names.

## Health model

Statuses: `healthy`, `warning`, `failed`, `unknown`, `stale`.

- Version unknown is **not** a fault.
- Config path override missing → `warning`.
- Registered service active but a required listener is missing → `failed`.
- Unit not active → `failed`.
- `stale` (state older than 2.5 × collect interval) wins over everything.

## API

```
GET  /api/state                 — the Runtime Digital Twin
GET  /api/healthz               — liveness / stale probe
POST /api/services/{id}/restart — allowlist restart (id only)
```

- `net/http` only. No framework.
- No WebSocket: the frontend polls (8 s foreground, 45 s hidden) with ETag /
  304. All API responses are `Cache-Control: no-store`.
- Restart never accepts a unit name, container name, or shell command from the
  client. The backend resolves the service id against the services.yaml
  allowlist.
- No database, no history, no time series, no metrics DB.

## Single binary

- Frontend is built once (`npm ci && vite build`) and committed under
  `internal/web/dist/`, embedded with `go:embed`.
- Runtime = one `opscockpit` binary. No Node.js, Vite, Python, FastAPI, SQLite,
  Redis, Prometheus, or Grafana is required in production.
- Serving target: 15–25 MB RSS (reference only; see benchmark script).

## Deployment

Production deployment (systemd unit, listening socket, reverse proxy) is
handled in a separate phase. See `deploy/` for reference templates only.
