#!/usr/bin/env bash
set -euo pipefail

# Optional user-local runtime installation, separate from the build toolchain.
# Digests are the official v2.1.0 release assets, pinned with the version.
lima_prefix=${1:?installation prefix required}
lima_work=${2:?project scratch directory required}
lima_archive=${3:-}
lima_version=2.1.0
lima_platform=$(uname -s)-$(uname -m)
case "$lima_platform" in
  Darwin-arm64) lima_digest=1da852bce2f98b8310fb53e5047e08ff798880ddf9ae4b3161d4de4e73777b34 ;;
  Linux-aarch64) lima_digest=8b806c81f38ad7ea8104f196b414e243726cad1dfa6aa52843608d55df8c5290 ;;
  Linux-x86_64) lima_digest=9e52c605790649aceac1f894eb2bb7cdb45897257faafb54ff30ce6a576bebfa ;;
  *) printf 'Unsupported Lima installation platform: %s\n' "$lima_platform" >&2; exit 1 ;;
esac
if [[ -e "$lima_prefix/bin/limactl" || -L "$lima_prefix/bin/limactl" ]]; then
  if [[ $("$lima_prefix/bin/limactl" --version) == "limactl version $lima_version" ]]; then
    printf 'Lima %s is already installed in %s\n' "$lima_version" "$lima_prefix"
    exit 0
  fi
  printf 'An existing limactl is installed in %s; update it with its original installer.\n' "$lima_prefix" >&2
  exit 1
fi
mkdir -p "$lima_work"
lima_stage=$(mktemp -d "$lima_work/install-lima.XXXXXX")
trap 'rm -rf "$lima_stage"' EXIT
if [[ -n "$lima_archive" ]]; then
  cp "$lima_archive" "$lima_stage/lima.tar.gz"
else
  curl --fail --location --retry 3 \
    "https://github.com/lima-vm/lima/releases/download/v$lima_version/lima-$lima_version-$lima_platform.tar.gz" \
    --output "$lima_stage/lima.tar.gz"
fi
if command -v sha256sum >/dev/null 2>&1; then
  lima_actual=$(sha256sum "$lima_stage/lima.tar.gz")
else
  lima_actual=$(shasum -a 256 "$lima_stage/lima.tar.gz")
fi
if [[ ${lima_actual%% *} != "$lima_digest" ]]; then
  printf '%s\n' 'Lima archive checksum mismatch; nothing installed.' >&2
  exit 1
fi
mkdir -p "$lima_prefix"
tar -xzf "$lima_stage/lima.tar.gz" -C "$lima_prefix"
"$lima_prefix/bin/limactl" --version
printf 'Installed Lima in %s. Ensure %s/bin is on PATH.\n' "$lima_prefix" "$lima_prefix"
