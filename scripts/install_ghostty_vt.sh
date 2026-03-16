#!/usr/bin/env sh
set -eu

GHOSTTY_COMMIT="${1:?ghostty commit is required}"
PATCH_COMMIT="${2:?ghostty-web patch commit is required}"
ZIG="${3:?zig binary path is required}"
TOOLS_DIR="${4:?tools dir is required}"
WORK_ROOT="${5:-./tmp}"
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)

CACHE_DIR="$TOOLS_DIR/cache"
INSTALL_BASE="$TOOLS_DIR/ghostty-vt"
INSTALL_DIR="$INSTALL_BASE/$GHOSTTY_COMMIT"
CURRENT_LINK="$INSTALL_BASE/current"
PATCH_FILE="$CACHE_DIR/ghostty-wasm-api-${PATCH_COMMIT}.patch"
LOCAL_PATCH_FILE="$SCRIPT_DIR/patches/ghostty-vt-codelima.patch"
SOURCE_URL="https://github.com/ghostty-org/ghostty.git"
PATCH_URL="https://raw.githubusercontent.com/coder/ghostty-web/$PATCH_COMMIT/patches/ghostty-wasm-api.patch"

LIB_EXT="so"
case "$(uname -s)" in
  Darwin)
    LIB_EXT="dylib"
    ;;
  Linux)
    LIB_EXT="so"
    ;;
  *)
    echo "unsupported operating system: $(uname -s)" >&2
    exit 1
    ;;
esac

PATCH_STAMP=$(cksum "$PATCH_FILE" 2>/dev/null | awk '{print $1 ":" $2}')
LOCAL_PATCH_STAMP=$(cksum "$LOCAL_PATCH_FILE" | awk '{print $1 ":" $2}')
EXPECTED_STAMP="$GHOSTTY_COMMIT|$PATCH_COMMIT|$PATCH_STAMP|$LOCAL_PATCH_STAMP"
INSTALL_STAMP_FILE="$INSTALL_DIR/.build-stamp"

if [ -f "$INSTALL_DIR/lib/libghostty-vt.$LIB_EXT" ] && [ -f "$INSTALL_STAMP_FILE" ] && [ "$(cat "$INSTALL_STAMP_FILE")" = "$EXPECTED_STAMP" ]; then
  mkdir -p "$INSTALL_BASE"
  ln -sfn "$INSTALL_DIR" "$CURRENT_LINK"
  exit 0
fi

mkdir -p "$CACHE_DIR" "$INSTALL_BASE" "$WORK_ROOT"

if [ ! -f "$PATCH_FILE" ]; then
  curl -fsSL "$PATCH_URL" -o "$PATCH_FILE"
fi

PATCH_STAMP=$(cksum "$PATCH_FILE" | awk '{print $1 ":" $2}')
EXPECTED_STAMP="$GHOSTTY_COMMIT|$PATCH_COMMIT|$PATCH_STAMP|$LOCAL_PATCH_STAMP"

TMP_DIR="$WORK_ROOT/install-ghostty-vt.$$"
SRC_DIR="$TMP_DIR/ghostty"
STAGE_DIR="$TMP_DIR/install"
rm -rf "$TMP_DIR"
mkdir -p "$TMP_DIR"
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM

git init "$SRC_DIR" >/dev/null 2>&1
git -C "$SRC_DIR" remote add origin "$SOURCE_URL"
git -C "$SRC_DIR" fetch --depth 1 origin "$GHOSTTY_COMMIT" >/dev/null
git -C "$SRC_DIR" checkout --detach FETCH_HEAD >/dev/null

(cd "$SRC_DIR" && git apply --check "$PATCH_FILE" && git apply "$PATCH_FILE")
(cd "$SRC_DIR" && git apply --check "$LOCAL_PATCH_FILE" && git apply "$LOCAL_PATCH_FILE")
(cd "$SRC_DIR" && "$ZIG" build lib-vt -Doptimize=ReleaseSmall --prefix "$STAGE_DIR")

rm -rf "$INSTALL_DIR"
mv "$STAGE_DIR" "$INSTALL_DIR"
printf '%s\n' "$EXPECTED_STAMP" > "$INSTALL_STAMP_FILE"
ln -sfn "$INSTALL_DIR" "$CURRENT_LINK"
