# QA

## List Verification

This flow verifies that the default `project list` and `node list` output is a concise table with the expected columns, including live VM state for nodes, while `--json` remains available for automation.

Prerequisites:

- run `make build`
- run the commands from the repository root

Setup:

```sh
ROOT_DIR="$(pwd)"
WORK_ROOT="$ROOT_DIR/tmp/qa-list"
rm -rf "$WORK_ROOT"
mkdir -p "$WORK_ROOT"
CODELIMA_HOME="$WORK_ROOT/.codelima"
```

Create a project and node:

```sh
./bin/codelima --home "$CODELIMA_HOME" project create --slug qa-list --workspace "$ROOT_DIR/test-project-dir" --setup-command "./script/setup"
./bin/codelima --home "$CODELIMA_HOME" node create --project qa-list --slug qa-list-node
```

Verify the default table output:

```sh
./bin/codelima --home "$CODELIMA_HOME" project list
./bin/codelima --home "$CODELIMA_HOME" node list
```

Expected result:

- `project list` prints a table with the columns `slug`, `uuid`, `workspace_path`, `runtime`, and `agent`
- the `project list` row includes `qa-list`, `$ROOT_DIR/test-project-dir`, `vm`, and `codex-cli`
- `node list` prints a table with the columns `slug`, `uuid`, `workspace_path`, `runtime`, `vm_status`, and `agent`
- the `node list` row includes `qa-list-node`, `$ROOT_DIR/test-project-dir`, `vm`, `created`, and `codex-cli`

Start the node and verify the VM status updates:

```sh
./bin/codelima --home "$CODELIMA_HOME" node start qa-list-node
./bin/codelima --home "$CODELIMA_HOME" node list
```

Expected result:

- both commands succeed
- the `node list` row for `qa-list-node` now shows `running` under `vm_status`

Verify structured output still works:

```sh
./bin/codelima --home "$CODELIMA_HOME" --json project list
./bin/codelima --home "$CODELIMA_HOME" --json node list
```

Expected result:

- both commands succeed
- both commands return JSON with `"ok": true`

Cleanup:

```sh
./bin/codelima --home "$CODELIMA_HOME" node delete qa-list-node
rm -rf "$WORK_ROOT"
```

## Tree Verification

This flow verifies that `project tree` includes both lineage projects and the nodes attached to each project.

Prerequisites:

- run `make build`
- run the commands from the repository root

Setup:

```sh
ROOT_DIR="$(pwd)"
WORK_ROOT="$ROOT_DIR/tmp/qa-tree"
rm -rf "$WORK_ROOT"
mkdir -p "$WORK_ROOT/root" "$WORK_ROOT/child"
CODELIMA_HOME="$WORK_ROOT/.codelima"
cp -R "$ROOT_DIR/test-project-dir/." "$WORK_ROOT/root"
```

Create a root project, a child project, and one node for each:

```sh
./bin/codelima --home "$CODELIMA_HOME" project create --slug qa-tree-root --workspace "$WORK_ROOT/root" --setup-command "./script/setup"
./bin/codelima --home "$CODELIMA_HOME" node create --project qa-tree-root --slug qa-tree-root-node
./bin/codelima --home "$CODELIMA_HOME" project fork qa-tree-root --slug qa-tree-child --workspace "$WORK_ROOT/child"
./bin/codelima --home "$CODELIMA_HOME" node create --project qa-tree-child --slug qa-tree-child-node
```

Verify the default tree output:

```sh
./bin/codelima --home "$CODELIMA_HOME" project tree
```

Expected result:

- the tree includes `qa-tree-root`
- the tree includes `node: qa-tree-root-node` under `qa-tree-root`
- the tree includes `qa-tree-child` under `qa-tree-root`
- the tree includes `node: qa-tree-child-node` under `qa-tree-child`

Verify structured output still includes nodes:

```sh
./bin/codelima --home "$CODELIMA_HOME" --json project tree
```

Expected result:

- the JSON result includes a `nodes` array on each project tree node
- the root `nodes` array includes `qa-tree-root-node`
- the child `nodes` array includes `qa-tree-child-node`

Cleanup:

```sh
./bin/codelima --home "$CODELIMA_HOME" node delete qa-tree-child-node
./bin/codelima --home "$CODELIMA_HOME" node delete qa-tree-root-node
rm -rf "$WORK_ROOT"
```

## Shell Verification

This flow verifies that `codelima shell` enters a healthy node in the guest-local project workspace copy instead of inheriting an unrelated host working directory.

Prerequisites:

- run `make build`
- run the commands from the repository root

Setup:

```sh
ROOT_DIR="$(pwd)"
WORK_ROOT="$ROOT_DIR/tmp/qa-shell"
rm -rf "$WORK_ROOT"
mkdir -p "$WORK_ROOT"
CODELIMA_HOME="$WORK_ROOT/.codelima"
```

Create and start a QA node:

```sh
./bin/codelima --home "$CODELIMA_HOME" project create --slug qa-shell --workspace "$ROOT_DIR/test-project-dir" --setup-command "./script/setup"
./bin/codelima --home "$CODELIMA_HOME" node create --project qa-shell --slug qa-shell-node
./bin/codelima --home "$CODELIMA_HOME" node start qa-shell-node
```

Non-interactive verification:

```sh
./bin/codelima --home "$CODELIMA_HOME" shell qa-shell-node -- pwd
```

Expected result:

- command exits successfully
- output is `$ROOT_DIR/test-project-dir`

Isolation verification:

```sh
./bin/codelima --home "$CODELIMA_HOME" shell qa-shell-node -- sh -lc 'printf vm-only > .vm-isolated'
test ! -e "$ROOT_DIR/test-project-dir/.vm-isolated"
```

Expected result:

- the guest command succeeds
- the host command succeeds because the marker file was not written into the host workspace

Interactive verification:

```sh
./bin/codelima --home "$CODELIMA_HOME" shell qa-shell-node
```

Inside the shell run:

```sh
pwd
exit
```

Expected result:

- `pwd` prints `$ROOT_DIR/test-project-dir`
- `exit` returns cleanly to the host shell

Cleanup:

```sh
./bin/codelima --home "$CODELIMA_HOME" node delete qa-shell-node
rm -rf "$WORK_ROOT"
```

## TUI Verification

This flow verifies that `codelima tui` renders the chosen shell-first layout, auto-switches the visible terminal when node selection changes, and preserves each node session while the TUI process is running.

Prerequisites:

- run `make build`
- run the commands from the repository root

Setup:

```sh
ROOT_DIR="$(pwd)"
WORK_ROOT="$ROOT_DIR/tmp/qa-tui"
rm -rf "$WORK_ROOT"
mkdir -p "$WORK_ROOT/root"
CODELIMA_HOME="$WORK_ROOT/.codelima"
cp -R "$ROOT_DIR/test-project-dir/." "$WORK_ROOT/root"
```

Create one project and two running nodes:

```sh
./bin/codelima --home "$CODELIMA_HOME" project create --slug qa-tui --workspace "$WORK_ROOT/root" --setup-command "./script/setup"
./bin/codelima --home "$CODELIMA_HOME" node create --project qa-tui --slug qa-tui-a
./bin/codelima --home "$CODELIMA_HOME" node create --project qa-tui --slug qa-tui-b
./bin/codelima --home "$CODELIMA_HOME" node start qa-tui-a
./bin/codelima --home "$CODELIMA_HOME" node start qa-tui-b
```

Run the TUI:

```sh
./bin/codelima --home "$CODELIMA_HOME" tui
```

Inside the TUI verify:

- the left pane renders the project and both nodes, and the right pane renders one visible terminal
- selecting `qa-tui-a` opens its shell session automatically
- `Tab` or `Enter` focuses the terminal, and `Alt-\`` returns focus to the tree
- in the `qa-tui-a` terminal, type `echo pending-a` without pressing `Enter`
- return to the tree, select `qa-tui-b`, and confirm the visible terminal switches to the `qa-tui-b` session
- in the `qa-tui-b` terminal, run `pwd` and confirm it prints `$WORK_ROOT/root`
- return to the tree, select `qa-tui-a` again, and confirm the partially typed `echo pending-a` input is still present

Cleanup:

```sh
./bin/codelima --home "$CODELIMA_HOME" node delete qa-tui-b
./bin/codelima --home "$CODELIMA_HOME" node delete qa-tui-a
rm -rf "$WORK_ROOT"
```

## Clone Verification

This flow verifies that `node clone` is a Lima VM copy that keeps the source guest workspace path and can clone a running source node by stopping and restarting it internally.

Prerequisites:

- run `make build`
- run the commands from the repository root

Setup:

```sh
ROOT_DIR="$(pwd)"
WORK_ROOT="$ROOT_DIR/tmp/qa-clone"
rm -rf "$WORK_ROOT"
mkdir -p "$WORK_ROOT/root"
CODELIMA_HOME="$WORK_ROOT/.codelima"
cp -R "$ROOT_DIR/test-project-dir/." "$WORK_ROOT/root"
```

Create and start the source node:

```sh
./bin/codelima --home "$CODELIMA_HOME" project create --slug qa-clone-root --workspace "$WORK_ROOT/root" --setup-command "./script/setup"
./bin/codelima --home "$CODELIMA_HOME" node create --project qa-clone-root --slug qa-clone-root-node
./bin/codelima --home "$CODELIMA_HOME" node start qa-clone-root-node
```

Clone the running source node and inspect both nodes:

```sh
./bin/codelima --home "$CODELIMA_HOME" node clone qa-clone-root-node --project-slug qa-clone-child --node-slug qa-clone-child-node --workspace "$WORK_ROOT/child"
./bin/codelima --home "$CODELIMA_HOME" node show qa-clone-root-node
./bin/codelima --home "$CODELIMA_HOME" node show qa-clone-child-node
```

Expected result:

- `node clone` succeeds even though the source node was running
- `node show qa-clone-root-node` reports `status: running`
- `node show qa-clone-child-node` reports `guest_workspace_path: $WORK_ROOT/root`
- `node show qa-clone-child-node` reports `workspace_seeded: true`
- `node show qa-clone-child-node` reports `bootstrap_completed: true`

Start the child node and verify its shell path:

```sh
./bin/codelima --home "$CODELIMA_HOME" node start qa-clone-child-node
./bin/codelima --home "$CODELIMA_HOME" shell qa-clone-child-node -- pwd
```

Expected result:

- both commands succeed
- `pwd` prints `$WORK_ROOT/root`

Cleanup:

```sh
./bin/codelima --home "$CODELIMA_HOME" node delete qa-clone-child-node
./bin/codelima --home "$CODELIMA_HOME" node delete qa-clone-root-node
rm -rf "$WORK_ROOT"
```

## Workspace Rebind Verification

This flow verifies that a moved workspace can be rebound to a project only after all project nodes are terminated.

Prerequisites:

- run `make build`
- run the commands from the repository root

Setup:

```sh
ROOT_DIR="$(pwd)"
WORK_ROOT="$ROOT_DIR/tmp/qa-rebind"
rm -rf "$WORK_ROOT"
mkdir -p "$WORK_ROOT"
CODELIMA_HOME="$WORK_ROOT/.codelima"
mkdir -p "$WORK_ROOT/original" "$WORK_ROOT/moved"
cp -R "$ROOT_DIR/test-project-dir/." "$WORK_ROOT/original"
cp -R "$ROOT_DIR/test-project-dir/." "$WORK_ROOT/moved"
```

Create a project and a node:

```sh
./bin/codelima --home "$CODELIMA_HOME" project create --slug qa-rebind --workspace "$WORK_ROOT/original" --setup-command "./script/setup"
./bin/codelima --home "$CODELIMA_HOME" node create --project qa-rebind --slug qa-rebind-node
```

Verify rebinding is blocked while the node is live:

```sh
./bin/codelima --home "$CODELIMA_HOME" project update qa-rebind --workspace "$WORK_ROOT/moved"
```

Expected result:

- command fails
- stderr contains `project workspace cannot be changed while nodes are live`

Delete the node and rebind the project:

```sh
./bin/codelima --home "$CODELIMA_HOME" node delete qa-rebind-node
./bin/codelima --home "$CODELIMA_HOME" project update qa-rebind --workspace "$WORK_ROOT/moved"
./bin/codelima --home "$CODELIMA_HOME" project show qa-rebind
```

Expected result:

- `project update` succeeds after the node is deleted
- `project show` reports `workspace_path: $WORK_ROOT/moved`

Cleanup:

```sh
rm -rf "$WORK_ROOT"
```
