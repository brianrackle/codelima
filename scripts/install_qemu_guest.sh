#!/usr/bin/env bash
set -euo pipefail

# Optional development tooling for Debian/Ubuntu arm64 guests. Download through
# the host's authenticated APT indexes, but install no system packages.
qemu_tools=${1:?tools directory required}
qemu_work=${2:?project scratch directory required}
if [[ $(uname -s) != Linux || $(dpkg --print-architecture) != arm64 ]]; then
  printf '%s\n' 'This local QEMU installer supports Debian/Ubuntu Linux arm64.' >&2
  exit 1
fi
mkdir -p "$qemu_tools" "$qemu_work"
exec 9>"$qemu_tools/.qemu-install.lock"
flock 9
qemu_destination="$qemu_tools/qemu"
if [[ -f "$qemu_destination/.complete" ]] && "$qemu_destination/bin/qemu-system-aarch64" --version >/dev/null; then
  exit 0
fi
qemu_stage=$(mktemp -d "$qemu_work/install-qemu.XXXXXX")
trap 'rm -rf "$qemu_stage"' EXIT
mkdir -p "$qemu_stage/packages" "$qemu_stage/root" "$qemu_stage/bin" "$qemu_stage/share/qemu"
qemu_packages=(qemu-system-arm qemu-utils qemu-efi-aarch64 ipxe-qemu)
apt-get -s --no-install-recommends install "${qemu_packages[@]}" >"$qemu_stage/apt-plan.txt"
while read -r qemu_package; do qemu_packages+=("$qemu_package"); done < <(awk '/^Inst / { print $2 }' "$qemu_stage/apt-plan.txt")
mapfile -t qemu_packages < <(printf '%s\n' "${qemu_packages[@]}" | sort -u)
(
  cd "$qemu_stage/packages"
  apt-get download "${qemu_packages[@]}"
  sha256sum ./*.deb > ../packages.sha256
)
for qemu_package in "$qemu_stage"/packages/*.deb; do
  dpkg-deb -f "$qemu_package" Package Version >>"$qemu_stage/packages.manifest"
  # Exclude documentation links to host-installed packages: these may be
  # intentionally dangling in a local runtime tree (notably on VirtioFS).
  dpkg-deb --fsys-tarfile "$qemu_package" |
    tar -xf - -C "$qemu_stage/root" --no-same-owner --no-same-permissions \
      --exclude=./usr/share/doc --exclude=./usr/share/man
done
cat >"$qemu_stage/bin/qemu-system-aarch64" <<'WRAPPER'
#!/bin/sh
set -eu
qemu_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
qemu_tool=$(basename -- "$0")
export LD_LIBRARY_PATH="$qemu_root/root/usr/lib/aarch64-linux-gnu${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
export QEMU_MODULE_DIR="$qemu_root/root/usr/lib/aarch64-linux-gnu/qemu"
case "$qemu_tool" in
  qemu-system-*) exec "$qemu_root/root/usr/bin/$qemu_tool" -L "$qemu_root/root/usr/share/qemu" "$@" ;;
  *) exec "$qemu_root/root/usr/bin/$qemu_tool" "$@" ;;
esac
WRAPPER
chmod 0755 "$qemu_stage/bin/qemu-system-aarch64"
ln -s qemu-system-aarch64 "$qemu_stage/bin/qemu-img"
ln -s ../../root/usr/share/AAVMF/AAVMF_CODE.fd "$qemu_stage/share/qemu/edk2-aarch64-code.fd"
"$qemu_stage/bin/qemu-system-aarch64" --version
"$qemu_stage/bin/qemu-img" --version
touch "$qemu_stage/.complete"
rm -rf "$qemu_destination"
mv "$qemu_stage" "$qemu_destination"
