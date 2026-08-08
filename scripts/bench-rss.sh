#!/usr/bin/env bash
# bench-rss.sh — reference RSS measurement for `opscockpit serve`.
#
# NOTE: this is a development-environment reference, not a production VPS
# measurement. Use it to sanity-check the serve memory target (15–25 MB, ≤30 MB
# expected, >40 MB signals an architecture problem).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$(mktemp -d)/opscockpit"
PORT="${PORT:-8092}"

echo "==> building opscockpit…"
(cd "$ROOT" && CGO_ENABLED=0 go build -trimpath -o "$BIN" ./cmd/opscockpit)

echo "==> generating a mock state.json…"
(cd "$ROOT" && go run ./cmd/mockstate -out /tmp/opscockpit-bench-state.json >/dev/null)

echo "==> starting serve on :$PORT…"
"$BIN" serve -state /tmp/opscockpit-bench-state.json -listen ":$PORT" >/dev/null 2>&1 &
SRV=$!
trap 'kill $SRV 2>/dev/null || true' EXIT

sleep 2

# RSS after warmup. PID RSS in KB on Linux, bytes on macOS.
pid=$(pgrep -f "opscockpit serve.*:$PORT" | head -1)
if [[ -z "$pid" ]]; then pid=$SRV; fi

if command -v ps >/dev/null 2>&1; then
  rss_kb=$(ps -o rss= -p "$pid" | tr -d ' ')
  rss_mb=$(awk "BEGIN { printf \"%.1f\", $rss_kb/1024 }")
  echo "serve RSS (reference): ${rss_kb} KB = ${rss_mb} MB"
fi

echo "==> hitting /api/state a few times…"
for _ in 1 2 3 4 5; do curl -s -o /dev/null "http://localhost:$PORT/api/state"; done

if command -v ps >/dev/null 2>&1; then
  rss_kb=$(ps -o rss= -p "$pid" | tr -d ' ')
  rss_mb=$(awk "BEGIN { printf \"%.1f\", $rss_kb/1024 }")
  echo "serve RSS after requests (reference): ${rss_kb} KB = ${rss_mb} MB"
  echo
  echo "Target: 15–25 MB. Expected ≤30 MB. >40 MB = investigate."
fi
