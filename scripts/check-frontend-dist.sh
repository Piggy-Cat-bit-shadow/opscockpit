#!/usr/bin/env bash
# check-frontend-dist.sh — verify the committed frontend build is in sync with
# the frontend source.
#
# The Go binary embeds internal/web/dist/ (committed). If the frontend source
# changes but the committed dist is not rebuilt, this script fails.
#
# It compares a source hash (computed from the frontend source files, excluding
# node_modules/dist) against the hash recorded in frontend-build.json when the
# dist was committed.
#
# Usage:
#   ./scripts/check-frontend-dist.sh        # check-only (CI safe)
#   ./scripts/check-frontend-dist.sh --fix  # rebuild + update fingerprint
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
FINGERPRINT="$ROOT/frontend-build.json"
UPSTREAM_COMMIT="f07f43686ec05f586bebe476b889a47137d2af2d"

source_hash() {
  # Content-only, cross-platform deterministic hash (macOS BSD find/tar and
  # Linux GNU tools differ in archive headers — never use tar here). Sort the
  # path list, hash each file's content, then hash the aggregate output.
  (cd "$ROOT" && {
    echo "frontend/package.json"
    echo "frontend/vite.config.ts"
    echo "frontend/index.html"
    echo "frontend/tsconfig.json"
    echo "frontend/tsconfig.app.json"
    echo "frontend/tsconfig.node.json"
    find frontend/src -type f
  } | sort | xargs shasum -a 256 | shasum -a 256 | cut -d' ' -f1)
}

dist_hash() {
  # Hash of the committed dist that Go embeds (same content-only pattern).
  (cd "$ROOT" && find internal/web/dist -type f | sort | xargs shasum -a 256 | shasum -a 256 | cut -d' ' -f1)
}

write_fingerprint() {
  local src dist
  src="$(source_hash)"
  dist="$(dist_hash)"
  built_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  cat > "$FINGERPRINT" <<EOF
{
  "source_hash": "$src",
  "dist_hash": "$dist",
  "built_at": "$built_at",
  "homelable_upstream_commit": "$UPSTREAM_COMMIT"
}
EOF
  echo "wrote $FINGERPRINT"
}

refresh_fingerprint() {
  write_fingerprint
  echo "done. commit frontend-build.json."
  exit 0
}

if [[ "${1:-}" == "--fix" ]]; then
  echo "rebuilding frontend…"
  (cd "$ROOT/frontend" && npm ci --silent && npm run build)
  rm -rf "$ROOT/internal/web/dist"
  mkdir -p "$ROOT/internal/web/dist"
  cp -R "$ROOT/frontend/dist/." "$ROOT/internal/web/dist/"
  refresh_fingerprint
fi

if [[ "${1:-}" == "--refresh" ]]; then
  refresh_fingerprint
fi

if [[ ! -f "$FINGERPRINT" ]]; then
  echo "error: $FINGERPRINT missing. Run $0 --fix first." >&2
  exit 1
fi

current_src="$(source_hash)"
recorded_src="$(python3 -c "import json;print(json.load(open('$FINGERPRINT'))['source_hash'])")"

if [[ "$current_src" != "$recorded_src" ]]; then
  echo "error: frontend source changed but committed dist is stale." >&2
  echo "  current source hash: $current_src" >&2
  echo "  recorded source hash: $recorded_src" >&2
  echo "Run: ./scripts/check-frontend-dist.sh --fix" >&2
  exit 1
fi

echo "ok: committed frontend dist is in sync with source."
