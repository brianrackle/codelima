#!/usr/bin/env sh
set -eu

VERSION="${1:?zig version is required}"
TOOLS_DIR="${2:?tools dir is required}"
WORK_ROOT="${3:-./tmp}"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$OS" in
  darwin)
    ZIG_OS="macos"
    ;;
  linux)
    ZIG_OS="linux"
    ;;
  *)
    echo "unsupported operating system: $OS" >&2
    exit 1
    ;;
esac

case "$ARCH" in
  x86_64|amd64)
    ARCH="x86_64"
    ;;
  arm64|aarch64)
    ARCH="aarch64"
    ;;
  *)
    echo "unsupported architecture: $ARCH" >&2
    exit 1
    ;;
esac

INSTALL_DIR="$TOOLS_DIR/zig/$VERSION"
CACHE_DIR="$TOOLS_DIR/cache"
ARCHIVE="$CACHE_DIR/zig-${VERSION}-${ARCH}-${ZIG_OS}.tar.xz"
URL="https://ziglang.org/download/${VERSION}/zig-${ARCH}-${ZIG_OS}-${VERSION}.tar.xz"

if [ -x "$INSTALL_DIR/zig" ]; then
  exit 0
fi

mkdir -p "$CACHE_DIR" "$TOOLS_DIR/zig" "$WORK_ROOT"

if [ ! -f "$ARCHIVE" ]; then
  curl -fsSL "$URL" -o "$ARCHIVE"
fi

TMP_DIR="$WORK_ROOT/install-zig.$$"
rm -rf "$TMP_DIR"
mkdir -p "$TMP_DIR"
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM
tar -C "$TMP_DIR" -xJf "$ARCHIVE"
rm -rf "$INSTALL_DIR"
mv "$TMP_DIR"/zig-* "$INSTALL_DIR"
