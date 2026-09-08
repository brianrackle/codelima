#!/usr/bin/env sh
set -eu

VERSION="${1:?zig version is required}"
TOOLS_DIR="${2:?tools dir is required}"
WORK_ROOT="${3:-./tmp}"
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$OS" in
  darwin) ZIG_OS=macos ;;
  linux) ZIG_OS=linux ;;
  *) echo "unsupported operating system: $OS" >&2; exit 1 ;;
esac
case "$ARCH" in
  x86_64|amd64) ARCH=x86_64 ;;
  arm64|aarch64) ARCH=aarch64 ;;
  *) echo "unsupported architecture: $ARCH" >&2; exit 1 ;;
esac

# Pinned from https://ziglang.org/download/index.json. Version changes require
# reviewed checksums, rather than authenticating downloads with a mutable URL.
case "$VERSION:$ARCH:$ZIG_OS" in
  0.16.0:x86_64:macos) SHA256=0387557ed1877bc6a2e1802c8391953baddba76081876301c522f52977b52ba7 ;;
  0.16.0:aarch64:macos) SHA256=b23d70deaa879b5c2d486ed3316f7eaa53e84acf6fc9cc747de152450d401489 ;;
  0.16.0:x86_64:linux) SHA256=70e49664a74374b48b51e6f3fdfbf437f6395d42509050588bd49abe52ba3d00 ;;
  0.16.0:aarch64:linux) SHA256=ea4b09bfb22ec6f6c6ceac57ab63efb6b46e17ab08d21f69f3a48b38e1534f17 ;;
  *) echo "no reviewed Zig archive checksum for $VERSION/$ARCH/$ZIG_OS" >&2; exit 1 ;;
esac

mkdir -p "$TOOLS_DIR/zig" "$TOOLS_DIR/cache" "$WORK_ROOT"
TOOLS_DIR=$(CDPATH= cd -- "$TOOLS_DIR" && pwd)
WORK_ROOT=$(CDPATH= cd -- "$WORK_ROOT" && pwd)
INSTALL_DIR="$TOOLS_DIR/zig/$VERSION"
ARCHIVE="$TOOLS_DIR/cache/zig-${VERSION}-${ARCH}-${ZIG_OS}.tar.xz"
URL="https://ziglang.org/download/${VERSION}/zig-${ARCH}-${ZIG_OS}-${VERSION}.tar.xz"
LOCK_FILE="$TOOLS_DIR/zig/.install.lock"
TMP_DIR=

if [ "${CODELIMA_INSTALL_LOCK_HELD:-}" != "$LOCK_FILE" ]; then
  TOOLING_GO="${CODELIMA_TOOLING_GO:-$TOOLS_DIR/go/1.24.1/bin/go}"
  exec "$TOOLING_GO" run "$SCRIPT_DIR/tooling_lock.go" "$LOCK_FILE" sh "$0" "$VERSION" "$TOOLS_DIR" "$WORK_ROOT"
fi
unset CODELIMA_INSTALL_LOCK_HELD

cleanup() {
  if [ -n "$TMP_DIR" ]; then rm -rf "$TMP_DIR"; fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

if [ -x "$INSTALL_DIR/zig" ] && [ "$("$INSTALL_DIR/zig" version)" = "$VERSION" ] &&
   [ "$(cat "$INSTALL_DIR/.archive-sha256" 2>/dev/null || true)" = "$SHA256" ]; then
  exit 0
fi
if [ -e "$INSTALL_DIR" ]; then
  echo "existing Zig installation has no matching verified stamp: $INSTALL_DIR" >&2
  exit 1
fi

TMP_DIR=$(mktemp -d "$TOOLS_DIR/zig/.install.XXXXXX")
if [ ! -f "$ARCHIVE" ]; then
  curl --fail --location --silent --show-error --retry 3 "$URL" -o "$TMP_DIR/archive.tar.xz"
  actual=$(shasum -a 256 "$TMP_DIR/archive.tar.xz" | awk '{print $1}')
  if [ "$actual" != "$SHA256" ]; then echo "Zig archive checksum mismatch" >&2; exit 1; fi
  mv "$TMP_DIR/archive.tar.xz" "$ARCHIVE"
fi
actual=$(shasum -a 256 "$ARCHIVE" | awk '{print $1}')
if [ "$actual" != "$SHA256" ]; then echo "cached Zig archive checksum mismatch: $ARCHIVE" >&2; exit 1; fi
tar -C "$TMP_DIR" -xJf "$ARCHIVE"
STAGE_DIR="$TMP_DIR/zig-${ARCH}-${ZIG_OS}-${VERSION}"
[ "$("$STAGE_DIR/zig" version)" = "$VERSION" ]
printf '%s\n' "$SHA256" > "$STAGE_DIR/.archive-sha256"
mv "$STAGE_DIR" "$INSTALL_DIR"
