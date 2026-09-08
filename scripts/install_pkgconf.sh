#!/usr/bin/env sh
set -eu

VERSION="${1:?pkgconf version is required}"
TOOLS_DIR="${2:?tools dir is required}"
WORK_ROOT="${3:?project work root is required}"
ZIG="${4:?managed Zig binary path is required}"
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)

# Upstream release tarball, including generated configure (no autotools,
# Meson, Homebrew, existing pkg-config, or system C compiler is needed).
case "$VERSION" in
  2.5.1) SHA256=cd05c9589b9f86ecf044c10a2269822bc9eb001eced2582cfffd658b0a50c243 ;;
  *) echo "no reviewed pkgconf archive checksum for $VERSION" >&2; exit 1 ;;
esac
case "$(uname -s)" in
  Darwin|Linux) ;;
  *) echo "unsupported pkgconf host" >&2; exit 1 ;;
esac

mkdir -p "$TOOLS_DIR/pkgconf" "$TOOLS_DIR/bin" "$TOOLS_DIR/cache/zig-global" "$WORK_ROOT"
TOOLS_DIR=$(CDPATH= cd -- "$TOOLS_DIR" && pwd)
WORK_ROOT=$(CDPATH= cd -- "$WORK_ROOT" && pwd)
ZIG=$(CDPATH= cd -- "$(dirname "$ZIG")" && pwd)/$(basename "$ZIG")
TOOLING_GO="${CODELIMA_TOOLING_GO:-$TOOLS_DIR/go/1.24.1/bin/go}"
LOCK_FILE="$TOOLS_DIR/pkgconf/.install.lock"
ARCHIVE="$TOOLS_DIR/cache/pkgconf-$VERSION.tar.xz"
URL="https://distfiles.ariadne.space/pkgconf/pkgconf-$VERSION.tar.xz"
TMP_DIR=

if [ "${CODELIMA_INSTALL_LOCK_HELD:-}" != "$LOCK_FILE" ]; then
  exec "$TOOLING_GO" run "$SCRIPT_DIR/tooling_lock.go" "$LOCK_FILE" sh "$0" "$VERSION" "$TOOLS_DIR" "$WORK_ROOT" "$ZIG"
fi
unset CODELIMA_INSTALL_LOCK_HELD

cleanup() {
  if [ -n "$TMP_DIR" ]; then rm -rf "$TMP_DIR"; fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

ZIG_VERSION=$("$ZIG" version)
INSTALLER_SHA256=$(shasum -a 256 "$0" "$SCRIPT_DIR/pkgconf_toolchain.sh" | awk '{print $1}' | shasum -a 256 | awk '{print $1}')
EXPECTED_STAMP="$VERSION|archive=$SHA256|zig=$ZIG_VERSION|platform=$(uname -s)-$(uname -m)|static=yes|installer=$INSTALLER_SHA256"
BUILD_ID=$(printf '%s' "$EXPECTED_STAMP" | shasum -a 256 | awk '{print $1}')
INSTALL_DIR="$TOOLS_DIR/pkgconf/$VERSION-$BUILD_ID"
PKG_CONFIG_LINK="$TOOLS_DIR/bin/pkg-config"

publish_tool() {
  ln -s "$INSTALL_DIR/bin/pkgconf" "$TMP_DIR/pkg-config"
  case "$(uname -s)" in
    Darwin) mv -fh "$TMP_DIR/pkg-config" "$PKG_CONFIG_LINK" ;;
    Linux) mv -Tf "$TMP_DIR/pkg-config" "$PKG_CONFIG_LINK" ;;
  esac
}

# Both the immutable prefix and symlink are staged on their final filesystem.
TMP_DIR=$(mktemp -d "$TOOLS_DIR/pkgconf/.install.XXXXXX")
if [ -x "$INSTALL_DIR/bin/pkgconf" ] &&
   [ "$(cat "$INSTALL_DIR/.build-stamp" 2>/dev/null || true)" = "$EXPECTED_STAMP" ] &&
   [ "$("$INSTALL_DIR/bin/pkgconf" --version)" = "$VERSION" ]; then
  publish_tool
  exit 0
fi
if [ -e "$INSTALL_DIR" ]; then
  echo "incomplete immutable pkgconf installation: $INSTALL_DIR" >&2
  exit 1
fi

if [ ! -f "$ARCHIVE" ]; then
  curl --fail --location --silent --show-error --retry 3 "$URL" -o "$TMP_DIR/archive.tar.xz"
  actual=$(shasum -a 256 "$TMP_DIR/archive.tar.xz" | awk '{print $1}')
  if [ "$actual" != "$SHA256" ]; then echo "pkgconf archive checksum mismatch" >&2; exit 1; fi
  mv "$TMP_DIR/archive.tar.xz" "$ARCHIVE"
fi
actual=$(shasum -a 256 "$ARCHIVE" | awk '{print $1}')
if [ "$actual" != "$SHA256" ]; then echo "cached pkgconf archive checksum mismatch: $ARCHIVE" >&2; exit 1; fi
tar -xJf "$ARCHIVE" -C "$TMP_DIR"
SOURCE_DIR="$TMP_DIR/pkgconf-$VERSION"
for tool in cc ar ranlib; do
  cp "$SCRIPT_DIR/pkgconf_toolchain.sh" "$SOURCE_DIR/codelima-zig-$tool"
  chmod 0755 "$SOURCE_DIR/codelima-zig-$tool"
done

export CODELIMA_PKGCONF_ZIG="$ZIG"
export ZIG_GLOBAL_CACHE_DIR="$TOOLS_DIR/cache/zig-global"
export ZIG_LOCAL_CACHE_DIR="$TMP_DIR/zig-cache"
export TMPDIR="$TMP_DIR"
# Only the executable is needed. Link libpkgconf statically, and never run
# make install against a system prefix. No installation path enters C flags.
if ! (cd "$SOURCE_DIR" && CC=./codelima-zig-cc AR=./codelima-zig-ar RANLIB=./codelima-zig-ranlib \
  ./configure --prefix=/ --disable-shared --enable-static --disable-dependency-tracking \
  --with-pkg-config-dir= --with-system-libdir=/lib:/usr/lib --with-system-includedir=/usr/include \
  > "$TMP_DIR/configure.log" 2>&1 && make -s -j2 pkgconf > "$TMP_DIR/build.log" 2>&1); then
  cat "$TMP_DIR/configure.log" >&2
  if [ -f "$TMP_DIR/build.log" ]; then cat "$TMP_DIR/build.log" >&2; fi
  echo "managed pkgconf build failed" >&2
  exit 1
fi
[ "$("$SOURCE_DIR/pkgconf" --version)" = "$VERSION" ] || { echo "built pkgconf version mismatch" >&2; exit 1; }
mkdir -p "$TMP_DIR/install/bin" "$TMP_DIR/install/share/licenses/pkgconf"
cp "$SOURCE_DIR/pkgconf" "$TMP_DIR/install/bin/pkgconf"
cp "$SOURCE_DIR/COPYING" "$TMP_DIR/install/share/licenses/pkgconf/COPYING"
printf '%s\n' "$EXPECTED_STAMP" > "$TMP_DIR/install/.build-stamp"
mv "$TMP_DIR/install" "$INSTALL_DIR"
publish_tool
