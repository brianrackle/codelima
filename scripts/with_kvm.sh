#!/usr/bin/env bash
set -euo pipefail

# Run as the current user with the KVM device's group for this process tree.
# Never chmod the device or change persistent user/group membership.
if [[ $# == 0 ]]; then
  printf '%s\n' 'usage: with_kvm.sh COMMAND [ARGS...]' >&2
  exit 2
fi
if [[ -r /dev/kvm && -w /dev/kvm ]]; then exec "$@"; fi
if [[ ! -c /dev/kvm ]]; then
  printf '%s\n' '/dev/kvm is unavailable in this guest.' >&2
  exit 1
fi
kvm_gid=$(stat -c %g /dev/kvm)
kvm_groups=$( { id -G | tr ' ' '\n'; printf '%s\n' "$kvm_gid"; } | sort -nu | paste -sd, -)
kvm_env_names=$(compgen -e | paste -sd, -)
kvm_user_env=("HOME=$HOME" "PATH=$PATH")
if [[ -v TMPDIR ]]; then kvm_user_env+=("TMPDIR=$TMPDIR"); fi
exec sudo -n "--preserve-env=$kvm_env_names" setpriv --reuid "$(id -u)" --regid "$(id -g)" --groups "$kvm_groups" \
  --no-new-privs env "${kvm_user_env[@]}" "$@"
