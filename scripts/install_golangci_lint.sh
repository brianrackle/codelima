#!/usr/bin/env sh
set -eu

VERSION="${1:?golangci-lint version is required}"
TOOLS_DIR="${2:?tools dir is required}"

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

BIN_DIR="$TOOLS_DIR/bin"
CACHE_DIR="$TOOLS_DIR/cache"
ARCHIVE="$CACHE_DIR/golangci-lint-${VERSION}-${OS}-${ARCH}.tar.gz"
URL="https://github.com/golangci/golangci-lint/releases/download/v${VERSION}/golangci-lint-${VERSION}-${OS}-${ARCH}.tar.gz"

if [ -x "$BIN_DIR/golangci-lint" ]; then
  exit 0
fi

mkdir -p "$BIN_DIR" "$CACHE_DIR"

if [ ! -f "$ARCHIVE" ]; then
  curl -fsSL "$URL" -o "$ARCHIVE"
fi

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM
tar -C "$TMP_DIR" -xzf "$ARCHIVE"
cp "$TMP_DIR/golangci-lint-${VERSION}-${OS}-${ARCH}/golangci-lint" "$BIN_DIR/golangci-lint"
chmod +x "$BIN_DIR/golangci-lint"
