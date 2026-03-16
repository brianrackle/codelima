#!/usr/bin/env sh
set -eu

VERSION="${1:?go version is required}"
TOOLS_DIR="${2:?tools dir is required}"
WORK_ROOT="${3:-./tmp}"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
  x86_64)
    ARCH="amd64"
    ;;
  arm64|aarch64)
    ARCH="arm64"
    ;;
  *)
    echo "unsupported architecture: $ARCH" >&2
    exit 1
    ;;
esac

INSTALL_DIR="$TOOLS_DIR/go/$VERSION"
CACHE_DIR="$TOOLS_DIR/cache"
ARCHIVE="$CACHE_DIR/go${VERSION}.${OS}-${ARCH}.tar.gz"
URL="https://go.dev/dl/go${VERSION}.${OS}-${ARCH}.tar.gz"

if [ -x "$INSTALL_DIR/bin/go" ]; then
  exit 0
fi

mkdir -p "$CACHE_DIR" "$TOOLS_DIR/go"

if [ ! -f "$ARCHIVE" ]; then
  curl -fsSL "$URL" -o "$ARCHIVE"
fi

mkdir -p "$WORK_ROOT"
TMP_DIR="$WORK_ROOT/install-go.$$"
rm -rf "$TMP_DIR"
mkdir -p "$TMP_DIR"
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM
tar -C "$TMP_DIR" -xzf "$ARCHIVE"
rm -rf "$INSTALL_DIR"
mv "$TMP_DIR/go" "$INSTALL_DIR"
