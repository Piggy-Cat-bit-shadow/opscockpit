# OpsCockpit deployment templates — REFERENCE ONLY

These templates are for the future deployment phase. They are NOT applied by
this repository or its CI. Actual production setup (user creation, directory
layout, Nginx/UFW/Docker integration) is a separate phase performed
deliberately, never automatically.

## Files

| Template | Purpose |
|---|---|
| `opscockpit-serve.service` | non-root web service (`opscockpit` / `opscockpit-web`) serving the unix socket |
| `opscockpit-collect.service` | root oneshot that refreshes `state.json` |
| `opscockpit-collect.timer` | runs collect every ~30 s (OnBootSec=10s, OnUnitActiveSec=30s) |
| `sudoers.d-opscockpit` | lets the web user run only the fixed restart-helper |

## File permissions

| Path | Owner | Mode |
|---|---|---|
| `/etc/opscockpit/` | `root:root` | `0755` |
| `/etc/opscockpit/services.yaml` | `root:root` | `0600` or `0640` (never group/world writable) |
| `/var/lib/opscockpit/` | `root:opscockpit-web` | `0750` |
| `/var/lib/opscockpit/state.json` | `root:opscockpit-web` | `0640` |
| `/run/opscockpit/` (RuntimeDirectory) | `opscockpit:opscockpit-web` | `0750` |
| `/run/opscockpit/opscockpit.sock` | `opscockpit:opscockpit-web` | `0660` |

The Nginx worker user must be added to the `opscockpit-web` group so it can
proxy the unix socket. Do NOT guess the Nginx username in code — detect the
actual user at deployment time.

## Important caveats

- **`NoNewPrivileges=true` must NOT be set on the serve unit.** The web user
  needs `sudo -n` privilege escalation to the root restart-helper; the kernel
  blocks that when NoNewPrivileges is on.
- The sudoers `*` wildcard is NOT an argument-count validator. Real safety
  comes from the helper: exactly one positional id, `^[a-z0-9][a-z0-9._-]*$`,
  root-owned registry verification, and internal fixed services path.
- The restart helper re-reads `/etc/opscockpit/services.yaml` itself; serve
  never passes a services path.
