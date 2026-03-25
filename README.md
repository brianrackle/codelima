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

### Why Sandbox Agentic Coding In CodeLima

CodeLima is built on top of Lima, so each node is a real VM instead of a shell wrapper around your host filesystem. That means you can give a coding agent broad permissions inside the VM without taking the same risk on your host machine.

That isolation is useful when you want to avoid:

- package bloat from repeated `apt`, `snap`, `npm`, or language-runtime installs on the host
- version conflicts between repos that want different toolchains
- filesystem misuse such as writing outside the intended project directory
- host breakage from destructive shell mistakes, failed package upgrades, or broken login-shell state
- accumulating experimental dependencies on your macOS or Linux workstation

If you need lower-level control, you can always drop down to Lima directly. CodeLima does not hide the fact that nodes are Lima VMs:

```sh
limactl list
limactl shell <instance-name>
limactl copy <instance-name>:/path/in/vm ./local-path
limactl copy ./local-file <instance-name>:/path/in/vm
limactl stop <instance-name>
```

Use `codelima node show <node>` or `limactl list` to find the backing instance name.

### TUI Quick Start

The TUI opens when you run `codelima` with no command:

```sh
codelima
```

Basic layout:

```text
+---------------------------+---------------------------------------------+
| Projects / Nodes          | Details or terminal                         |
|                           |                                             |
| ▼ api                     | Project: api                                |
|   • api-copy    STOPPED   | Node: api-copy  Mode: copy                  |
|   • api-mount   RUNNING   |                                             |
| ▼ billing                 | Alt-` toggles tree focus <-> terminal focus |
|   • billing-a   RUNNING   |                                             |
+---------------------------+---------------------------------------------+
```

Fast key reference:

- `Alt-\``: toggle between tree focus and terminal focus
- `q`: quit the TUI
- `Up` / `Down`: move selection in the tree
- `Left` / `Right`: collapse or expand projects in the tree
- `[a]`: add project
- `[g]`: manage reusable environment configs
- on a selected project: `[n]` create node, `[e]` manage project environment, `[u]` update project, `[x]` delete project
- on a selected node: `[s]` start or stop node, `[d]` delete node, `[c]` clone node
- mouse: click tree entries to select, click links to open them, drag terminal text to copy, `Shift`-drag to force local copy, wheel-scroll local terminal scrollback when the guest is not capturing the mouse

Create Project dialog:

```text
+--------------------------- Create Project ----------------------------+
| Project Slug: api                                                 |
| Workspace Path: /Users/you/src/api                                |
| Environment Configs: codex                                        |
+--------------------------------------------------------------------+
```

Create Node dialog:

```text
+---------------------------- Create Node -----------------------------+
| Selected project: api                                             |
| Node Slug: api-copy                                               |
| Workspace Mode: copy: isolated guest workspace copy               |
+--------------------------------------------------------------------+
```

### Workflow 1: Manage Many Codebases From One Control Plane

Use one `CODELIMA_HOME` to track many host workspaces while giving each codebase its own projects and nodes.

CLI:

```sh
codelima project create --slug api --workspace /Users/you/src/api
codelima project create --slug billing --workspace /Users/you/src/billing
codelima project create --slug docs --workspace /Users/you/src/docs

codelima project list
codelima project tree
```

TUI view after registering several repos:

```text
+---------------------------+---------------------------------------------+
| Projects / Nodes          | Project: billing                            |
|                           |                                             |
| ▼ api                     | Workspace: /Users/you/src/billing           |
|   • api-copy    STOPPED   | [n] create node                             |
| ▼ billing                 | [e] environment                             |
|   • billing-a   RUNNING   | [u] update project                          |
| ▼ docs                    | [x] delete project                          |
+---------------------------+---------------------------------------------+
```

Why this helps for agentic coding:

- one place to switch between many repos quickly
- per-project default environments, resources, and agent profile metadata
- per-node terminals that stay attached while you move around the tree

### Workflow 2: Choose How VM Changes Sync Back To The Host

Node creation gives you two workspace modes:

- `copy`: safest for experimentation; the host repo is copied into the VM on first start and guest edits stay in the VM
- `mounted`: fastest feedback loop; the host repo is mounted writable and guest edits appear on the host immediately

CLI:

```sh
codelima node create --project api --slug api-copy --workspace-mode copy
codelima node create --project api --slug api-mounted --workspace-mode mounted

codelima node start api-copy
codelima node start api-mounted
codelima node list
```

ASCII comparison:

```text
copy mode                         mounted mode
---------                         ------------
host repo ----copy once----> VM   host repo <----live writable----> VM
safe isolation                    immediate sync to host
best for risky agents             best for tight edit/test loops
```

Practical rule:

- use `copy` when you want the strongest sandbox boundary around a coding agent
- use `mounted` when you intentionally want the VM to act directly on your host workspace

### Workflow 3: Run Codex Or Claude In A Sandboxed VM With Full In-Guest Permissions

Fresh homes include:

- environment config `codex`
- environment config `claude-code`
- agent profiles `codex-cli` and `claude-code`

The environment config installs the tool in the VM. The agent profile records which command that project or node expects to run. CodeLima itself does not add extra in-guest restrictions, so if you want a full-permission, no-restriction Codex or Claude session, start it inside the VM shell rather than on your host.

Codex example:

```sh
codelima project create \
  --slug payments \
  --workspace /Users/you/src/payments \
  --env-config codex \
  --agent-profile codex-cli

codelima node create --project payments --slug payments-codex --workspace-mode copy
codelima node start payments-codex
codelima shell payments-codex
```

Then, inside the VM shell:

```sh
codex
```

Claude example:

```sh
codelima project create \
  --slug frontend \
  --workspace /Users/you/src/frontend \
  --env-config claude-code \
  --agent-profile claude-code

codelima node create --project frontend --slug frontend-claude --workspace-mode copy
codelima node start frontend-claude
codelima shell frontend-claude
```

Then, inside the VM shell:

```sh
claude
```

TUI view after creating the project and node:

```text
+---------------------------+---------------------------------------------+
| Projects / Nodes          | Project: payments                           |
| ▼ payments                | Node: payments-codex  Mode: copy            |
|   • payments-codex RUNNING| Environment configs: codex                  |
|                           | Open terminal, then run `codex` in the VM   |
+---------------------------+---------------------------------------------+
```

Why this is safer:

- unrestricted agent actions happen inside the VM
- package installs and shell state stay off the host
- if the agent breaks the VM, you can stop, delete, and recreate the node
- `copy` mode keeps accidental file damage out of the host workspace

### Workflow 4: Build Custom Environments For Any Agent Or Linux Package Set

Reusable environment configs are just ordered command lists that run when a new node is first bootstrapped. Use them to install editors, CLIs, package managers, or custom agent wrappers.

CLI:

```sh
codelima environment create \
  --slug devbox \
  --env-command "sudo apt-get update" \
  --env-command "sudo apt-get install -y ripgrep fd-find jq gh" \
  --env-command "curl -fsSL https://mise.run | sh"

codelima environment update devbox --env-command "sudo npm install -g @anthropic-ai/claude-code"
codelima environment show devbox

codelima project create \
  --slug tooling \
  --workspace /Users/you/src/tooling \
  --env-config devbox
```

TUI flow:

```text
[g] Env Configs
  -> Create Config
  -> Add Command
  -> Move Command
  -> Remove Command

[a] Add Project
  -> choose Environment Configs: devbox
```

Good uses for custom environments:

- installing Linux packages for a repo-specific toolchain
- installing your preferred coding agent if it is not one of the built-ins
- installing helper tools such as `gh`, `just`, `direnv`, `uv`, `pnpm`, or `docker` clients
- encoding repeatable setup once instead of repeating it in every VM by hand

### CLI Commands At A Glance

Health and config:

```sh
codelima doctor
codelima config show
```

Reusable environments:

```sh
codelima environment create --slug NAME --env-command '...'
codelima environment list
codelima environment show NAME
codelima environment update NAME --env-command '...'
codelima environment delete NAME
```

Projects:

```sh
codelima project create --slug NAME --workspace /path/to/repo
codelima project list
codelima project show NAME
codelima project update NAME --workspace /new/path
codelima project delete NAME
codelima project tree
codelima project fork SOURCE --slug CHILD --workspace /path/to/child
```

Nodes:

```sh
codelima node create --project PROJECT --slug NODE [--workspace-mode copy|mounted]
codelima node list
codelima node show NODE
codelima node start NODE
codelima node stop NODE
codelima node clone NODE --node-slug NEW-NODE
codelima node delete NODE
codelima node status NODE
codelima node logs NODE
codelima node cleanup-incomplete
codelima node cleanup-incomplete --apply
```

Shell entry:

```sh
codelima shell NODE
codelima shell NODE -- uname -a
```

### Lima Fallback Examples

Because CodeLima uses Lima instead of inventing a separate VM backend, you can always fall back to `limactl` if you need something CodeLima does not expose yet:

```sh
limactl list
limactl shell <instance-name>
limactl copy <instance-name>:/var/log/cloud-init-output.log ./cloud-init-output.log
limactl copy ./local-config <instance-name>:/tmp/local-config
limactl stop <instance-name>
limactl delete -f <instance-name>
```

That makes CodeLima a higher-level workflow layer on top of Lima rather than a dead-end abstraction.

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
- Built-in `codex-cli` and `claude-code` agent profiles define the default launch command names. Pair them with the matching `codex` or `claude-code` environment config so the executable is actually installed in the VM.
