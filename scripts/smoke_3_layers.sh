#!/usr/bin/env sh
set -eu

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT_DIR/bin/codelima"
FIXTURE="$ROOT_DIR/test-project-dir"

if [ ! -x "$BIN" ]; then
  echo "build the CLI first with make build" >&2
  exit 1
fi

WORK_ROOT="$ROOT_DIR/tmp/smoke-3-layers"
ROOT_WORKSPACE="$WORK_ROOT/root"
CODELIMA_HOME="$WORK_ROOT/.codelima"
export CODELIMA_HOME

rm -rf "$WORK_ROOT"
mkdir -p "$ROOT_WORKSPACE"
cp -R "$FIXTURE/." "$ROOT_WORKSPACE"

cleanup() {
  "$BIN" --home "$CODELIMA_HOME" node delete grandchild-node >/dev/null 2>&1 || true
  "$BIN" --home "$CODELIMA_HOME" node delete child-node >/dev/null 2>&1 || true
  "$BIN" --home "$CODELIMA_HOME" node delete root-node >/dev/null 2>&1 || true
  rm -rf "$WORK_ROOT"
}
trap cleanup EXIT INT TERM

"$BIN" --home "$CODELIMA_HOME" project create \
  --slug root \
  --workspace "$ROOT_WORKSPACE" \
  --setup-command "./script/setup" >/dev/null

"$BIN" --home "$CODELIMA_HOME" node create \
  --project root \
  --slug root-node >/dev/null

"$BIN" --home "$CODELIMA_HOME" node start root-node >/dev/null
"$BIN" --home "$CODELIMA_HOME" node stop root-node >/dev/null

"$BIN" --home "$CODELIMA_HOME" node clone root-node \
  --node-slug child-node >/dev/null

"$BIN" --home "$CODELIMA_HOME" node start child-node >/dev/null
"$BIN" --home "$CODELIMA_HOME" node stop child-node >/dev/null

"$BIN" --home "$CODELIMA_HOME" node clone child-node \
  --node-slug grandchild-node >/dev/null

"$BIN" --home "$CODELIMA_HOME" node start grandchild-node >/dev/null
"$BIN" --home "$CODELIMA_HOME" node stop grandchild-node >/dev/null

"$BIN" --home "$CODELIMA_HOME" project tree
"$BIN" --home "$CODELIMA_HOME" node list
