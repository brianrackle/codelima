#!/usr/bin/env sh
set -eu

VERSION="${1:?release version is required}"
GO_BIN="${2:?go binary path is required}"
TOOLS_DIR="${3:?tools dir is required}"
DIST_DIR="${4:?dist dir is required}"
BUILD_BIN="${5:-}"
PLATFORM_TAG="${6:-}"
RENDERER_BIN="${7:-}"

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
ROOT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)

GOOS=$("$GO_BIN" env GOOS)
GOARCH=$("$GO_BIN" env GOARCH)
case "$GOOS/$GOARCH" in
  darwin/arm64|linux/amd64|linux/arm64)
    ;;
  *)
    echo "unsupported CodeLima release target: $GOOS/$GOARCH" >&2
    exit 1
    ;;
esac
if [ -z "$PLATFORM_TAG" ]; then
  PLATFORM_TAG="$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m | tr '[:upper:]' '[:lower:]')"
fi
if [ -z "$BUILD_BIN" ]; then
  BUILD_BIN="$ROOT_DIR/bin/$PLATFORM_TAG/codelima"
fi
if [ -z "$RENDERER_BIN" ]; then
  RENDERER_BIN="$ROOT_DIR/bin/$PLATFORM_TAG/codelima-renderer-worker"
fi
NATIVE_ROOT="$TOOLS_DIR/ghostty-vt/current"
if [ ! -f "$NATIVE_ROOT/lib/libghostty-vt.a" ] || [ ! -f "$NATIVE_ROOT/.native-build-id" ]; then
  echo "verified static renderer dependency not found under $NATIVE_ROOT; run make init" >&2
  exit 1
fi
TOOLS_DIR=$(CDPATH= cd -- "$TOOLS_DIR" && pwd)
NATIVE_ROOT="$TOOLS_DIR/ghostty-vt/current"

cd "$ROOT_DIR"
set -- -patches "$SCRIPT_DIR/patches"
if [ -n "${GHOSTTY_VT_FEATURES:-}" ]; then set -- "$@" "-features=$GHOSTTY_VT_FEATURES"; fi
if [ -n "${GHOSTTY_VT_TARGET:-}" ]; then set -- "$@" "-target=$GHOSTTY_VT_TARGET"; fi
if [ -n "${GHOSTTY_VT_CPU:-}" ]; then set -- "$@" "-cpu=$GHOSTTY_VT_CPU"; fi
if [ -n "${GHOSTTY_VT_OPTIMIZE:-}" ]; then set -- "$@" "-optimize=$GHOSTTY_VT_OPTIMIZE"; fi
RENDERER_BUILD_ID=$("$GO_BIN" run scripts/renderer_build.go "$@")
if [ "$RENDERER_BUILD_ID" != "$(cat "$NATIVE_ROOT/.native-build-id")" ]; then
  echo "installed renderer build identity differs from the selected, reviewed profile; run make init" >&2
  exit 1
fi

# Direct packaging must use the same metadata resolver as Make, even when the
# caller's PATH has no pkg-config. Validate the native profile before bootstrapping
# so mismatched release inputs fail without network or installation side effects.
ZIG_VERSION="${ZIG_VERSION:-0.16.0}"
PKGCONF_VERSION="${PKGCONF_VERSION:-2.5.1}"
ZIG_BIN="$TOOLS_DIR/zig/$ZIG_VERSION/zig"
export CODELIMA_TOOLING_GO="$GO_BIN"
"$SCRIPT_DIR/install_zig.sh" "$ZIG_VERSION" "$TOOLS_DIR" "$ROOT_DIR/tmp"
"$SCRIPT_DIR/install_pkgconf.sh" "$PKGCONF_VERSION" "$TOOLS_DIR" "$ROOT_DIR/tmp" "$ZIG_BIN"
export PKG_CONFIG="$TOOLS_DIR/bin/pkg-config"
export PKG_CONFIG_PATH="$NATIVE_ROOT/share/pkgconfig${PKG_CONFIG_PATH:+:$PKG_CONFIG_PATH}"
LDFLAGS="-X github.com/brianrackle/codelima/internal/codelima.Version=$VERSION -X github.com/brianrackle/codelima/internal/rendererbuild.identityOverride=$RENDERER_BUILD_ID"
mkdir -p "$(dirname "$BUILD_BIN")" "$(dirname "$RENDERER_BIN")" "$DIST_DIR"
# macOS needs its existing Virtualization.framework host-capability adapter.
# Ghostty remains outside the CLI dependency graph on every platform.
CLI_CGO=0
if [ "$GOOS" = darwin ]; then CLI_CGO=1; fi
CGO_ENABLED="$CLI_CGO" "$GO_BIN" build -ldflags "$LDFLAGS" -o "$BUILD_BIN" ./cmd/codelima
CGO_ENABLED=1 "$GO_BIN" build -ldflags "$LDFLAGS" -o "$RENDERER_BIN" ./cmd/codelima-renderer-worker
"$GO_BIN" run ./cmd/codelima-release archive \
  --version "$VERSION" \
  --goos "$GOOS" \
  --goarch "$GOARCH" \
  --binary "$BUILD_BIN" \
  --renderer-binary "$RENDERER_BIN" \
  --renderer-build-id "$RENDERER_BUILD_ID" \
  --output-dir "$DIST_DIR"
