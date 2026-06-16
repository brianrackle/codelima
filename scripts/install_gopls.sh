#!/usr/bin/env sh
set -eu

VERSION="${1:?gopls version is required}"
GO="${2:?go binary is required}"
TOOLS_DIR="${3:?tools dir is required}"
WORK_ROOT="${4:-./tmp}"

BIN_DIR="$TOOLS_DIR/bin"
GOPLS="$BIN_DIR/gopls"
GOMODCACHE="${GOMODCACHE:-$TOOLS_DIR/gopath/pkg/mod}"
GOCACHE="${GOCACHE:-$TOOLS_DIR/gocache}"

if [ -x "$GOPLS" ]; then
  INSTALLED_VERSION="$("$GOPLS" version 2>/dev/null | awk 'NR == 1 { print $NF }')"
  if [ "$INSTALLED_VERSION" = "$VERSION" ]; then
    exit 0
  fi
fi

mkdir -p "$BIN_DIR" "$GOMODCACHE" "$GOCACHE" "$WORK_ROOT"
TMP_DIR="$WORK_ROOT/install-gopls.$$"
rm -rf "$TMP_DIR"
mkdir -p "$TMP_DIR"
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM

GOBIN="$BIN_DIR" \
GOMODCACHE="$GOMODCACHE" \
GOCACHE="$GOCACHE" \
GOTOOLCHAIN=local \
TMPDIR="$TMP_DIR" \
  "$GO" install "golang.org/x/tools/gopls@$VERSION"
