# OpsCockpit

A lightweight single-host ops dashboard for one Linux VPS: current server
status, service status, a dynamic port-centric topology, and a restrained
health panel — all served by one Go binary with an embedded React frontend.

```
Linux Runtime → opscockpit collect → state.json → opscockpit serve → Web UI
```

The binary knows **no server topology**. After deployment it discovers the host
at runtime (systemd, /proc, cgroup, `ss`, Docker, config files, services.yaml)
and renders whatever is actually there.

- **Single binary.** No Node, Python, database, Redis, Prometheus, Grafana, or
  WebSocket server at runtime.
- **No database.** Just `services.yaml` (registry/override) and `state.json`
  (current Runtime Digital Twin).
- **Data-driven frontend.** React Flow + Dagre render `PortNode →
  ProtocolNode → ServiceNode`. New services appear without frontend changes.

## Architecture

See [ARCHITECTURE.md](ARCHITECTURE.md) for the full design.

## Build

```bash
# Go backend only (uses the committed frontend build)
go build ./...

# Full frontend rebuild (only needed when frontend sources change)
cd frontend && npm ci && npm run build
./scripts/check-frontend-dist.sh --fix
```

The Linux amd64 release artifact is built on demand via GitHub Actions
(`workflow_dispatch`) and downloaded as a single binary:
`opscockpit-linux-amd64` plus `.sha256` and `.tar.gz`.

## Run a mock demo (no real server)

Fixtures in `testdata/` simulate the runtime (ss output, /proc, cgroup v2,
systemd units). Run the whole pipeline against them:

```bash
./scripts/mockdemo.sh                 # Hysteria on UDP/443
./scripts/mockdemo.sh --fixture-b     # Hysteria on UDP/9443 — same binary!
```

Open http://localhost:8090. `--fixture-b` proves the topology is data-driven:
only the ss fixture changes, the UI now shows port 9443 instead of 443.

## Commands

```
opscockpit collect   — gather runtime state, write state.json
opscockpit serve     — serve the web UI from state.json
opscockpit discover  — show runtime listeners and registered services
opscockpit version   — print version
```

## services.yaml

`services.yaml` (see `configs/services.example.yaml`) is the service registry
**and override** — it is not a topology file. It supplies friendly names,
systemd units, config paths, version commands, restart permission, and health
hints. Ports and listeners always come from the runtime:

```yaml
services:
  - id: hysteria2
    name: Hysteria2
    systemd:
      unit: hysteria-server.service
    config_paths:
      - /etc/hysteria/config.yaml
    version:
      command: [/usr/local/bin/hysteria, version]
      timeout: 5s
    restart_enabled: true
```

## state.json

`state.json` is the current Runtime Digital Twin written by `opscockpit
collect`:

```jsonc
{
  "schema_version": 1,
  "generated_at": "...",
  "host": { "hostname": "...", "cpu": {}, "memory": {}, "disk": {}, "load": {} },
  "services": [{ "id": "...", "name": "...", "status": "...", "listeners": [] }],
  "health": { "status": "healthy", "stale": false },
  "topology": { "nodes": [], "edges": [] }
}
```

It never contains credentials. The schema is an allowlist; a validator rejects
any state that carries a `password`, `token`, `secret`, `private key`, `uuid`,
`api key`, cookie, or credential field before it is written.

## API

```
GET  /api/state
GET  /api/healthz
POST /api/services/{id}/restart
```

The restart endpoint resolves the service id against the services.yaml
allowlist — clients never submit unit names or shell commands. All responses
are `no-store`; the frontend polls with ETag/304 (no WebSocket).

## Release artifact

- `opscockpit-linux-amd64` — stripped, trimpath, `CGO_ENABLED=0`
- `opscockpit-linux-amd64.sha256`
- `opscockpit-linux-amd64.tar.gz` — binary + `services.example.yaml` + docs

## Attribution

OpsCockpit reuses the frontend topology engine (React Flow + Dagre layout and
component patterns) from [Pouzor/homelable](https://github.com/Pouzor/homelable)
(MIT), pinned at commit `f07f43686ec05f586bebe476b889a47137d2af2d`. See
[UPSTREAM.md](UPSTREAM.md) and [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

## Testing

```bash
go test ./...
go vet ./...
cd frontend && npm run typecheck && npm test && npm run build
```

Collectors are tested with fixtures/mocks so CI does not depend on a live host.

## Deployment

Production deployment will be handled separately.
