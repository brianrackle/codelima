#!/usr/bin/env sh
set -eu

# Autoconf/libtool expect a single compiler command, not a quoted path followed
# by Zig's subcommand. The installer copies this shim under these relative
# names so paths containing spaces never enter their command-string parser.
case "$0" in
  *-cc) exec "${CODELIMA_PKGCONF_ZIG:?managed Zig path is required}" cc "$@" ;;
  *-ar) exec "${CODELIMA_PKGCONF_ZIG:?managed Zig path is required}" ar "$@" ;;
  *-ranlib) exec "${CODELIMA_PKGCONF_ZIG:?managed Zig path is required}" ranlib "$@" ;;
  *) echo "unsupported pkgconf toolchain invocation" >&2; exit 1 ;;
esac
