# QA

## Shell Verification

This flow verifies that `codelima shell` enters a healthy node in the mounted project workspace instead of inheriting an unrelated host working directory.

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
