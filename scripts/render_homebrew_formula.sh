#!/usr/bin/env sh
set -eu

REPO="${1:?repo is required}"
TAG="${2:?tag is required}"
MANIFEST_ROOT="${3:?manifest root is required}"
OUTPUT_PATH="${4:?output path is required}"
GO_BIN="${5:?go binary path is required}"

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
ROOT_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)

set --
found_manifest=false
while IFS= read -r manifest; do
  found_manifest=true
  set -- "$@" --manifest "$manifest"
done <<EOF
$(find "$MANIFEST_ROOT" -type f -name '*.json' | sort)
EOF
if [ "$found_manifest" = false ]; then
  echo "no manifest files found under $MANIFEST_ROOT" >&2
  exit 1
fi

cd "$ROOT_DIR"
"$GO_BIN" run ./cmd/codelima-release formula \
  --repo "$REPO" \
  --tag "$TAG" \
  --output "$OUTPUT_PATH" \
  "$@"
