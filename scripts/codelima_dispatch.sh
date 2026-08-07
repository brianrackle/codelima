#!/usr/bin/env sh
# Installed as bin/codelima. The repo is mounted host+guest at the same path,
# so a fixed symlink here would point one OS at the other's binary; dispatch
# on the invoking platform instead.
set -eu
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PLATFORM_TAG="$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m | tr '[:upper:]' '[:lower:]')"
TARGET="$SCRIPT_DIR/$PLATFORM_TAG/codelima"
if [ ! -x "$TARGET" ]; then
  echo "codelima: no build for $PLATFORM_TAG at $TARGET; run 'make build' on this platform" >&2
  exit 127
fi
exec "$TARGET" "$@"
