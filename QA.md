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
./bin/codelima --home "$CODELIMA_HOME" project create --slug qa-list --workspace "$ROOT_DIR/test-project-dir" --env-command "./script/setup"
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
./bin/codelima --home "$CODELIMA_HOME" project create --slug qa-tree-root --workspace "$WORK_ROOT/root" --env-command "./script/setup"
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
./bin/codelima --home "$CODELIMA_HOME" project create --slug qa-shell --workspace "$ROOT_DIR/test-project-dir" --env-command "./script/setup"
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

## Environment Config Verification

This flow verifies that reusable environment configs can be created once, assigned to multiple projects, resolved into new node bootstrap state, and removed only after projects stop referencing them.

Prerequisites:

- run `make build`
- run the commands from the repository root

Setup:

```sh
ROOT_DIR="$(pwd)"
WORK_ROOT="$ROOT_DIR/tmp/qa-env-config"
rm -rf "$WORK_ROOT"
mkdir -p "$WORK_ROOT/project-a" "$WORK_ROOT/project-b"
CODELIMA_HOME="$WORK_ROOT/.codelima"
cp -R "$ROOT_DIR/test-project-dir/." "$WORK_ROOT/project-a"
cp -R "$ROOT_DIR/test-project-dir/." "$WORK_ROOT/project-b"
```

Create one shared environment config and two projects that reference it:

```sh
./bin/codelima --home "$CODELIMA_HOME" environment create --slug qa-shared --env-command "./script/setup" --env-command "test -f README.md"
./bin/codelima --home "$CODELIMA_HOME" project create --slug qa-env-a --workspace "$WORK_ROOT/project-a" --env-config qa-shared
./bin/codelima --home "$CODELIMA_HOME" project create --slug qa-env-b --workspace "$WORK_ROOT/project-b" --env-config qa-shared
./bin/codelima --home "$CODELIMA_HOME" environment list
./bin/codelima --home "$CODELIMA_HOME" environment show qa-shared
./bin/codelima --home "$CODELIMA_HOME" project show qa-env-a
```

Expected result:

- `environment list` includes `qa-shared`
- `environment show qa-shared` includes `environment_commands` with both configured commands
- `project show qa-env-a` includes `environment_configs` with `qa-shared`

Update the shared config, create a new node from one of the projects, and verify the resolved bootstrap state:

```sh
./bin/codelima --home "$CODELIMA_HOME" environment update qa-shared --env-command "pwd >/dev/null"
./bin/codelima --home "$CODELIMA_HOME" environment show qa-shared
./bin/codelima --home "$CODELIMA_HOME" node create --project qa-env-b --slug qa-env-b-node
grep -R '"pwd >/dev/null"' "$CODELIMA_HOME/nodes"
./bin/codelima --home "$CODELIMA_HOME" node start qa-env-b-node
```

Expected result:

- `environment show qa-shared` now includes only `pwd >/dev/null`
- the `grep` output includes the new command inside the created node's `bootstrap.json`
- `node start qa-env-b-node` succeeds

Verify the config cannot be deleted while still referenced, then clear the references and delete it:

```sh
./bin/codelima --home "$CODELIMA_HOME" environment delete qa-shared
./bin/codelima --home "$CODELIMA_HOME" project update qa-env-a --clear-env-configs
./bin/codelima --home "$CODELIMA_HOME" project update qa-env-b --clear-env-configs
./bin/codelima --home "$CODELIMA_HOME" environment delete qa-shared
./bin/codelima --home "$CODELIMA_HOME" environment list
```

Expected result:

- the first `environment delete qa-shared` fails because projects still reference it
- both `project update --clear-env-configs` commands succeed
- the second `environment delete qa-shared` succeeds
- the final `environment list` no longer includes `qa-shared`

Cleanup:

```sh
./bin/codelima --home "$CODELIMA_HOME" node delete qa-env-b-node || true
./bin/codelima --home "$CODELIMA_HOME" project delete qa-env-a || true
./bin/codelima --home "$CODELIMA_HOME" project delete qa-env-b || true
./bin/codelima --home "$CODELIMA_HOME" environment delete qa-shared || true
rm -rf "$WORK_ROOT"
```

## TUI Verification

This flow verifies that running `codelima` with no command renders the chosen shell-first layout, lets you manage selected projects and nodes from the tree, auto-switches the visible terminal when node selection changes, and preserves each node session while the TUI process is running. It also verifies the preferred Ghostty-backed terminal path, including scrollback, hyperlink handling, and the apt/dpkg progress case that previously froze the app.

Prerequisites:

- run `make build`
- run the commands from the repository root

Setup:

```sh
ROOT_DIR="$(pwd)"
WORK_ROOT="$ROOT_DIR/tmp/qa-tui"
rm -rf "$WORK_ROOT"
mkdir -p "$WORK_ROOT/root" "$WORK_ROOT/extra"
CODELIMA_HOME="$WORK_ROOT/.codelima"
cp -R "$ROOT_DIR/test-project-dir/." "$WORK_ROOT/root"
```

Create one root project, a running node, and a forked child project for later patch verification:

```sh
./bin/codelima --home "$CODELIMA_HOME" project create --slug qa-tui --workspace "$WORK_ROOT/root" --env-command "./script/setup"
./bin/codelima --home "$CODELIMA_HOME" node create --project qa-tui --slug qa-tui-a
./bin/codelima --home "$CODELIMA_HOME" node start qa-tui-a
./bin/codelima --home "$CODELIMA_HOME" project fork qa-tui --slug qa-tui-child --workspace "$WORK_ROOT/child"
./bin/codelima --home "$CODELIMA_HOME" node create --project qa-tui-child --slug qa-tui-child-a
```

Run the TUI:

```sh
./bin/codelima --home "$CODELIMA_HOME"
```

Inside the TUI verify:

- the left pane renders the available projects and nodes, and the right pane renders either node details or one visible terminal
- press `g`, create reusable environment config `qa-shared`, confirm the command menu opens immediately, add `./script/setup`, then add `direnv allow`, move `direnv allow` above `./script/setup`, remove `direnv allow` through the selector plus confirmation flow, and confirm the menu stays open after each edit
- press `a`, create a standalone project `qa-tui-extra` with workspace `$WORK_ROOT/extra`, open the Environment Configs selector from the dialog, choose `qa-shared`, and confirm it appears as a second top-level project without a long frozen pause
- select project `qa-tui-extra`, confirm the right pane lists `qa-shared` under environment configs
- with `qa-tui-extra` still selected, press `u`, open the Environment Configs selector from the update dialog, clear the selection, submit, and confirm the right pane shows no environment configs
- with `qa-tui-extra` still selected, press `e`, add environment command `./script/setup`, remove it through the selector plus confirmation flow, add it again, and confirm the project environment menu stays open after each edit
- with `qa-tui-extra` still selected, press `e` again, clear the environment commands, and confirm the right pane shows none configured
- select project `qa-tui`, press `n`, create node `qa-tui-b`, and confirm the new node appears under the project without opening a shell session
- with `qa-tui` still selected, press `u`, change the project slug to `qa-tui-root`, submit, and confirm the project tree updates in place
- when node create, start, stop, clone, or delete is in progress, confirm the TUI shows streamed Lima or guest-command output instead of freezing on a blank status line
- selecting `qa-tui-a` opens its shell session automatically
- `Tab` or `Enter` focuses the terminal, and `Alt-\`` returns focus to the tree
- in the `qa-tui-a` terminal, type `echo pending-a` without pressing `Enter`
- return to the tree, select `qa-tui-b`, press `s`, and confirm the node starts and opens its shell session automatically
- in the `qa-tui-b` terminal, run `pwd` and confirm it prints `$WORK_ROOT/root`
- drag over the visible `pwd` output in the terminal pane, then confirm `pbpaste` in a second host shell contains the copied text
- with the `qa-tui-b` terminal focused and the guest at a normal shell prompt, spin the mouse wheel up and down and confirm local scrollback moves without freezing the app
- return to the tree, select `qa-tui-a` again, and confirm the partially typed `echo pending-a` input is still present
- in a focused node terminal, start an app that captures the mouse such as `vim`, then confirm `Shift`-drag still performs a local text copy instead of sending the drag to the guest
- select `qa-tui-b`, press `s`, and confirm the node stops while remaining selectable in the tree
- with `qa-tui-b` selected, press `c`, clone it into node `qa-tui-b-clone`, then confirm the cloned node appears under project `qa-tui-root`
- click a visible workspace path in the right pane and confirm the host opens that path or dispatches it to the default `file://` handler
- refocus the `qa-tui-a` or `qa-tui-b` terminal, print an OSC 8 hyperlink such as `printf '\033]8;;https://example.com\033\\example\033]8;;\033\\\n'`, click the visible link text, and confirm the host opens it
- in a focused node terminal, run `sudo apt-get install -y sl` and confirm the embedded terminal remains responsive past the `Reading database ...` progress output and returns to a prompt

In a second host shell, create a host-side change in the forked child project:

```sh
printf 'qa patch\n' > "$WORK_ROOT/child/README.md"
```

Back in the TUI verify patch operations from the forked child node:

- select `qa-tui-child-a`, press `p`, choose propose, target project `qa-tui-root`, and record the new patch ID shown in the status line or related patch list
- press `p` again, choose approve, enter that patch ID, and confirm the patch status becomes `approved`
- press `p` again, choose apply, enter that patch ID, and confirm the patch status becomes `applied`
- in the second host shell, confirm `cat "$WORK_ROOT/root/README.md"` now prints `qa patch`
- change the child project again in the second host shell with `printf 'qa reject\n' > "$WORK_ROOT/child/README.md"`
- propose a second patch from `qa-tui-child-a` to `qa-tui-root`, then use `p` and reject on that second patch ID, and confirm the patch status becomes `rejected`
- select `qa-tui-b-clone`, press `d`, delete the cloned node, and confirm it disappears from project `qa-tui-root`
- select `qa-tui-child-a`, press `d`, delete the child node, then select project `qa-tui-child`, press `x`, and confirm the child project disappears from the tree
- select `qa-tui-b`, press `d`, delete it, and confirm it disappears from the tree
- select project `qa-tui-extra`, press `x`, and confirm the standalone project disappears from the tree

Cleanup the remaining root project from either the TUI or the CLI:

- in the TUI, delete `qa-tui-a`, then select project `qa-tui-root` and delete it
- or run the equivalent CLI cleanup commands below after quitting the TUI

Cleanup:

```sh
./bin/codelima --home "$CODELIMA_HOME" node delete qa-tui-a
./bin/codelima --home "$CODELIMA_HOME" node delete qa-tui-b
./bin/codelima --home "$CODELIMA_HOME" node delete qa-tui-b-clone || true
./bin/codelima --home "$CODELIMA_HOME" node delete qa-tui-child-a
./bin/codelima --home "$CODELIMA_HOME" project delete qa-tui-child || true
./bin/codelima --home "$CODELIMA_HOME" project delete qa-tui-extra || true
./bin/codelima --home "$CODELIMA_HOME" project delete qa-tui-root || true
./bin/codelima --home "$CODELIMA_HOME" environment delete qa-shared || true
rm -rf "$WORK_ROOT"
```

## Clone Verification

This flow verifies that `node clone` is a Lima VM copy that keeps the source guest workspace path, stays in the same project, and can clone a running source node by stopping and restarting it internally.

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
./bin/codelima --home "$CODELIMA_HOME" project create --slug qa-clone-root --workspace "$WORK_ROOT/root" --env-command "./script/setup"
./bin/codelima --home "$CODELIMA_HOME" node create --project qa-clone-root --slug qa-clone-root-node
./bin/codelima --home "$CODELIMA_HOME" node start qa-clone-root-node
```

Clone the running source node and inspect the project tree plus both nodes:

```sh
./bin/codelima --home "$CODELIMA_HOME" node clone qa-clone-root-node --node-slug qa-clone-child-node
./bin/codelima --home "$CODELIMA_HOME" project tree
./bin/codelima --home "$CODELIMA_HOME" node show qa-clone-root-node
./bin/codelima --home "$CODELIMA_HOME" node show qa-clone-child-node
```

Expected result:

- `node clone` succeeds even though the source node was running
- `project tree` still shows a single project, `qa-clone-root`, with both nodes attached to it
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
./bin/codelima --home "$CODELIMA_HOME" project create --slug qa-rebind --workspace "$WORK_ROOT/original" --env-command "./script/setup"
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

## Packaging Verification

This flow verifies that the repository can build a release archive for the current platform, emit the matching manifest, and render a Homebrew formula from those manifests without hand-editing release metadata.

Prerequisites:

- run `make init`
- run the commands from the repository root

Setup:

```sh
ROOT_DIR="$(pwd)"
WORK_ROOT="$ROOT_DIR/tmp/qa-package"
DIST_DIR="$WORK_ROOT/dist"
rm -rf "$WORK_ROOT"
mkdir -p "$DIST_DIR"
```

Build the package and inspect the generated files:

```sh
make package PACKAGE_VERSION=0.0.0-qa DIST_DIR="$DIST_DIR"
find "$DIST_DIR" -maxdepth 1 -type f | sort
ARCHIVE="$(find "$DIST_DIR" -maxdepth 1 -type f -name '*.tar.gz' | head -n 1)"
MANIFEST="$(find "$DIST_DIR" -maxdepth 1 -type f -name '*.json' | head -n 1)"
tar -tzf "$ARCHIVE"
cat "$MANIFEST"
```

Expected result:

- `make package` succeeds
- `DIST_DIR` contains one `.tar.gz` archive and one `.json` manifest
- the archive contains `codelima_0.0.0-qa_<goos>_<goarch>/bin/codelima`
- the archive contains `codelima_0.0.0-qa_<goos>_<goarch>/bin/codelima-real`
- the archive contains `codelima_0.0.0-qa_<goos>_<goarch>/lib/libghostty-vt.dylib` on macOS or `libghostty-vt.so` on Linux
- the manifest reports the same `version`, `goos`, `goarch`, and `asset_name`

Render the Homebrew formula from the generated manifest:

```sh
make package-formula \
  PACKAGE_VERSION=0.0.0-qa \
  RELEASE_TAG=v0.0.0-qa \
  RELEASE_REPO=brianrackle/codelima \
  DIST_DIR="$DIST_DIR" \
  FORMULA_OUTPUT="$DIST_DIR/Formula/codelima.rb"
cat "$DIST_DIR/Formula/codelima.rb"
```

Expected result:

- `make package-formula` succeeds
- the formula contains `depends_on "git"`
- the formula contains `depends_on "lima"`
- the formula URLs point at `https://github.com/brianrackle/codelima/releases/download/v0.0.0-qa/`
- the formula references the generated asset name and sha256 from the manifest

Cleanup:

```sh
rm -rf "$WORK_ROOT"
```
