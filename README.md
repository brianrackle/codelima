# CodeLima

> Sandbox agentic coding in real Lima VMs instead of on your host machine.

CodeLima is a Go CLI and shell-first TUI for managing lineage-aware projects, Lima-backed nodes, and reusable environment configs from one control plane. It helps you run coding agents, dev environments, and repo-specific toolchains inside real Linux VMs while keeping your macOS or Linux workstation cleaner, safer, and easier to reason about.

## The Problem

Modern coding workflows are powerful, but the default setup is messy:

- running agents with broad permissions directly on the host
- accumulating `apt`, `npm`, language runtimes, and shell-state drift across repos
- conflicting toolchains between projects
- accidental writes outside the repo you meant to touch
- slow context switching when you juggle many repos, many environments, and many terminal sessions

## How CodeLima Solves It

CodeLima is a thin workflow layer on top of Lima, not a replacement for it. You register host workspaces as projects, create Lima-backed nodes for those projects, choose whether each node gets an isolated guest copy or a live writable mount of the repo, and bootstrap nodes with reusable environment configs.

That gives you:

- **real VM isolation** for risky agent sessions
- **two workspace modes**: `copy` for safer experimentation and `mounted` for immediate host sync
- **shared environment configs** for Codex, Claude Code, and custom Linux toolchains
- **global, project, and node Lima command overrides** when a repo or VM needs non-default `limactl` flags
- **one control plane** for many repos and many nodes
- **a shell-first TUI** that keeps reusable project-local and node terminal sessions available while the TUI is running
- **direct Lima escape hatches** whenever you want to use `limactl` yourself

## What That Feels Like In Practice

- Give `codex` or `claude` full in-guest permissions without giving the same reach to your host.
- Keep package installs, shell state, and experiments off your workstation.
- Move between projects and nodes quickly from the CLI or the TUI.

## See The Value In 60 Seconds

Once `codelima` is on your `PATH`, you can register a repo, create a sandboxed node, and drop into the VM:

```sh
codelima project create \
  --slug payments \
  --workspace /Users/you/src/payments \
  --env-config codex \
  --agent-profile codex-cli

codelima node create --project payments --slug payments-sandbox --workspace-mode copy
codelima node start payments-sandbox
codelima shell payments-sandbox
```

Then, inside the VM shell:

```sh
codex
```

Use `copy` when you want the strongest sandbox boundary around agent actions. Use `mounted` when you intentionally want the VM to act directly on your host workspace with immediate sync.

## Install

Install the latest packaged release from Homebrew:

```sh
brew tap brianrackle/codelima
brew install codelima
```

The Homebrew formula installs the packaged `codelima` binary plus the bundled `libghostty-vt` runtime library, and declares `git` and `lima` as runtime dependencies.

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

`make init` installs the Go toolchain, `golangci-lint`, Zig, and a patched `libghostty-vt` build locally under `.tooling/<os>-<arch>`; system Go or Zig installs are not required. It also refreshes `.tooling/ghostty-vt/current` as a compatibility link for the cgo bridge. The per-platform layout avoids host and guest toolchain collisions when the same repository is used from both macOS and a Linux VM.

## Build From Source

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

## Quick Start

The examples below assume `codelima` is installed and available on `PATH`.
For repository-local development, use `make run ARGS="..."` or `./bin/codelima ...`.

Create a project from a host repo:

```sh
codelima project create --slug api --workspace /Users/you/src/api
```

Create a Lima-backed node for that project:

```sh
codelima node create --project api --slug api-copy --workspace-mode copy
```

Start the node and open a shell inside it:

```sh
codelima node start api-copy
codelima shell api-copy
```

Open the TUI instead of running a subcommand:

```sh
codelima
```

Use `copy` when you want an isolated guest workspace. Use `mounted` when you want guest edits to appear on the host immediately.

## Core Concepts

CodeLima manages:

- projects and immutable workspace snapshots under `CODELIMA_HOME`
- Lima-backed nodes delegated through `limactl`
- reusable environment configs that bootstrap new nodes
- a canonical shell surface that passes through to `limactl shell`
- a shell-first TUI that keeps reusable project-local and node terminal sessions while the TUI process is running

### Capabilities

- register host workspaces as lineage-aware projects
- capture immutable snapshots on demand for lineage-aware project workflows
- create, start, stop, clone, inspect, and delete Lima-backed nodes, choosing either an isolated copied workspace or a writable mounted workspace per node
- detect and clean up incomplete node metadata directories left by failed node creation attempts
- create reusable environment configs and assign them to multiple projects as shared bootstrap defaults, including built-in `codex` and `claude-code` installers
- open an interactive shell or run one-off commands inside a node, starting in a guest-local copy of the project workspace that keeps the same absolute path
- browse the project tree, manage selected projects and nodes, and jump between preserved project-local and node sessions in a Ghostty-backed embedded terminal by running `codelima` with no command
- keep navigating the tree or focus another preserved project or node terminal while long-running project or node mutations continue in the background
- inspect local control-plane health with `doctor` and resolved defaults with `config show`
- view project lineage with attached project nodes via `project tree`

## Command Structure

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

## TUI Quick Start

The TUI opens when you run `codelima` with no command:

```sh
codelima
```

Basic layout:

```text
+---------------------------+---------------------------------------------+
| Projects / Nodes          | [Info] Terminal                             |
|                           |                                             |
| ▼ api                     | Project controls                            |
|   • api-copy    STOPPED   | Slug: api                                   |
|   • api-mount   RUNNING   | Workspace: /Users/you/src/api               |
| ▼ billing                 | Press i for terminal preview                |
|   • billing-a   RUNNING   |                                             |
+---------------------------+---------------------------------------------+
```

Fast key reference:

- `Alt-\`` or `F6`: toggle between tree focus and terminal focus
- `i`: toggle the right pane between info and terminal while the tree is focused
- macOS Terminal.app note: `Option` does not act as `Alt`/Meta by default, so use `F6` there or enable Profile > Keyboard > Use Option as Meta key if you want `Alt-\`` to work
- `q`: quit the TUI
- `Up` / `Down`: move selection in the tree
- `Left` / `Right`: collapse or expand projects in the tree
- `[a]`: add project
- `[g]`: manage reusable environment configs
- on a selected project: `[n]` create node, `[u]` update project, `[x]` delete project
- on a selected node: `[s]` start or stop node, `[d]` delete node, `[c]` clone node
- mouse: click tree entries to select, click links to open them, and wheel-scroll local terminal scrollback when the guest is not capturing the mouse; use your terminal emulator's host-selection bypass gesture for terminal text selection/copy while mouse-aware apps keep receiving guest mouse input

In tree focus, selecting a project or node shows its info pane by default. Press `i` to switch the split pane to that project's host-local shell or the selected node's guest terminal preview without changing fullscreen terminal focus behavior; stopped nodes still show a terminal-oriented placeholder until you start them.

Project and node forms, menus, and selectors replace the right pane instead of opening centered modals, so the tree stays visible while you work through them. Long-running project and node mutations run in the background, render transient task state in the tree and details pane, and leave the rest of the TUI usable while they finish.

Create Project form in the right pane:

```text
+--------------------------- Create Project ----------------------------+
| Project Slug: api                                                 |
| Workspace Path: /Users/you/src/api                                |
| Environment Configs: codex                                        |
+--------------------------------------------------------------------+
```

Create Node form in the right pane:

```text
+---------------------------- Create Node -----------------------------+
| Selected project: api                                             |
| Node Slug: api-copy                                               |
| Workspace Mode: copy: isolated guest workspace copy               |
+--------------------------------------------------------------------+
```

## Workflow 1: Manage Many Codebases From One Control Plane

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
| Projects / Nodes          | [Info] Terminal                             |
|                           |                                             |
| ▼ api                     | Project controls                            |
|   • api-copy    STOPPED   | Slug: api                                   |
| ▼ billing                 | Workspace: /Users/you/src/billing           |
|   • billing-a   RUNNING   |                                             |
| ▼ docs                    | Press i for terminal preview                |
|                           |                                             |
+---------------------------+---------------------------------------------+
```

Why this helps for agentic coding:

- one place to switch between many repos quickly
- per-project default environments and agent profile metadata
- project and node terminals that stay attached while you move around the tree

Global Lima command defaults live in `CODELIMA_HOME/_config/config.yaml`.
Project-specific overrides live in the project metadata file shown in the TUI project info pane under `CODELIMA_HOME/projects/<project-id>/project.yaml`.
Node-specific overrides live in `CODELIMA_HOME/nodes/<node-id>/node.yaml`, and the TUI node info pane links that file directly.
When a project or node has no overrides yet, its metadata file includes a commented example `lima_commands` block you can uncomment and edit.

Each `lima_commands` action is an ordered command list. CodeLima executes the list in order, and higher-precedence overrides replace the whole list for that action. `lima_commands.bootstrap` runs during the first successful node start and replaces the older project-level `environment_commands` field.

Global default example:

```yaml
lima_commands:
  create:
    - "{{binary}} create -y --name {{instance_name}} --cpus=2 --memory=4 --disk=20 {{template_path}}"
  start:
    - "{{binary}} start -y {{instance_name}}"
  bootstrap: []
  workspace_seed_prepare:
    - "sudo rm -rf {{target_path}} && sudo mkdir -p {{target_parent}} && sudo chown \"$(id -un)\":\"$(id -gn)\" {{target_parent}}"
  clone:
    - "{{binary}} clone -y {{source_instance}} {{target_instance}}"
```

Project override example:

```yaml
lima_commands:
  bootstrap:
    - "./script/setup"
    - "direnv allow"
  workspace_seed_prepare:
    - "install -d {{target_parent}} && rm -rf {{target_path}}"
  copy:
    - "{{binary}} copy --backend=rsync{{recursive_flag}} {{source_path}} {{copy_target}}"
  create:
    - "{{binary}} create -y --name {{instance_name}} --cpus=6 --memory=12 --disk=80 --vm-type=vz {{template_path}}"
  start:
    - "{{binary}} start {{instance_name}} --vm-type=vz"
  clone:
    - "{{binary}} clone -y {{source_instance}} {{target_instance}}"
```

Node override example:

```yaml
lima_commands:
  start:
    - "{{binary}} start {{instance_name}} --tty=false"
  copy:
    - "{{binary}} copy --backend=rsync{{recursive_flag}} {{source_path}} {{copy_target}} --checksum"
```

Global, project, and node overrides follow normal precedence in that order. For the very first `node create` or `node clone`, use `--lima-commands-file` or the TUI `Lima Commands File` field when you need node-specific `create`, `clone`, or `template_copy` overrides to apply before the node metadata file already exists on disk.

Available placeholders depend on the command and include:

- `{{binary}}`
- `{{locator}}`
- `{{instance_name}}`
- `{{template_path}}`
- `{{source_instance}}`
- `{{target_instance}}`
- `{{source_path}}`
- `{{target_path}}`
- `{{target_parent}}`
- `{{copy_target}}`
- `{{recursive_flag}}`
- `{{workdir_flag}}`
- `{{command_args}}`

## Workflow 2: Choose How VM Changes Sync Back To The Host

Node creation gives you two workspace modes:

- `copy`: safest for experimentation; the host repo is copied into the VM on first start and guest edits stay in the VM
- `mounted`: fastest feedback loop; the host repo is mounted writable and guest edits appear on the host immediately

When a project needs non-default copy-mode seeding, override `lima_commands.workspace_seed_prepare` for the guest-side directory prep and `lima_commands.copy` for the host-to-guest transfer itself. The TUI create-node and clone-node dialogs also accept an optional `Lima Commands File` path for first-create per-node overrides.

CLI:

```sh
codelima node create --project api --slug api-copy --workspace-mode copy
codelima node create --project api --slug api-mounted --workspace-mode mounted
codelima node create --project api --slug api-tuned --lima-commands-file ./tmp/api-node-lima.yaml

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

## Workflow 3: Run Codex Or Claude In A Sandboxed VM With Full In-Guest Permissions

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

## Workflow 4: Build Custom Environments For Any Agent Or Linux Package Set

Reusable environment configs are just ordered command lists that run when a new node is first bootstrapped. Use them to install editors, CLIs, package managers, or custom agent wrappers.

CLI:

```sh
codelima environment create \
  --slug devbox \
  --bootstrap-command "sudo apt-get update" \
  --bootstrap-command "sudo apt-get install -y ripgrep fd-find jq gh" \
  --bootstrap-command "curl -fsSL https://mise.run | sh"

codelima environment update devbox --bootstrap-command "sudo npm install -g @anthropic-ai/claude-code"
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
  -> Add Bootstrap Command
  -> Move Bootstrap Command
  -> Remove Bootstrap Command

[a] Add Project
  -> choose Environment Configs: devbox
```

Good uses for custom environments:

- installing Linux packages for a repo-specific toolchain
- installing your preferred coding agent if it is not one of the built-ins
- installing helper tools such as `gh`, `just`, `direnv`, `uv`, `pnpm`, or `docker` clients
- encoding repeatable setup once instead of repeating it in every VM by hand

## Lima Fallback Examples

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

## CLI Commands At A Glance

Health and config:

```sh
codelima doctor
codelima config show
```

Reusable environments:

```sh
codelima environment create --slug NAME --bootstrap-command '...'
codelima environment list
codelima environment show NAME
codelima environment update NAME --bootstrap-command '...'
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

Global Lima command defaults are stored in `CODELIMA_HOME/_config/config.yaml` under `lima_commands`.
Project-specific overrides live in each project's `project.yaml` under the same key.
Node-specific overrides live in each node's `node.yaml` under the same key.
Use `config show` to inspect the effective global defaults, `project show` to inspect project-specific overrides, `node show` to inspect node-specific overrides, and the TUI details pane to find the on-disk metadata files quickly.

Nodes:

```sh
codelima node create --project PROJECT --slug NODE [--workspace-mode copy|mounted] [--lima-commands-file PATH]
codelima node list
codelima node show NODE
codelima node start NODE
codelima node stop NODE
codelima node clone NODE --node-slug NEW-NODE [--lima-commands-file PATH]
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

## Make Shortcuts

```sh
make run ARGS="doctor"
make run ARGS="config show"
make run ARGS="environment create --slug shared-dev --bootstrap-command ./script/setup"
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
