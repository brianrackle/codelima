#!/bin/sh

set -u

usage() {
  cat <<'EOF'
Usage: capture.sh [options]

Options:
  --home PATH            CodeLima metadata home
  --binary PATH          Host-compatible codelima binary
  --output PATH          Capture directory (default: ./tmp/terminal-freeze-<timestamp>)
  --terminal-id ID       Frozen terminal to probe (default: first listed terminal)
  --sample-seconds N     macOS sample duration, 0 disables (default: 10)
  --help                 Show this help
EOF
}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
project_root=$(CDPATH= cd -- "$script_dir/../../../.." && pwd -P)
diag_home=${CODELIMA_HOME:-${HOME}/.codelima}
diag_binary=
diag_output=
diag_terminal_id=
diag_sample_seconds=10

while [ "$#" -gt 0 ]; do
  case "$1" in
    --home|--binary|--output|--terminal-id|--sample-seconds)
      if [ "$#" -lt 2 ]; then
        printf 'missing value for %s\n' "$1" >&2
        usage >&2
        exit 2
      fi
      case "$1" in
        --home) diag_home=$2 ;;
        --binary) diag_binary=$2 ;;
        --output) diag_output=$2 ;;
        --terminal-id) diag_terminal_id=$2 ;;
        --sample-seconds) diag_sample_seconds=$2 ;;
      esac
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      printf 'unknown argument: %s\n' "$1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

case "$diag_sample_seconds" in
  ''|*[!0-9]*)
    printf 'sample duration must be a non-negative integer\n' >&2
    exit 2
    ;;
esac

host_os=$(uname -s 2>/dev/null || printf 'unknown')
host_arch=$(uname -m 2>/dev/null || printf 'unknown')
platform_tag=$(printf '%s-%s' "$host_os" "$host_arch" | tr '[:upper:]' '[:lower:]')
if [ -z "$diag_binary" ]; then
  platform_binary="$project_root/bin/$platform_tag/codelima"
  if [ -x "$platform_binary" ]; then
    diag_binary=$platform_binary
  elif command -v codelima >/dev/null 2>&1; then
    diag_binary=$(command -v codelima)
  else
    printf 'no host-compatible codelima binary found; pass --binary PATH\n' >&2
    exit 2
  fi
fi
if [ ! -x "$diag_binary" ]; then
  printf 'codelima binary is not executable: %s\n' "$diag_binary" >&2
  exit 2
fi

if [ -z "$diag_output" ]; then
  mkdir -p "$project_root/tmp" || exit 2
  capture_stamp=$(date -u '+%Y%m%dT%H%M%SZ')
  diag_output="$project_root/tmp/terminal-freeze-$capture_stamp"
  if [ -e "$diag_output" ]; then
    diag_output="$diag_output-$$"
  fi
fi
# The installed Go collector owns probing, bounds and the bundle format.
# Keep this wrapper build-free so an incident capture does not change binaries.
exec "$diag_binary" --home "$diag_home" daemon diagnose \
  --output "$diag_output" --terminal-id "$diag_terminal_id" \
  --sample-seconds "$diag_sample_seconds"
