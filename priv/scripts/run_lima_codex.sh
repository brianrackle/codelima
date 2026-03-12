#!/usr/bin/env bash

set -euo pipefail

PROJECT_DIR="${1:?project directory required}"
VM_NAME="${2:?vm name required}"
SETUP_COMMANDS="${3:-}"
MOUNT_SPEC="${PROJECT_DIR}:w"

prepend_path_dir() {
  local dir="${1:-}"

  if [[ -z "$dir" || ! -d "$dir" ]]; then
    return
  fi

  case ":$PATH:" in
    *":$dir:"*) ;;
    *) PATH="$dir:$PATH" ;;
  esac
}

bootstrap_host_path() {
  local brew_prefix="${HOMEBREW_PREFIX:-}"
  local custom_paths="${LIMA_HOST_BIN_PATHS:-}"

  if [[ -n "$custom_paths" ]]; then
    local dir
    local old_ifs="$IFS"
    IFS=':'

    for dir in $custom_paths; do
      prepend_path_dir "$dir"
    done

    IFS="$old_ifs"
    export PATH
    return
  fi

  prepend_path_dir "/usr/local/bin"
  prepend_path_dir "/opt/homebrew/bin"
  prepend_path_dir "/home/linuxbrew/.linuxbrew/sbin"
  prepend_path_dir "/home/linuxbrew/.linuxbrew/bin"
  prepend_path_dir "${HOME:-}/.linuxbrew/sbin"
  prepend_path_dir "${HOME:-}/.linuxbrew/bin"

  if [[ -z "$brew_prefix" ]] && command -v brew >/dev/null 2>&1; then
    brew_prefix="$(brew --prefix 2>/dev/null || true)"
  fi

  if [[ -n "$brew_prefix" ]]; then
    prepend_path_dir "$brew_prefix/bin"
  fi

  export PATH
}

status() {
  printf '[bootstrap] %s\n' "$1" >&2
}

run_streamed() {
  if command -v stdbuf >/dev/null 2>&1; then
    stdbuf -oL -eL "$@" 2>&1 | tr '\r' '\n'
  else
    "$@" 2>&1 | tr '\r' '\n'
  fi
}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf 'Missing required command: %s\n' "$1" >&2
    exit 127
  fi
}

ensure_vm() {
  if limactl list --format '{{.Name}}' | grep -qx "$VM_NAME"; then
    run_streamed limactl start -y --set '.nestedVirtualization=true' --mount-only "$MOUNT_SPEC" "$VM_NAME"
  else
    run_streamed limactl start -y --set '.nestedVirtualization=true' --name="$VM_NAME" --mount-only "$MOUNT_SPEC" template:default
  fi
}

ensure_codex() {
  if ! limactl shell -y "$VM_NAME" command -v node >/dev/null 2>&1; then
    status 'installing Node inside the Lima VM'
    run_streamed limactl shell -y "$VM_NAME" sudo snap install node --classic
  fi

  if ! limactl shell -y "$VM_NAME" npm list -g @openai/codex >/dev/null 2>&1; then
    status 'installing codex-cli inside the Lima VM'
    run_streamed limactl shell -y "$VM_NAME" sudo npm install -g @openai/codex --progress=false --fund=false --loglevel=warn
  fi
}

run_project_setup() {
  if [[ -z "${SETUP_COMMANDS//[[:space:]]/}" ]]; then
    return
  fi

  local command_block
  command_block=$'set -euo pipefail\n'"${SETUP_COMMANDS}"

  status 'running project setup commands'
  run_streamed lima bash -lc "$command_block"
}

bootstrap_host_path
require_command limactl
require_command lima

cd "$PROJECT_DIR"
export LIMA_INSTANCE="$VM_NAME"
export TERM="xterm-256color"

status "starting Lima VM ${VM_NAME}"
ensure_vm

status 'ensuring codex-cli is available'
ensure_codex

run_project_setup

status 'launching codex-cli'
exec lima codex
