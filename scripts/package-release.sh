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
# Both classifiers (internal/classify/image, internal/classify/text) are
# unconditionally ONNX/onnxruntime_go-backed now (no more CGO-free `-tags
# onnx`-gated build), so every target here needs CGO_ENABLED=1 and a
# matching C toolchain, plus the onnxruntime shared library bundled
# alongside the binary (onnxruntime_go loads it dynamically at runtime, not
# statically - see internal/classify/onnxrt, which looks for it next to the
# running executable if ONNXRUNTIME_SHARED_LIBRARY isn't set).
#
# Running this locally on Windows only covers the windows/amd64 target out
# of the box (this machine's own toolchain matches CGO's build arch);
# cross-compiling linux/amd64 or linux/arm64 from Windows needs separate
# Linux cross-toolchains this isn't set up for - treat the full three-target
# run as CI's job (.github/workflows/ci.yml's `release`) in practice.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
cd "$REPO_ROOT"

VERSION="${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
OUT_DIR="${2:-$REPO_ROOT/dist}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

# Pinned exact version, not "latest" - keep in sync with whatever version
# was last verified to load correctly (see HANDOFF.md).
ONNXRUNTIME_VERSION="1.27.0"
ONNXRUNTIME_BASE_URL="https://github.com/microsoft/onnxruntime/releases/download/v${ONNXRUNTIME_VERSION}"

LDFLAGS="-s -w"
LDFLAGS="$LDFLAGS -X github.com/yjlion/gowebfilter/internal/version.Version=${VERSION}"
LDFLAGS="$LDFLAGS -X github.com/yjlion/gowebfilter/internal/version.Commit=${COMMIT}"
LDFLAGS="$LDFLAGS -X github.com/yjlion/gowebfilter/internal/version.BuildDate=${BUILD_DATE}"

# goos:goarch:binary-extension:cc:onnxruntime-asset-basename:shared-lib-name
TARGETS=(
  "windows:amd64:.exe:x86_64-w64-mingw32-gcc:onnxruntime-win-x64:onnxruntime.dll"
  "linux:amd64::gcc:onnxruntime-linux-x64:libonnxruntime.so"
  "linux:arm64::aarch64-linux-gnu-gcc:onnxruntime-linux-aarch64:libonnxruntime.so"
)

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR"

for target in "${TARGETS[@]}"; do
  IFS=: read -r goos goarch ext cc ortAsset ortLibName <<<"$target"
  name="webfilter-${VERSION}-${goos}-${goarch}"
  stage="$OUT_DIR/$name"
  mkdir -p "$stage"

  echo "[package] building ${goos}/${goarch} (CC=${cc}) ..."
  GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=1 CC="$cc" go build \
    -ldflags "$LDFLAGS" \
    -o "$stage/webfilter${ext}" ./cmd/webfilter

  echo "[package] fetching onnxruntime ${ONNXRUNTIME_VERSION} shared library for ${goos}/${goarch} ..."
  ortArchiveExt="tgz"
  [[ "$goos" == "windows" ]] && ortArchiveExt="zip"
  ortArchive="$WORK_DIR/${ortAsset}-${ONNXRUNTIME_VERSION}.${ortArchiveExt}"
  if [[ ! -f "$ortArchive" ]]; then
    curl -sL -o "$ortArchive" "${ONNXRUNTIME_BASE_URL}/${ortAsset}-${ONNXRUNTIME_VERSION}.${ortArchiveExt}"
  fi
  ortExtractDir="$WORK_DIR/${ortAsset}"
  mkdir -p "$ortExtractDir"
  if [[ "$ortArchiveExt" == "zip" ]]; then
    unzip -q -o "$ortArchive" -d "$ortExtractDir"
  else
    tar -xzf "$ortArchive" -C "$ortExtractDir"
  fi
  find "$ortExtractDir" -name "$ortLibName" -exec cp {} "$stage/$ortLibName" \;
  [[ -f "$stage/$ortLibName" ]] || { echo "[package] ERROR: $ortLibName not found in $ortArchive" >&2; exit 1; }

  [[ -f config/settings.example.json ]] && cp config/settings.example.json "$stage/"
  [[ -f policies/default.json.example ]] && cp policies/default.json.example "$stage/"
  cp packaging/README.md "$stage/"
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
