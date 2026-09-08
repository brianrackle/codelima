#!/usr/bin/env sh
set -eu

GHOSTTY_COMMIT="${1:?ghostty commit is required}"
ZIG="${2:?zig binary path is required}"
TOOLS_DIR="${3:?tools dir is required}"
WORK_ROOT="${4:-./tmp}"
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
VT_FEATURES="${GHOSTTY_VT_FEATURES:--all,+formatter,+selection,+render-state,+input-encode,+color,+grid-introspection,+snapshot,+search,+kitty-graphics}"
OPTIMIZE="${GHOSTTY_VT_OPTIMIZE:-ReleaseSmall}"
TARGET="${GHOSTTY_VT_TARGET:-native}"
CPU="${GHOSTTY_VT_CPU:-baseline}"
BUILD_JOBS="${GHOSTTY_VT_BUILD_JOBS:-4}"

case "$GHOSTTY_COMMIT" in
  *[!0-9a-f]*|'') echo "Ghostty revision must be a full lowercase commit hash" >&2; exit 1 ;;
esac
[ "${#GHOSTTY_COMMIT}" -eq 40 ] || { echo "Ghostty revision must be a full commit hash" >&2; exit 1; }
case "$OPTIMIZE" in ReleaseSmall|ReleaseFast|ReleaseSafe|Debug) ;; *) echo "invalid Ghostty optimization mode" >&2; exit 1 ;; esac
case "$BUILD_JOBS" in ''|*[!0-9]*|0) echo "invalid Ghostty build concurrency" >&2; exit 1 ;; esac
case "$(uname -s)" in Darwin|Linux) ;; *) echo "unsupported native Ghostty host" >&2; exit 1 ;; esac

mkdir -p "$TOOLS_DIR/ghostty-vt/sources" "$TOOLS_DIR/cache/zig-global" "$WORK_ROOT"
TOOLS_DIR=$(CDPATH= cd -- "$TOOLS_DIR" && pwd)
WORK_ROOT=$(CDPATH= cd -- "$WORK_ROOT" && pwd)
ZIG=$(CDPATH= cd -- "$(dirname "$ZIG")" && pwd)/$(basename "$ZIG")
ZIG_VERSION=$("$ZIG" version)
[ "$ZIG_VERSION" = 0.16.0 ] || { echo "audited libghostty-vt requires Zig 0.16.0" >&2; exit 1; }
CACHE_DIR="$TOOLS_DIR/cache"
INSTALL_BASE="$TOOLS_DIR/ghostty-vt"
CURRENT_LINK="$INSTALL_BASE/current"
SOURCE_DIR="$INSTALL_BASE/sources/$GHOSTTY_COMMIT"
LOCAL_PATCH_FILE="$SCRIPT_DIR/patches/ghostty-vt-codelima.patch"
CLIPBOARD_PATCH_FILE="$SCRIPT_DIR/patches/ghostty-vt-clipboard-ack.patch"
GRAPHICS_PATCH_FILE="$SCRIPT_DIR/patches/ghostty-vt-graphics-policy.patch"
SNAPSHOT_PATCH_FILE="$SCRIPT_DIR/patches/ghostty-vt-snapshot-allocator.patch"
PATCH_SHA256=$(shasum -a 256 "$LOCAL_PATCH_FILE" "$CLIPBOARD_PATCH_FILE" "$GRAPHICS_PATCH_FILE" "$SNAPSHOT_PATCH_FILE" | awk '{print $1}' | shasum -a 256 | awk '{print $1}')
INSTALLER_SHA256=$(shasum -a 256 "$0" | awk '{print $1}')
PLATFORM="$(uname -s)-$(uname -m)"
EXPECTED_STAMP="$GHOSTTY_COMMIT|zig=$ZIG_VERSION|platform=$PLATFORM|target=$TARGET|cpu=$CPU|optimize=$OPTIMIZE|features=$VT_FEATURES|patch=$PATCH_SHA256|installer=$INSTALLER_SHA256"
LOCK_FILE="$INSTALL_BASE/.install.lock"
TMP_DIR=
TOOLING_GO="${CODELIMA_TOOLING_GO:-$TOOLS_DIR/go/1.24.1/bin/go}"

if [ "${CODELIMA_INSTALL_LOCK_HELD:-}" != "$LOCK_FILE" ]; then
  exec "$TOOLING_GO" run "$SCRIPT_DIR/tooling_lock.go" "$LOCK_FILE" sh "$0" "$GHOSTTY_COMMIT" "$ZIG" "$TOOLS_DIR" "$WORK_ROOT"
fi
unset CODELIMA_INSTALL_LOCK_HELD

cleanup() {
  if [ -n "$TMP_DIR" ]; then rm -rf "$TMP_DIR"; fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

publish_current() {
  # Both spellings replace the symlink itself, even when it names a directory.
  ln -s "$INSTALL_DIR" "$TMP_DIR/current"
  case "$(uname -s)" in
    Darwin) mv -fh "$TMP_DIR/current" "$CURRENT_LINK" ;;
    Linux) mv -Tf "$TMP_DIR/current" "$CURRENT_LINK" ;;
  esac
}

# Stage on the destination filesystem so publication is always one rename.
TMP_DIR=$(mktemp -d "$INSTALL_BASE/.install.XXXXXX")
NATIVE_BUILD_ID=$("$TOOLING_GO" run "$SCRIPT_DIR/renderer_build.go" -source "$GHOSTTY_COMMIT" \
  -features "$VT_FEATURES" -target "$TARGET" -cpu "$CPU" -optimize "$OPTIMIZE" \
  -patches "$SCRIPT_DIR/patches" -output "$TMP_DIR/profile")
EXPECTED_STAMP="$EXPECTED_STAMP|native=$NATIVE_BUILD_ID"
BUILD_ID=$(printf '%s' "$EXPECTED_STAMP" | shasum -a 256 | awk '{print $1}')
INSTALL_DIR="$INSTALL_BASE/$GHOSTTY_COMMIT-$BUILD_ID"
if [ -f "$INSTALL_DIR/lib/libghostty-vt.a" ] &&
   [ -s "$INSTALL_DIR/include/ghostty/codelima_build.h" ] &&
   [ "$(cat "$INSTALL_DIR/.native-build-id" 2>/dev/null || true)" = "$NATIVE_BUILD_ID" ] &&
   [ "$(cat "$INSTALL_DIR/.build-stamp" 2>/dev/null || true)" = "$EXPECTED_STAMP" ]; then
  publish_current
  exit 0
fi
if [ -e "$INSTALL_DIR" ]; then echo "incomplete immutable Ghostty installation: $INSTALL_DIR" >&2; exit 1; fi

if [ ! -d "$SOURCE_DIR/.git" ]; then
  git init "$TMP_DIR/source" >/dev/null 2>&1
  git -C "$TMP_DIR/source" remote add origin https://github.com/ghostty-org/ghostty.git
  git -C "$TMP_DIR/source" fetch --depth 1 origin "$GHOSTTY_COMMIT"
  git -C "$TMP_DIR/source" checkout --detach "$GHOSTTY_COMMIT"
  [ "$(git -C "$TMP_DIR/source" rev-parse HEAD)" = "$GHOSTTY_COMMIT" ]
  mv "$TMP_DIR/source" "$SOURCE_DIR"
fi
[ "$(git -C "$SOURCE_DIR" rev-parse HEAD)" = "$GHOSTTY_COMMIT" ] || { echo "Ghostty source revision mismatch" >&2; exit 1; }

# Build an immutable checkout of the requested tree. Zig verifies every remote
# dependency against build.zig.zon's content hash; never rewrite it to .path.
SRC_DIR="$TMP_DIR/build-source"
mkdir "$SRC_DIR"
git -C "$SOURCE_DIR" archive "$GHOSTTY_COMMIT" | tar -xf - -C "$SRC_DIR"
# The archive lives beneath CodeLima's repository. Without its own Git root,
# git apply can silently skip upstream paths as outside the current prefix.
git init "$SRC_DIR" >/dev/null 2>&1
(cd "$SRC_DIR" && git apply --check "$LOCAL_PATCH_FILE" "$CLIPBOARD_PATCH_FILE" "$GRAPHICS_PATCH_FILE" "$SNAPSHOT_PATCH_FILE" && git apply "$LOCAL_PATCH_FILE" "$CLIPBOARD_PATCH_FILE" "$GRAPHICS_PATCH_FILE" "$SNAPSHOT_PATCH_FILE" && git apply --reverse --check "$LOCAL_PATCH_FILE" "$CLIPBOARD_PATCH_FILE" "$GRAPHICS_PATCH_FILE" "$SNAPSHOT_PATCH_FILE")
APP_VERSION=$(sed -n 's/^[[:space:]]*\.version = "\([^"]*\)",/\1/p' "$SRC_DIR/build.zig.zon")
test -n "$APP_VERSION"
STAGE_DIR="$TMP_DIR/install"
(cd "$SRC_DIR" && ZIG_GLOBAL_CACHE_DIR="$CACHE_DIR/zig-global" ZIG_LOCAL_CACHE_DIR="$CACHE_DIR/ghostty-build/$BUILD_ID" \
  "$ZIG" build -j"$BUILD_JOBS" -Demit-lib-vt=true -Demit-xcframework=false \
  -Dversion-string="$APP_VERSION+$GHOSTTY_COMMIT" -Dlib-version-string="0.1.0-dev+$GHOSTTY_COMMIT" \
  -Doptimize="$OPTIMIZE" -Dtarget="$TARGET" -Dcpu="$CPU" -Dvt-features="$VT_FEATURES" --prefix "$STAGE_DIR")
test -s "$STAGE_DIR/lib/libghostty-vt.a"
test -s "$STAGE_DIR/include/ghostty/vt.h"
test -s "$STAGE_DIR/share/pkgconfig/libghostty-vt-static.pc"

# Embed the actual archive's identity, independently of whichever headers a C
# consumer selects. The adapter compares this function with the header macro.
"$ZIG" cc -target "$TARGET" -mcpu="$CPU" -O2 -fPIC -c "$TMP_DIR/profile/codelima_build.c" -o "$TMP_DIR/profile/codelima_build.o"
"$ZIG" ar rcs "$STAGE_DIR/lib/libghostty-vt.a" "$TMP_DIR/profile/codelima_build.o"
cp "$TMP_DIR/profile/codelima_build.h" "$STAGE_DIR/include/ghostty/codelima_build.h"
cp "$TMP_DIR/profile/.native-build-id" "$STAGE_DIR/.native-build-id"
cp "$TMP_DIR/profile/build-profile.json" "$STAGE_DIR/build-profile.json"

# pkg-config must retain the immutable final prefix, not the temporary stage.
sed "s|^prefix=.*|prefix=$INSTALL_DIR|" "$STAGE_DIR/share/pkgconfig/libghostty-vt-static.pc" > "$TMP_DIR/static.pc"
mv "$TMP_DIR/static.pc" "$STAGE_DIR/share/pkgconfig/libghostty-vt-static.pc"
printf '%s\n' "$EXPECTED_STAMP" > "$STAGE_DIR/.build-stamp"
printf '%s\n' "$BUILD_ID" > "$STAGE_DIR/.build-id"
cp "$LOCAL_PATCH_FILE" "$STAGE_DIR/codelima.patch"
cp "$CLIPBOARD_PATCH_FILE" "$STAGE_DIR/clipboard-ack.patch"
cp "$GRAPHICS_PATCH_FILE" "$STAGE_DIR/graphics-policy.patch"
cp "$SNAPSHOT_PATCH_FILE" "$STAGE_DIR/snapshot-allocator.patch"
mv "$SRC_DIR" "$STAGE_DIR/source"
mv "$STAGE_DIR" "$INSTALL_DIR"
publish_current
