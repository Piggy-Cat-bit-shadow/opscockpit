#!/usr/bin/env bash
# mockdemo — run OpsCockpit entirely against fixture data.
#
# This demonstrates the core idea: the binary knows no topology. It reads
# runtime fixtures (ss output, /proc, cgroup, systemd) exactly like it would
# read a live host, and the topology + UI render whatever the fixtures say.
#
# Usage:
#   ./scripts/mockdemo.sh                # fixture-a: Hysteria on UDP/443
#   ./scripts/mockdemo.sh --fixture-b    # Hysteria on UDP/9443 (same binary!)
#   ./scripts/mockdemo.sh --port 9000
#
# The binary is not recompiled between --fixture-a and --fixture-b. Only the
# ss fixture changes — proving the topology is data-driven, not hardcoded.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PORT="${PORT:-8090}"
MODE="fixture-a"
BUILD_DIR="$(mktemp -d)"
WORK="$(mktemp -d)"
trap 'rm -rf "$BUILD_DIR" "$WORK"' EXIT

while [[ $# -gt 0 ]]; do
  case "$1" in
    --port) PORT="$2"; shift 2 ;;
    --fixture-a) MODE="fixture-a"; shift ;;
    --fixture-b) MODE="fixture-b"; shift ;;
    *) echo "unknown flag: $1" >&2; exit 1 ;;
  esac
done

echo "==> building opscockpit…"
(cd "$ROOT" && CGO_ENABLED=0 go build -trimpath -o "$BUILD_DIR/opscockpit" ./cmd/opscockpit)

mkdir -p "$WORK/etc/nginx" "$WORK/etc/hysteria" "$WORK/etc/tuic" "$WORK/etc/AdGuardHome" "$WORK/etc/xray"
printf 'user nginx;\n' > "$WORK/etc/nginx/nginx.conf"
printf 'listen: 443\npassword: demo\n' > "$WORK/etc/hysteria/config.yaml"
printf '{"inbounds":[]}\n' > "$WORK/etc/tuic/config.json"
printf 'bind_host: 0.0.0.0\n' > "$WORK/etc/AdGuardHome/AdGuardHome.yaml"
printf '{"log":{}}\n' > "$WORK/etc/xray/config.json"

# ss fixture drives the topology. Only the Hysteria port differs A vs B.
SS_FIXTURE="$ROOT/testdata/ss-live.txt"
if [[ "$MODE" == "fixture-b" ]]; then
  sed 's/\[::\]:443/[::]:9443/' "$SS_FIXTURE" > "$WORK/ss.txt"
else
  cp "$SS_FIXTURE" "$WORK/ss.txt"
fi

echo "==> collect (mode: $MODE)…"
OPSCOCKPIT_SS_FILE="$WORK/ss.txt" \
OPSCOCKPIT_UNIT_DIR="$ROOT/testdata/systemd" \
  "$BUILD_DIR/opscockpit" collect \
  -services "$ROOT/configs/services.example.yaml" \
  -out "$WORK/state.json" \
  -root "$ROOT/testdata" \
  -cpu-interval-ms 0

echo "==> topology ports in state.json:"
grep -oE '"(443|853|8443|9443|18444)"' "$WORK/state.json" | sort | uniq -c

echo "==> serving on :$PORT"
exec "$BUILD_DIR/opscockpit" serve -state "$WORK/state.json" -listen ":$PORT" \
  -services "$ROOT/configs/services.example.yaml"
