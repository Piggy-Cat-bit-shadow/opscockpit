# testdata — mock Linux runtime

OpsCockpit is developed and tested against this fixture tree so CI never
depends on a live host. The fixtures mirror a realistic VPS:

| Listener | PID | Service |
|---|---|---|
| 0.0.0.0:443/TCP | 1001 | Nginx |
| [::]:443/UDP | 2002 | Hysteria2 |
| [::]:8443/UDP | 3003 | TUIC |
| 0.0.0.0:853/TCP + UDP | 4004 | AdGuard Home (one service object) |
| 127.0.0.1:18444/TCP | 5005 | Xray (internal — never a top-level port) |

## Layout

```
testdata/
  proc/                  → /proc   (meminfo, stat, loadavg, uptime, hostname, per-PID status)
  sys/fs/cgroup/...      → cgroup v2 memory.current + cgroup.procs per unit
  systemd/*.service      → `systemctl show` output per unit
  ss-live.txt            → `ss -H -lntup` output
  etc/                   → config files for config-exists checks
```

## Use with the real binary

The production binary supports fixture mode via environment variables (no code
changes to the collect pipeline):

```bash
OPSCOCKPIT_SS_FILE=testdata/ss-live.txt \
OPSCOCKPIT_UNIT_DIR=testdata/systemd \
opscockpit collect -services configs/services.example.yaml -root testdata -out state.json
```

`./scripts/mockdemo.sh` wraps this: `--fixture-a` (Hysteria on UDP/443) and
`--fixture-b` (Hysteria on UDP/9443) serve the same binary against different
fixtures — proving the topology is data-driven.
