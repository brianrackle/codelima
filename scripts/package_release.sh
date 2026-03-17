#!/usr/bin/env sh
set -eu

VERSION="${1:?release version is required}"
GO_BIN="${2:?go binary path is required}"
TOOLS_DIR="${3:?tools dir is required}"
DIST_DIR="${4:?dist dir is required}"

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
ROOT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)

GOOS=$("$GO_BIN" env GOOS)
GOARCH=$("$GO_BIN" env GOARCH)

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

mkdir -p "$ROOT_DIR/bin" "$DIST_DIR"

cd "$ROOT_DIR"
"$GO_BIN" build -o "$ROOT_DIR/bin/codelima" ./cmd/codelima
"$GO_BIN" run ./cmd/codelima-release archive \
  --version "$VERSION" \
  --goos "$GOOS" \
  --goarch "$GOARCH" \
  --binary "$ROOT_DIR/bin/codelima" \
  --ghostty-lib "$GHOSTTY_LIB" \
  --output-dir "$DIST_DIR"
