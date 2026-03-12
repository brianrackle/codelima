# QA

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
