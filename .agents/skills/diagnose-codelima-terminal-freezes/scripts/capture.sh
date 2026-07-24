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
if [ -e "$diag_output" ]; then
  printf 'capture output already exists: %s\n' "$diag_output" >&2
  exit 2
fi
mkdir "$diag_output" || exit 2
chmod 700 "$diag_output" 2>/dev/null || true

run_probe() {
  probe_name=$1
  shift
  "$diag_binary" --home "$diag_home" --json "$@" \
    >"$diag_output/$probe_name.json" \
    2>"$diag_output/$probe_name.err"
  probe_status=$?
  printf '%s\n' "$probe_status" >"$diag_output/$probe_name.exit"
  return 0
}

copy_if_readable() {
  source_path=$1
  target_name=$2
  if [ -r "$source_path" ]; then
    cp "$source_path" "$diag_output/$target_name"
  fi
}

tail_if_readable() {
  source_path=$1
  target_name=$2
  if [ -r "$source_path" ]; then
    tail -n 2000 "$source_path" >"$diag_output/$target_name"
  fi
}

{
  printf 'captured_at_utc=%s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  printf 'project_root=%s\n' "$project_root"
  printf 'codelima_home=%s\n' "$diag_home"
  printf 'codelima_binary=%s\n' "$diag_binary"
  printf 'host_os=%s\n' "$host_os"
  printf 'host_arch=%s\n' "$host_arch"
  printf 'platform_tag=%s\n' "$platform_tag"
  printf 'shell_pid=%s\n' "$$"
} >"$diag_output/capture.txt"

"$diag_binary" --version >"$diag_output/codelima-version.txt" 2>&1 || true
file "$diag_binary" >"$diag_output/codelima-binary.txt" 2>&1 || true
uname -a >"$diag_output/uname.txt" 2>&1 || true
if command -v sw_vers >/dev/null 2>&1; then
  sw_vers >"$diag_output/sw-vers.txt" 2>&1 || true
fi

copy_if_readable "$diag_home/_daemon/daemon.identity" "daemon.identity"
copy_if_readable "$diag_home/_daemon/session.json" "session.json"
tail_if_readable "$diag_home/_daemon/daemon.log" "daemon.log.tail"
tail_if_readable "$diag_home/_logs/codelima.log" "codelima.log.tail"
tail_if_readable "$diag_home/_logs/codelima.log.1" "codelima.log.1.tail"

daemon_pid=
if [ -r "$diag_home/_daemon/daemon.pid" ]; then
  daemon_pid=$(tr -d '[:space:]' <"$diag_home/_daemon/daemon.pid")
fi
case "$daemon_pid" in
  ''|*[!0-9]*)
    printf 'unavailable\n' >"$diag_output/daemon-pid.txt"
    daemon_pid=
    ;;
  *)
    printf '%s\n' "$daemon_pid" >"$diag_output/daemon-pid.txt"
    ps -p "$daemon_pid" -o pid,ppid,lstart,state,%cpu,%mem,command \
      >"$diag_output/daemon-ps.txt" 2>&1 || true
    if command -v lsof >/dev/null 2>&1; then
      lsof -p "$daemon_pid" >"$diag_output/daemon-lsof.txt" 2>&1 || true
    fi
    if [ -d "/proc/$daemon_pid" ]; then
      copy_if_readable "/proc/$daemon_pid/status" "proc-status.txt"
      copy_if_readable "/proc/$daemon_pid/limits" "proc-limits.txt"
      copy_if_readable "/proc/$daemon_pid/stack" "proc-stack.txt"
    fi
    if [ "$host_os" = "Darwin" ] &&
       [ "$diag_sample_seconds" -gt 0 ] &&
       command -v sample >/dev/null 2>&1; then
      sample "$daemon_pid" "$diag_sample_seconds" 1 \
        -file "$diag_output/daemon-sample.txt" \
        >"$diag_output/daemon-sample.stdout" \
        2>"$diag_output/daemon-sample.err" || true
    fi
    ;;
esac

run_probe "daemon-status" daemon status
run_probe "terminal-list" terminal list
run_probe "daemon-snapshot" daemon snapshot

if [ -z "$diag_terminal_id" ] && [ -r "$diag_output/terminal-list.json" ]; then
  diag_terminal_id=$(
    sed -n 's/.*"terminal_id":[[:space:]]*"\([^"]*\)".*/\1/p' \
      "$diag_output/terminal-list.json" |
      head -n 1
  )
fi
if [ -n "$diag_terminal_id" ]; then
  printf '%s\n' "$diag_terminal_id" >"$diag_output/probed-terminal-id.txt"
  run_probe "terminal-read" terminal read "$diag_terminal_id"
fi

status_exit=$(cat "$diag_output/daemon-status.exit")
list_exit=$(cat "$diag_output/terminal-list.exit")
read_exit=not-run
if [ -r "$diag_output/terminal-read.exit" ]; then
  read_exit=$(cat "$diag_output/terminal-read.exit")
fi

{
  printf '# CodeLima terminal-freeze capture\n\n'
  printf -- '- Capture directory: `%s`\n' "$diag_output"
  printf -- '- Daemon PID: `%s`\n' "${daemon_pid:-unavailable}"
  printf -- '- Daemon status exit: `%s`\n' "$status_exit"
  printf -- '- Terminal list exit: `%s`\n' "$list_exit"
  printf -- '- Terminal read exit: `%s`\n\n' "$read_exit"
  printf '## Initial classification\n\n'
  if [ "$status_exit" = "0" ] && [ "$list_exit" = "0" ]; then
    if [ "$read_exit" = "not-run" ]; then
      printf 'The daemon control plane responded; no terminal actor was available to probe.\n'
    elif [ "$read_exit" = "0" ]; then
      printf 'The daemon control plane and selected terminal actor both responded; investigate event delivery and client snapshot/redraw handling.\n'
    else
      printf 'The daemon control plane responded but the terminal actor probe failed; investigate the selected actor and process-wide Ghostty bridge serialization.\n'
    fi
  else
    printf 'The daemon control-plane probes failed; investigate the daemon server, socket state, or process-wide runtime blockage.\n'
  fi
  if [ -r "$diag_output/daemon-sample.txt" ] &&
     grep -q 'withGhosttyStderrSuppressed' "$diag_output/daemon-sample.txt"; then
    printf '\nThe native sample contains `withGhosttyStderrSuppressed`; inspect the owning Ghostty bridge call and mutex waiters.\n'
  fi
  printf '\n## Handling\n\n'
  printf 'Keep this bundle local. Review logs, argv, paths, and terminal metadata for sensitive information before sharing. No recovery action was performed.\n'
} >"$diag_output/summary.md"

printf 'CodeLima terminal-freeze evidence captured at %s\n' "$diag_output"
