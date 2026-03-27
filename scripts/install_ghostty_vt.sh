#!/usr/bin/env sh
set -eu

GHOSTTY_COMMIT="${1:?ghostty commit is required}"
ZIG="${2:?zig binary path is required}"
TOOLS_DIR="${3:?tools dir is required}"
WORK_ROOT="${4:-./tmp}"
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)

CACHE_DIR="$TOOLS_DIR/cache"
INSTALL_BASE="$TOOLS_DIR/ghostty-vt"
INSTALL_DIR="$INSTALL_BASE/$GHOSTTY_COMMIT"
CURRENT_LINK="$INSTALL_BASE/current"
TOOLS_ROOT=$(dirname "$TOOLS_DIR")
COMPAT_INSTALL_BASE="$TOOLS_ROOT/ghostty-vt"
COMPAT_CURRENT_LINK="$COMPAT_INSTALL_BASE/current"
LOCAL_PATCH_FILE="$SCRIPT_DIR/patches/ghostty-vt-codelima.patch"
SOURCE_URL="https://github.com/ghostty-org/ghostty.git"
ZIG_GLOBAL_CACHE_DIR="$CACHE_DIR/zig-global"

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

LOCAL_PATCH_STAMP=$(cksum "$LOCAL_PATCH_FILE" | awk '{print $1 ":" $2}')
EXPECTED_STAMP="$GHOSTTY_COMMIT|$LOCAL_PATCH_STAMP"
INSTALL_STAMP_FILE="$INSTALL_DIR/.build-stamp"

if [ -f "$INSTALL_DIR/lib/libghostty-vt.$LIB_EXT" ] && [ -f "$INSTALL_STAMP_FILE" ] && [ "$(cat "$INSTALL_STAMP_FILE")" = "$EXPECTED_STAMP" ]; then
  mkdir -p "$INSTALL_BASE" "$COMPAT_INSTALL_BASE"
  rm -f "$CURRENT_LINK"
  ln -s "$INSTALL_DIR" "$CURRENT_LINK"
  rm -f "$COMPAT_CURRENT_LINK"
  ln -s "$INSTALL_DIR" "$COMPAT_CURRENT_LINK"
  exit 0
fi

mkdir -p "$CACHE_DIR" "$INSTALL_BASE" "$COMPAT_INSTALL_BASE" "$WORK_ROOT" "$ZIG_GLOBAL_CACHE_DIR"

TMP_DIR="$WORK_ROOT/install-ghostty-vt.$$"
SRC_DIR="$TMP_DIR/ghostty"
STAGE_DIR="$TMP_DIR/install"
ZIG_LOCAL_CACHE_DIR="$SRC_DIR/.zig-cache"
UUCODE_DIR="$SRC_DIR/.codelima-uucode"
rm -rf "$TMP_DIR"
mkdir -p "$TMP_DIR"
trap 'rm -rf "$TMP_DIR"' EXIT INT TERM

git init "$SRC_DIR" >/dev/null 2>&1
git -C "$SRC_DIR" remote add origin "$SOURCE_URL"
git -C "$SRC_DIR" fetch --depth 1 origin "$GHOSTTY_COMMIT" >/dev/null
git -C "$SRC_DIR" checkout --detach FETCH_HEAD >/dev/null

(cd "$SRC_DIR" && git apply --check "$LOCAL_PATCH_FILE" && git apply "$LOCAL_PATCH_FILE")

UUCODE_URL=$(
  awk '
    $1 == ".uucode" && $2 == "=" && $3 == ".{" { in_uucode = 1; next }
    in_uucode && $1 == ".url" {
      gsub(/"/, "", $3)
      gsub(/,/, "", $3)
      print $3
      exit
    }
    in_uucode && $1 == "}," { in_uucode = 0 }
  ' "$SRC_DIR/build.zig.zon"
)
if [ -z "$UUCODE_URL" ]; then
  echo "failed to locate uucode dependency URL in build.zig.zon" >&2
  exit 1
fi

mkdir -p "$UUCODE_DIR"
curl -fsSL "$UUCODE_URL" | tar -xzf - -C "$UUCODE_DIR" --strip-components=1
awk '
  /^[[:space:]]*\.uucode = \.\{/ {
    print "        .uucode = .{"
    print "            .path = \"./.codelima-uucode\","
    print "        },"
    in_uucode = 1
    next
  }
  in_uucode && /^[[:space:]]*},$/ {
    in_uucode = 0
    next
  }
  !in_uucode { print }
' "$SRC_DIR/build.zig.zon" > "$SRC_DIR/build.zig.zon.codelima" &&
mv "$SRC_DIR/build.zig.zon.codelima" "$SRC_DIR/build.zig.zon"

attempt=1
while :; do
  if (cd "$SRC_DIR" && ZIG_GLOBAL_CACHE_DIR="$ZIG_GLOBAL_CACHE_DIR" ZIG_LOCAL_CACHE_DIR="$ZIG_LOCAL_CACHE_DIR" "$ZIG" build -Demit-lib-vt=true -Doptimize=ReleaseSmall --prefix "$STAGE_DIR"); then
    break
  fi
  if [ "$attempt" -ge 3 ]; then
    echo "ghostty lib-vt build failed after $attempt attempts" >&2
    exit 1
  fi
  attempt=$((attempt + 1))
  rm -rf "$STAGE_DIR"
  sleep 2
done

rm -rf "$INSTALL_DIR"
mv "$STAGE_DIR" "$INSTALL_DIR"
printf '%s\n' "$EXPECTED_STAMP" > "$INSTALL_STAMP_FILE"
rm -f "$CURRENT_LINK"
ln -s "$INSTALL_DIR" "$CURRENT_LINK"
rm -f "$COMPAT_CURRENT_LINK"
ln -s "$INSTALL_DIR" "$COMPAT_CURRENT_LINK"
