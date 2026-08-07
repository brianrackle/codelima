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
LIB_NAME="libghostty-vt.so"
case "$GOOS" in
  darwin)
    LIB_NAME="libghostty-vt.dylib"
    ;;
  linux)
    LIB_NAME="libghostty-vt.so"
    ;;
  *)
    echo "unsupported goos: $GOOS" >&2
    exit 1
    ;;
esac

GHOSTTY_LIB="$TOOLS_DIR/ghostty-vt/current/lib/$LIB_NAME"
if [ ! -f "$GHOSTTY_LIB" ]; then
  echo "ghostty library not found: $GHOSTTY_LIB" >&2
  exit 1
fi

mkdir -p "$(dirname "$BUILD_BIN")" "$ROOT_DIR/bin" "$DIST_DIR"

cd "$ROOT_DIR"
"$GO_BIN" build -ldflags "-X github.com/brianrackle/codelima/internal/codelima.Version=$VERSION" -o "$BUILD_BIN" ./cmd/codelima
"$GO_BIN" build -ldflags "-X github.com/brianrackle/codelima/internal/codelima.Version=$VERSION" -o "$RENDERER_BIN" ./cmd/codelima-renderer-worker
"$GO_BIN" run ./cmd/codelima-release archive \
  --version "$VERSION" \
  --goos "$GOOS" \
  --goarch "$GOARCH" \
  --binary "$BUILD_BIN" \
  --renderer-binary "$RENDERER_BIN" \
  --ghostty-lib "$GHOSTTY_LIB" \
  --output-dir "$DIST_DIR"
