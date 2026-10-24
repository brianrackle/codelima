#!/usr/bin/env sh
set -eu

version="${1:?mise version is required}"
tools_dir="${2:?tools directory is required}"
work_root="${3:?project temp directory is required}"
platform="$(uname -s)"
case "$platform" in
  Linux) platform=linux ;;
  Darwin) platform=macos ;;
  *) echo "unsupported mise platform: $platform" >&2; exit 1 ;;
esac
arch="$(uname -m)"
case "$arch" in
  aarch64|arm64) arch=arm64 ;;
  x86_64) arch=x64 ;;
  *) echo "unsupported mise architecture: $arch" >&2; exit 1 ;;
esac
destination="$tools_dir/mise/$version"
if [ -x "$destination/bin/mise" ]; then
  exit 0
fi
mkdir -p "$work_root" "$tools_dir/mise"
scratch="$(mktemp -d "$work_root/install-mise.XXXXXX")"
trap 'rm -rf "$scratch"' EXIT INT TERM
asset="mise-v$version-$platform-$arch.tar.gz"
base="https://github.com/jdx/mise/releases/download/v$version"
curl -fsSL "$base/$asset" -o "$scratch/$asset"
curl -fsSL "$base/SHASUMS256.txt" -o "$scratch/checksums"
expected="$(awk -v asset="$asset" '$2 == asset || $2 == ("./" asset) { print $1 }' "$scratch/checksums")"
if [ -z "$expected" ]; then
  echo "mise archive is missing from release checksums" >&2
  exit 1
fi
actual="$(shasum -a 256 "$scratch/$asset" | awk '{print $1}')"
if [ "$actual" != "$expected" ]; then
  echo "mise checksum mismatch" >&2
  exit 1
fi
tar -xzf "$scratch/$asset" -C "$scratch"
mv "$scratch/mise" "$destination"
