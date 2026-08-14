#!/usr/bin/env bash
# capture_screenshots.sh - render the management web UI to PNGs in screenshots/.
#
# Runs the whole thing against a throwaway data directory seeded by
# scripts/seed_sample_data.go, so the repo's real config/, policies/ and logs/
# are never touched and the screenshots always show the same sample data.
#
# Usage:
#   bash scripts/capture_screenshots.sh [--out DIR] [--port N] [--keep]
#
#   --out DIR   where to write PNGs (default: <repo>/screenshots)
#   --port N    mgmt port for the temporary server (default: 8099)
#   --keep      don't delete the temporary data directory on exit
#
# Requires a Chromium/Chrome binary. Set CHROME=/path/to/chromium to point at
# one explicitly; otherwise the usual names are tried on PATH.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="$REPO_ROOT/screenshots"
PORT=8099
KEEP=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --out)  OUT_DIR="$2"; shift 2 ;;
    --port) PORT="$2"; shift 2 ;;
    --keep) KEEP=1; shift ;;
    -h|--help) sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

# ---------------------------------------------------------------------------
# locate a browser
# ---------------------------------------------------------------------------
if [[ -n "${CHROME:-}" ]]; then
  BROWSER="$CHROME"
else
  BROWSER=""
  for c in chromium chromium-browser google-chrome google-chrome-stable chrome; do
    if command -v "$c" >/dev/null 2>&1; then BROWSER="$(command -v "$c")"; break; fi
  done
fi
if [[ -z "$BROWSER" || ! -x "$BROWSER" ]]; then
  cat >&2 <<'MSG'
error: no Chromium/Chrome binary found.

Install one (e.g. `pacman -S chromium`, `apt install chromium`) or point at an
existing build:

    CHROME=/path/to/chromium bash scripts/capture_screenshots.sh

Screenshots need a real browser engine: the UI is an Alpine.js app that fetches
its data from the management API, so a static HTML dump would be empty.
MSG
  exit 1
fi

command -v go >/dev/null 2>&1 || { echo "error: go toolchain not found in PATH" >&2; exit 1; }

# ---------------------------------------------------------------------------
# build + seed + serve
# ---------------------------------------------------------------------------
DATA_DIR="$(mktemp -d "${TMPDIR:-/tmp}/webfilter-screenshots.XXXXXX")"
BIN="$DATA_DIR/webfilter"
SERVER_PID=""

cleanup() {
  [[ -n "$SERVER_PID" ]] && kill "$SERVER_PID" 2>/dev/null || true
  if [[ "$KEEP" -eq 1 ]]; then
    echo "[shots] kept data dir: $DATA_DIR"
  else
    rm -rf "$DATA_DIR"
  fi
}
trap cleanup EXIT

echo "[shots] building webfilter ..."
(cd "$REPO_ROOT" && CGO_ENABLED=0 go build -o "$BIN" ./cmd/webfilter)

echo "[shots] seeding sample data ..."
(cd "$REPO_ROOT" && go run scripts/seed_sample_data.go -dir "$DATA_DIR" -mgmt-port "$PORT" >/dev/null)

echo "[shots] starting server on 127.0.0.1:$PORT ..."
"$BIN" run --settings "$DATA_DIR/config/settings.json" >"$DATA_DIR/server.log" 2>&1 &
SERVER_PID=$!

for _ in $(seq 1 50); do
  if curl -sf "http://127.0.0.1:$PORT/api/status" >/dev/null 2>&1; then break; fi
  sleep 0.2
done
if ! curl -sf "http://127.0.0.1:$PORT/api/status" >/dev/null 2>&1; then
  echo "error: server did not come up; log follows" >&2
  cat "$DATA_DIR/server.log" >&2
  exit 1
fi

mkdir -p "$OUT_DIR"

# shoot <name> <path> <width> <height>
shoot() {
  local name="$1" path="$2" width="$3" height="$4"
  echo "[shots]   $name.png  (${width}x${height})"
  "$BROWSER" \
    --headless --no-sandbox --disable-gpu --disable-dev-shm-usage \
    --hide-scrollbars --force-device-scale-factor=1 \
    --virtual-time-budget=10000 \
    --window-size="$width,$height" \
    --screenshot="$OUT_DIR/$name.png" \
    "http://127.0.0.1:$PORT/$path" >/dev/null 2>&1
}

echo "[shots] capturing ..."
# Heights are per-page: enough to show the whole view without a lot of dead
# space under it.
shoot dashboard      "index.html"                       1440 1000
shoot policies       "policies.html"                    1440  620
shoot policy-editor  "policy-editor.html?name=kids"     1440 1400
shoot logs           "logs.html"                        1440 1100
shoot analytics      "analytics.html"                   1440 1250
shoot tools          "tools.html"                       1440 1500
shoot settings       "settings.html"                    1440 1400

echo "[shots] done -> $OUT_DIR"
ls -1 "$OUT_DIR"
