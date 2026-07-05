#!/usr/bin/env bash
# scripts/build-deb.sh - build a webfilter .deb for one Linux binary already
# built by scripts/package-release.sh (which invokes this once per linux
# GOARCH). Requires dpkg-deb (present on any Debian/Ubuntu host, including
# the ubuntu-latest GitHub Actions runner) - it just packs files, so it
# doesn't need to match the host's own architecture.
#
# Usage: ./scripts/build-deb.sh VERSION ARCH BINARY OUT_DIR
#   VERSION  release version string (a leading "v" is stripped for the
#            Debian version field, e.g. v1.2.3 -> 1.2.3)
#   ARCH     Debian architecture: amd64 or arm64
#   BINARY   path to the built linux/ARCH webfilter binary
#   OUT_DIR  where to write the .deb
set -euo pipefail

VERSION="$1"
ARCH="$2"
BINARY="$3"
OUT_DIR="$4"

DEB_VERSION="${VERSION#v}"
if [[ -z "$DEB_VERSION" || "$DEB_VERSION" == "dev" ]]; then
  DEB_VERSION="0.0.0~dev"
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
DEB_DIR="$REPO_ROOT/packaging/deb"

STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

mkdir -p \
  "$STAGE/DEBIAN" \
  "$STAGE/opt/webfilter/config" \
  "$STAGE/opt/webfilter/policies" \
  "$STAGE/lib/systemd/system"

install -m 0755 "$BINARY" "$STAGE/opt/webfilter/webfilter"
install -m 0644 "$REPO_ROOT/config/settings.example.json" "$STAGE/opt/webfilter/config/settings.example.json"
install -m 0644 "$REPO_ROOT/policies/default.json.example" "$STAGE/opt/webfilter/policies/default.json.example"
if [[ -d "$REPO_ROOT/categories" ]]; then
  cp -R "$REPO_ROOT/categories" "$STAGE/opt/webfilter/categories"
fi
install -m 0644 "$REPO_ROOT/packaging/webfilter.service" "$STAGE/lib/systemd/system/webfilter.service"

install -m 0755 "$DEB_DIR/postinst" "$STAGE/DEBIAN/postinst"
install -m 0755 "$DEB_DIR/postrm" "$STAGE/DEBIAN/postrm"
sed -e "s/@VERSION@/$DEB_VERSION/" -e "s/@ARCH@/$ARCH/" "$DEB_DIR/control.in" > "$STAGE/DEBIAN/control"

mkdir -p "$OUT_DIR"
DEB_PATH="$OUT_DIR/webfilter_${DEB_VERSION}_${ARCH}.deb"
dpkg-deb --build --root-owner-group "$STAGE" "$DEB_PATH"
echo "[build-deb] wrote $DEB_PATH"
