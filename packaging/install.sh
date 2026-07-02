#!/usr/bin/env bash
# packaging/install.sh - install a prebuilt webfilter binary as a systemd
# service on Linux.
#
# Usage:
#   sudo ./install.sh [--mode run|split] [--prefix DIR] [--binary PATH]
#
#   --mode run     install the combined webfilter.service (proxy + mgmt in
#                  one process, matching `webfilter run`). Default.
#   --mode split   install webfilter-proxy.service + webfilter-mgmt.service
#                  as two independent units instead, for operators who want
#                  process isolation between the two components.
#   --prefix DIR   install location (default: /opt/webfilter)
#   --binary PATH  path to a prebuilt webfilter binary
#                  (default: <repo-root>/webfilter - build it first with
#                  `go build -o webfilter ./cmd/webfilter`)
#
set -euo pipefail

MODE="run"
PREFIX="/opt/webfilter"
BINARY=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --mode)   MODE="$2"; shift 2 ;;
    --prefix) PREFIX="$2"; shift 2 ;;
    --binary) BINARY="$2"; shift 2 ;;
    -h|--help)
      sed -n '2,17p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *) echo "unknown arg: $1" >&2; exit 2 ;;
  esac
done

if [[ "$MODE" != "run" && "$MODE" != "split" ]]; then
  echo "error: --mode must be 'run' or 'split'" >&2
  exit 2
fi

if [[ "$(id -u)" -ne 0 ]]; then
  echo "error: must be run as root (sudo ./install.sh ...)" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"

if [[ -z "$BINARY" ]]; then
  BINARY="$REPO_ROOT/webfilter"
fi
if [[ ! -f "$BINARY" ]]; then
  echo "error: binary not found at $BINARY" >&2
  echo "       build it first: go build -o webfilter ./cmd/webfilter, or pass --binary PATH" >&2
  exit 1
fi

echo "[install] creating system user 'webfilter' ..."
if ! id -u webfilter &>/dev/null; then
  useradd --system --home-dir "$PREFIX" --shell /usr/sbin/nologin webfilter
else
  echo "[install] user 'webfilter' already exists"
fi

echo "[install] creating $PREFIX ..."
mkdir -p "$PREFIX/config" "$PREFIX/policies" "$PREFIX/certs" "$PREFIX/categories" "$PREFIX/logs" "$PREFIX/data"

echo "[install] installing binary ..."
install -m 0755 "$BINARY" "$PREFIX/webfilter"

# internal/classify/onnxrt dynamically loads the onnxruntime shared library
# next to the running executable if ONNXRUNTIME_SHARED_LIBRARY isn't set -
# a release archive bundles libonnxruntime.so alongside the binary (see
# scripts/package-release.sh), so copy it into place too if present.
ORT_LIB="$(dirname "$BINARY")/libonnxruntime.so"
if [[ -f "$ORT_LIB" ]]; then
  install -m 0755 "$ORT_LIB" "$PREFIX/libonnxruntime.so"
  echo "[install] installed libonnxruntime.so"
else
  echo "[install] warning: libonnxruntime.so not found next to $BINARY - the text classifier's ML stage will fail to load unless ONNXRUNTIME_SHARED_LIBRARY points at one (the image classifier is unaffected, it's pure Go)" >&2
fi

if [[ ! -f "$PREFIX/config/settings.json" ]] && [[ -f "$REPO_ROOT/config/settings.example.json" ]]; then
  cp "$REPO_ROOT/config/settings.example.json" "$PREFIX/config/settings.json"
  echo "[install] seeded $PREFIX/config/settings.json from settings.example.json"
fi
if [[ ! -f "$PREFIX/policies/default.json" ]] && [[ -f "$REPO_ROOT/policies/default.json.example" ]]; then
  cp "$REPO_ROOT/policies/default.json.example" "$PREFIX/policies/default.json"
  echo "[install] seeded $PREFIX/policies/default.json from default.json.example"
fi

chown -R webfilter:webfilter "$PREFIX"

echo "[install] installing systemd unit(s) (mode: $MODE) ..."
UNITS=()
if [[ "$MODE" == "run" ]]; then
  cp "$SCRIPT_DIR/webfilter.service" /etc/systemd/system/webfilter.service
  UNITS=(webfilter.service)
else
  cp "$SCRIPT_DIR/webfilter-proxy.service" /etc/systemd/system/webfilter-proxy.service
  cp "$SCRIPT_DIR/webfilter-mgmt.service" /etc/systemd/system/webfilter-mgmt.service
  UNITS=(webfilter-proxy.service webfilter-mgmt.service)
fi

systemctl daemon-reload
for u in "${UNITS[@]}"; do
  systemctl enable "$u"
done

echo ""
echo "[install] done. Next steps:"
echo "  1. Review/edit $PREFIX/config/settings.json"
echo -n "  2. Start:  "
for u in "${UNITS[@]}"; do echo -n "systemctl start $u; "; done
echo ""
echo "  3. Web UI: http://<host>:8000 (default mgmt_port)"
echo "  4. Trust the generated CA cert once the service has run once: $PREFIX/certs/ca.crt"
echo ""
