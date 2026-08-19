#!/usr/bin/env bash
# packaging/install.sh - install a prebuilt webfilter binary as a systemd
# service on Linux.
#
# Usage:
#   sudo ./install.sh [--mode run|split] [--prefix DIR] [--binary PATH]
#                     [--tun2socks|--no-tun2socks]
#
#   --mode run     install the combined webfilter.service (proxy + mgmt in
#                  one process, matching `webfilter run`). Default.
#   --mode split   install webfilter-proxy.service + webfilter-mgmt.service
#                  as two independent units instead, for operators who want
#                  process isolation between the two components.
#   --prefix DIR   install location (default: /opt/webfilter)
#   --binary PATH  path to a prebuilt webfilter binary
#                  (default: a `webfilter` binary next to this script, i.e.
#                  the layout of a downloaded release archive; falls back to
#                  <repo-root>/webfilter for an in-repo dev checkout - build
#                  it first with `go build -o webfilter ./cmd/webfilter`)
#   --tun2socks    install the tun2socks.conf systemd drop-in, which grants
#                  the service CAP_NET_ADMIN/CAP_NET_RAW so TUN capture can
#                  start without running the whole engine as root.
#   --no-tun2socks never install that drop-in.
#                  The default is auto: install it exactly when the settings
#                  file being installed has tun2socks enabled.
#
set -euo pipefail

MODE="run"
PREFIX="/opt/webfilter"
BINARY=""
TUN2SOCKS="auto"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --mode)   MODE="$2"; shift 2 ;;
    --prefix) PREFIX="$2"; shift 2 ;;
    --binary) BINARY="$2"; shift 2 ;;
    --tun2socks)    TUN2SOCKS="yes"; shift ;;
    --no-tun2socks) TUN2SOCKS="no";  shift ;;
    -h|--help)
      sed -n '2,25p' "$0" | sed 's/^# \{0,1\}//'
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

# Two layouts are supported: a downloaded release archive, where install.sh
# sits flat next to the webfilter binary and the example config/policy files
# (see scripts/package-release.sh), and an in-repo dev checkout, where
# install.sh lives in packaging/ and the binary/examples are one directory
# up (repo root / config / policies). Prefer the release layout since that's
# how most users actually run this script.
if [[ -z "$BINARY" ]]; then
  if [[ -f "$SCRIPT_DIR/webfilter" ]]; then
    BINARY="$SCRIPT_DIR/webfilter"
  else
    BINARY="$REPO_ROOT/webfilter"
  fi
fi
if [[ ! -f "$BINARY" ]]; then
  echo "error: binary not found at $BINARY" >&2
  echo "       build it first: go build -o webfilter ./cmd/webfilter, or pass --binary PATH" >&2
  exit 1
fi

SETTINGS_EXAMPLE="$SCRIPT_DIR/settings.example.json"
[[ -f "$SETTINGS_EXAMPLE" ]] || SETTINGS_EXAMPLE="$REPO_ROOT/config/settings.example.json"
POLICY_EXAMPLE="$SCRIPT_DIR/default.json.example"
[[ -f "$POLICY_EXAMPLE" ]] || POLICY_EXAMPLE="$REPO_ROOT/policies/default.json.example"

echo "[install] creating system user 'webfilter' ..."
if ! id -u webfilter &>/dev/null; then
  useradd --system --home-dir "$PREFIX" --shell /usr/sbin/nologin webfilter
else
  echo "[install] user 'webfilter' already exists"
fi

echo "[install] creating $PREFIX ..."
# bin/ is where `webfilter tun2socks download` installs the tun2socks binary
# (beside the executable). Pre-create it so the download works as the
# unprivileged service user without needing to create a dir under $PREFIX.
mkdir -p "$PREFIX/config" "$PREFIX/policies" "$PREFIX/certs" "$PREFIX/categories" "$PREFIX/logs" "$PREFIX/data" "$PREFIX/bin"

echo "[install] installing binary ..."
install -m 0755 "$BINARY" "$PREFIX/webfilter"

if [[ ! -f "$PREFIX/config/settings.json" ]] && [[ -f "$SETTINGS_EXAMPLE" ]]; then
  cp "$SETTINGS_EXAMPLE" "$PREFIX/config/settings.json"
  echo "[install] seeded $PREFIX/config/settings.json from settings.example.json"
fi
if [[ ! -f "$PREFIX/policies/default.json" ]] && [[ -f "$POLICY_EXAMPLE" ]]; then
  cp "$POLICY_EXAMPLE" "$PREFIX/policies/default.json"
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

# TUN capture needs CAP_NET_ADMIN (and CAP_NET_RAW when interface_name is set).
# Only the unit that actually supervises tun2socks gets it: `webfilter mgmt`
# never does, so in split mode the mgmt unit is deliberately left alone.
if [[ "$MODE" == "run" ]]; then
  TUN_UNIT="webfilter.service"
else
  TUN_UNIT="webfilter-proxy.service"
fi
TUN_DROPIN="/etc/systemd/system/$TUN_UNIT.d/10-tun2socks.conf"

if [[ "$TUN2SOCKS" == "auto" ]]; then
  # Ask the app to parse its own settings rather than grepping JSON here. A
  # binary that can't exec (e.g. cross-built for another arch) just falls
  # through to "not enabled", which is the safe default.
  if "$PREFIX/webfilter" tun2socks status --settings "$PREFIX/config/settings.json" 2>/dev/null \
      | grep -qE '^enabled: *true'; then
    TUN2SOCKS="yes"
  else
    TUN2SOCKS="no"
  fi
fi

if [[ "$TUN2SOCKS" == "yes" ]]; then
  echo "[install] tun2socks is enabled: granting $TUN_UNIT CAP_NET_ADMIN/CAP_NET_RAW ..."
  # The drop-in ships with the default prefix baked into its ExecStopPost path,
  # so a --prefix install has to be rewritten or the teardown hook would point
  # at a binary that isn't there.
  mkdir -p "$(dirname "$TUN_DROPIN")"
  sed "s#/opt/webfilter#$PREFIX#g" "$SCRIPT_DIR/tun2socks.conf" > "$TUN_DROPIN"
  chmod 0644 "$TUN_DROPIN"
  echo "[install]   -> $TUN_DROPIN"
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
if [[ "$TUN2SOCKS" != "yes" ]]; then
  echo ""
  echo "  TUN capture is not enabled. To turn it on later:"
  echo "    - enable tun2socks in $PREFIX/config/settings.json (or the Settings page)"
  echo "    - sudo -u webfilter $PREFIX/webfilter tun2socks download"
  echo "    - sudo ./install.sh --tun2socks   (grants $TUN_UNIT the needed capabilities)"
elif [[ ! -x "$PREFIX/bin/tun2socks" ]]; then
  echo ""
  echo "  TUN capture is enabled but its binary is missing. Install it with:"
  echo "    sudo -u webfilter $PREFIX/webfilter tun2socks download"
fi
echo ""
