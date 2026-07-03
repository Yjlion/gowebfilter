#!/usr/bin/env bash
# scripts/package-release.sh - cross-compile every release target and bundle
# each into the tarball/zip a GitHub release would attach, matching the
# GOOS/GOARCH matrix .github/workflows/ci.yml already builds+vets+tests.
#
# Usage: ./scripts/package-release.sh [VERSION] [OUT_DIR]
#   VERSION  embedded via -ldflags and used in archive names
#            (default: `git describe --tags --always --dirty`, or "dev")
#   OUT_DIR  where to write archives (default: ./dist)
#
# Both classifiers are pure Go and embedded, so release builds use
# CGO_ENABLED=0 and do not bundle any native ML runtime libraries.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
cd "$REPO_ROOT"

VERSION="${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
OUT_DIR="${2:-$REPO_ROOT/dist}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

LDFLAGS="-s -w"
LDFLAGS="$LDFLAGS -X github.com/yjlion/gowebfilter/internal/version.Version=${VERSION}"
LDFLAGS="$LDFLAGS -X github.com/yjlion/gowebfilter/internal/version.Commit=${COMMIT}"
LDFLAGS="$LDFLAGS -X github.com/yjlion/gowebfilter/internal/version.BuildDate=${BUILD_DATE}"

# goos:goarch:binary-extension
TARGETS=(
  "windows:amd64:.exe"
  "linux:amd64:"
  "linux:arm64:"
)

rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR"

if [[ ! -f categories/index.json ]]; then
  echo "[package] categories/index.json missing; downloading category lists ..."
  CGO_ENABLED=0 go run ./cmd/webfilter categories update --settings config/settings.example.json
fi

for target in "${TARGETS[@]}"; do
  IFS=: read -r goos goarch ext <<<"$target"
  name="webfilter-${VERSION}-${goos}-${goarch}"
  stage="$OUT_DIR/$name"
  mkdir -p "$stage"

  echo "[package] building ${goos}/${goarch} ..."
  GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go build \
    -ldflags "$LDFLAGS" \
    -o "$stage/webfilter${ext}" ./cmd/webfilter

  [[ -f config/settings.example.json ]] && cp config/settings.example.json "$stage/"
  [[ -f policies/default.json.example ]] && cp policies/default.json.example "$stage/"
  [[ -d categories ]] && cp -R categories "$stage/"
  cp packaging/README.md "$stage/"
  if [[ "$goos" == "windows" ]]; then
    if [[ -f wintun.dll ]]; then
      cp wintun.dll "$stage/"
    elif [[ -f packaging/wintun.dll ]]; then
      cp packaging/wintun.dll "$stage/"
    else
      echo "[package] warning: wintun.dll not found; Windows TUN mode will require users to add it beside webfilter.exe"
    fi
  fi
  if [[ "$goos" == "linux" ]]; then
    cp packaging/webfilter.service packaging/webfilter-proxy.service \
      packaging/webfilter-mgmt.service packaging/install.sh "$stage/"
    chmod +x "$stage/install.sh"
  fi

  archive_ext="tar.gz"
  [[ "$goos" == "windows" ]] && archive_ext="zip"
  archive="$OUT_DIR/${name}.${archive_ext}"
  go run "$SCRIPT_DIR/archive.go" "$stage" "$archive"
  rm -rf "$stage"
  echo "[package] wrote $archive"
done

echo ""
echo "[package] done:"
ls -la "$OUT_DIR"
