# CodeLima

## Install

Install the latest packaged release from Homebrew:

```sh
brew tap brianrackle/codelima
brew install codelima
```

The Homebrew formula installs the packaged `codelima` binary plus the bundled `libghostty-vt` runtime library, and declares `git` and `lima` as runtime dependencies.

`CodeLima` is a Go CLI for managing lineage-aware projects, Lima-backed nodes, and a shell-first TUI.

The CLI manages:

- projects and immutable workspace snapshots under `CODELIMA_HOME`
- Lima-backed nodes delegated through `limactl`
- a canonical shell surface that passes through to `limactl shell`
- a shell-first TUI that keeps one live terminal session per opened node while the TUI process is running

## Supported Systems

Current packaged releases are built for:

- macOS `arm64`
- macOS `amd64`
- Linux `arm64`
- Linux `amd64`

Repository-local development and CI are exercised on macOS and Linux.

## Prerequisites

- macOS or Linux
- `curl`, `tar`, `git`, and `make`
- a working C toolchain for Go's `cgo` path (`clang` via Xcode Command Line Tools on macOS, or the equivalent build tools on Linux)
- Lima installed and working on the host

`make init` installs the Go toolchain, `golangci-lint`, Zig, and a patched `libghostty-vt` build locally under `.tooling/<os>-<arch>`; system Go or Zig installs are not required. The per-platform layout avoids host and guest toolchain collisions when the same repository is used from both macOS and a Linux VM.

## Setup

```sh
make init
make build
```

The binary is written to `./bin/codelima`.

To build a distributable archive for the current platform:

```sh
make package PACKAGE_VERSION=1.2.3 DIST_DIR=./tmp/dist
```

That writes a Homebrew-ready tarball plus a JSON manifest under `./tmp/dist`.

## User Guide

The examples in this guide assume `codelima` is installed and available on `PATH`.
For repository-local development, use `make run ARGS="..."` or `./bin/codelima ...`.

### Capabilities

- register host workspaces as lineage-aware projects
- capture immutable snapshots on demand for lineage-aware project workflows
- create, start, stop, clone, inspect, and delete Lima-backed nodes, choosing either an isolated copied workspace or a writable mounted workspace per node
- detect and clean up incomplete node metadata directories left by failed node creation attempts
- create reusable environment configs and assign them to multiple projects as shared bootstrap defaults, including built-in `codex` and `claude-code` installers
- open an interactive shell or run one-off commands inside a node, starting in a guest-local copy of the project workspace that keeps the same absolute path
- browse the project tree, manage selected projects and nodes, and jump between preserved per-node sessions in a Ghostty-backed embedded terminal by running `codelima` with no command
- inspect local control-plane health with `doctor` and resolved defaults with `config show`
- view project lineage with attached project nodes via `project tree`

### Command Structure

Most commands follow this shape:

```sh
codelima [--home PATH] [--json] <group> <command> [flags]
```

Running `codelima` with no command opens the TUI:

```sh
codelima [--home PATH]
```

Useful global flags:

- `--home PATH` points the CLI at a specific `CODELIMA_HOME`
- `--json` returns structured output for automation
- `--log-level LEVEL` reserves a verbosity setting for future CLI logging

`project list` renders a compact table by default with `slug`, `uuid`, `workspace_path`, `runtime`, and `agent`. `node list` adds `workspace_mode` and `vm_status` so both the workspace binding strategy and live VM state are visible without switching to `node show`. `node cleanup-incomplete` also renders a compact table, showing each incomplete node directory plus any recovered Lima instance name. Use `--json` when you need the full structured payload for scripts.

### Typical Workflow

1. Check host readiness and inspect the active config.

```sh
codelima doctor
codelima config show
```

If `doctor` reports an incomplete node metadata directory from an older failed `node create`, inspect the candidates first and then remove them explicitly:

```sh
codelima node cleanup-incomplete
codelima node cleanup-incomplete --apply
```

`node cleanup-incomplete` is a dry run by default. Add `--apply` only after you confirm the listed `node_dir` values are stale partial metadata directories rather than healthy nodes.

2. Inspect the built-in reusable environment configs, then register a workspace as a project.

```sh
codelima environment list
codelima environment show codex

codelima project create \
  --slug root \
  --workspace ./test-project-dir \
  --env-config codex
```

Fresh homes seed two reusable environment configs:

- `codex`: installs Node via `snap`, then installs `@openai/codex` globally with `npm`
- `claude-code`: runs `curl -fsSL https://claude.ai/install.sh | bash`

Projects can combine those shared defaults with project-specific environment commands, or you can create your own reusable config when you need a team- or repo-specific bootstrap:

```sh
codelima environment create \
  --slug shared-dev \
  --env-command ./script/setup \
  --env-command "direnv allow"

codelima environment update shared-dev --env-command "mise install"
codelima environment list
codelima environment show codex
codelima environment show shared-dev
codelima project update root --env-command ./script/setup --env-command "direnv allow"
codelima project update root --env-config codex --env-config shared-dev
codelima project update root --clear-env-configs
codelima project update root --clear-env-commands
```

3. Create and start a Lima-backed node for that project.

```sh
codelima node create --project root --slug root-node
codelima node create --project root --slug root-mounted --workspace-mode mounted
codelima node start root-node
codelima node status root-node
```

`node create` supports two workspace modes:

- `copy` is the default. On first start, CodeLima copies the host workspace into the VM at the same absolute path and keeps later guest edits isolated inside the VM.
- `mounted` mounts the host workspace writable into the VM at the same absolute path, so guest edits are reflected in the host workspace immediately.

4. Open a shell or run a one-off command inside the node.

```sh
codelima shell root-node
codelima shell root-node -- uname -a
```

Or open the shell-first TUI and switch between preserved per-node sessions from the project tree:

```sh
codelima
```

Inside the TUI, selecting a node auto-switches the visible terminal. `Alt-\`` toggles between the tree view and a terminal-focused full-width shell for the selected running node. Clicking the tree moves focus back to the tree and restores the split view without destroying the shell session.
Selecting a project exposes project actions in the right pane: create a node, manage the project's environment commands and shared config refs, update the project binding, or delete the project. The create-node dialog now lets you choose between a copied workspace and a writable mounted workspace for the new node. Selecting a node exposes node actions: start or stop it, delete it, or clone it into another node in the same project. Non-running nodes stay selectable so you can manage them before opening a shell session.
Project creation and environment config management are global tree actions, so you can add a new top-level project or reusable config even when the tree is empty. Fresh homes already include `codex` and `claude-code` in those selectors. The project create and update dialogs use an environment-config selector instead of asking you to type config slugs, and `[g]` manage config opens a selector before the config command menu. Creating a reusable config now drops directly into the same command editor used for later edits, so you can add, remove, confirm, and reorder commands without retyping numbered positions.
Project create and update save only project metadata, while long-running Lima-backed node actions stream live `limactl` and guest bootstrap output in a TUI overlay instead of freezing the screen. Workspace paths and URLs shown in the right pane are clickable, and OSC 8 hyperlinks emitted inside the terminal pane are clickable too. Inside the terminal pane, a plain left-button drag copies the currently visible terminal text to the host clipboard when the guest is not actively capturing the mouse, and `Shift`-drag forces that local copy behavior even when an application such as Vim has enabled mouse handling. The mouse wheel scrolls local terminal scrollback when the guest is not actively capturing the mouse, and falls through to the guest when mouse tracking or alternate-screen scroll handling is enabled.

The tree is keyboard and mouse driven. In tree focus, the footer mirrors the currently available action hotkeys for the selected item, while the right pane keeps the fuller action context. The common flow is to select a project or node in the tree and then press the matching letter key:

- global tree actions: `[a]` add project, `[g]` manage reusable environment configs
- project actions: `[n]` create node, `[e]` manage environment commands and config refs, `[u]` update project, `[x]` delete project
- node actions: `[s]` start or stop node, `[d]` delete node, `[c]` clone node

In the default `copy` mode, the first successful start copies the host project workspace into the VM at the same absolute path it has on the host. The host workspace is not mounted into the VM, so guest-side edits stay isolated inside the guest unless you explicitly bring them back out.

In `mounted` mode, CodeLima mounts the host workspace writable into the VM instead of copying it, so guest-side edits are immediately reflected on the host. `node create` and the first `node start` still require the registered host workspace to exist. Rebind the project before creating a replacement node from a moved host workspace:

```sh
codelima node delete root-node
codelima project update root --workspace /new/host/path
codelima node create --project root --slug root-node
codelima node start root-node
```

5. Fork the project when you need a child workspace and direct project lineage.

```sh
codelima project fork root --slug child --workspace /tmp/codelima-child
codelima node create --project child --slug child-node
```

6. Clone a node when you want another VM in the same project.

```sh
codelima node clone root-node --node-slug root-node-clone
```

`node clone` copies the source VM at the Lima layer. If the source node is running, CodeLima stops it, clones it, and starts it again. The cloned node stays in the same project and keeps the same workspace mode, workspace path binding, and bootstrap state as the source VM.

### Useful Examples

Create an isolated metadata root for a temporary session:

```sh
codelima --home /tmp/codelima-dev doctor
```

List projects and print the lineage tree with attached nodes:

```sh
codelima project list
codelima node list
codelima project tree
codelima
```

Inspect node history and runtime state:

```sh
codelima node show root-node
codelima node logs root-node
codelima node status root-node
```

Create three cloned VMs in one project from `test-project-dir`:

```sh
codelima environment create --slug shared-dev --env-command ./script/setup
codelima project create --slug root --workspace ./test-project-dir --env-config shared-dev
codelima node create --project root --slug root-node
codelima node start root-node
codelima node clone root-node --node-slug child-node
codelima node start child-node
codelima node clone child-node --node-slug grandchild-node
codelima project tree
codelima node list
```

Rebind a project after moving its workspace on the host:

```sh
codelima node delete root-node
codelima project update root --workspace /Users/you/Projects/codelima/test-project-dir
codelima project show root
```

Clean up a node while keeping historical metadata:

```sh
codelima node stop root-node
codelima node delete root-node
```

## Make Shortcuts

```sh
make run ARGS="doctor"
make run ARGS="config show"
make run ARGS="environment create --slug shared-dev --env-command ./script/setup"
make run ARGS="project create --slug root --workspace ./test-project-dir --env-config shared-dev"
make run ARGS="node create --project root --slug root-node"
make run ARGS="node start root-node"
make run ARGS="shell root-node -- uname -a"
make tui ARGS="--home /tmp/codelima-dev/.codelima"
make package PACKAGE_VERSION=1.2.3 DIST_DIR=./tmp/dist
make package-formula PACKAGE_VERSION=1.2.3 RELEASE_TAG=v1.2.3 RELEASE_REPO=brianrackle/codelima DIST_DIR=./tmp/dist FORMULA_OUTPUT=./tmp/dist/Formula/codelima.rb
```

## Tooling

```sh
make fmt
make lint
make test
make build
make package PACKAGE_VERSION=1.2.3 DIST_DIR=./tmp/dist
make package-formula PACKAGE_VERSION=1.2.3 RELEASE_TAG=v1.2.3 RELEASE_REPO=brianrackle/codelima DIST_DIR=./tmp/dist FORMULA_OUTPUT=./tmp/dist/Formula/codelima.rb
make verify
```

## Releases

Local release packaging uses the same make targets as CI:

```sh
make package PACKAGE_VERSION=1.2.3 DIST_DIR=./tmp/dist
make package-formula \
  PACKAGE_VERSION=1.2.3 \
  RELEASE_TAG=v1.2.3 \
  RELEASE_REPO=brianrackle/codelima \
  DIST_DIR=./tmp/dist \
  FORMULA_OUTPUT=./tmp/dist/Formula/codelima.rb
```

`make package` builds a platform-native archive that contains:

- `bin/codelima` as a small launcher that points `CODELIMA_GHOSTTY_VT_LIB` at the packaged Ghostty library
- `bin/codelima-real` as the compiled Go binary
- `lib/libghostty-vt.{dylib,so}` as the runtime terminal backend
- `<asset>.json` as the manifest used to generate the Homebrew formula

The repository ships two GitHub Actions workflows:

- `.github/workflows/ci.yml` runs `make verify` on Ubuntu and macOS for pushes to `main` and pull requests
- `.github/workflows/release.yml` builds release archives for `darwin-amd64`, `darwin-arm64`, `linux-amd64`, and `linux-arm64`, creates or updates the GitHub release for the tag, uploads the archives and manifests, and then updates a Homebrew tap when the required repository settings are present

To enable automatic tap updates, configure:

- repository variable `HOMEBREW_TAP_REPO`, for example `brianrackle/homebrew-codelima`
- optional repository variable `HOMEBREW_TAP_BRANCH`, which defaults to `main`
- repository secret `HOMEBREW_TAP_TOKEN` with permission to push to the tap repository

Once those are in place, releasing a new Homebrew version is:

```sh
git tag v1.2.3
git push origin v1.2.3
```

The release workflow publishes the assets and updates `Formula/codelima.rb` in the tap. End users then upgrade with `brew update && brew upgrade codelima`.

## Documentation

Keep `README.md` focused on user-facing setup, capabilities, workflows, and command examples.
Internal documentation for design, maintenance, and tooling should live in `BUILD.md`.

## Smoke Test

The smoke test uses the real `limactl` binary and the repository fixture in `test-project-dir` to create and manage three VM layers inside one project:

```sh
make smoke
```

The script:

1. creates a root project bound to `test-project-dir`
2. creates and starts a root Lima-backed node
3. clones that node into a second node in the same project
4. clones the second node into a third node in the same project
5. prints the resulting project tree and node list

## Metadata Layout

By default the CLI stores metadata in `~/.codelima`:

```text
~/.codelima/
  _config/
  _locks/
  _index/
  projects/
  nodes/
```

Override the location with `--home` or `CODELIMA_HOME`.

## Notes

- `config show` displays the active defaults and resolved paths.
- Built-in `codex-cli` and `claude-code` profiles are smoke-friendly defaults. Replace the profile YAML files under `CODELIMA_HOME/_config/agent-profiles/` when you want real install commands.
