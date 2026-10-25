# codelima

**A terminal-based Lima VM manager for development and coding agents.**

> [!WARNING]
> Version 1.0.0 changes CodeLima from primarily a terminal emulator for Lima VMs to a **Lima VM manager**.

> **Disclaimer:** This project is 100% vibe-coded. I have never read a single
> line of the code. However, I do 100% of my work within the codelima TUI and
> shells, so it is actively tested for real-world use. It still has some issues
> with daemon hangs and intermittent performance issues.

CodeLima creates and manages Linux development machines powered by
[Lima](https://lima-vm.io/). See your fleet, allocate resources, manage reusable
templates, inspect running services, and follow operations from one TUI or CLI.
Each directory-bound VM is called a **node**.

Use nodes for independent projects, parallel experiments, or several coding
agents working with their own operating systems and processes. Workspaces are
mounted read/write at the same absolute path on the host and guest by default;
use copy mode for a guest-local workspace. A mounted workspace is shared, so
use separate Git worktrees when agents should edit independently.

## Features

- Global and directory-scoped fleet views with filtering, sorting, grouping,
  live resource usage, and batch lifecycle actions.
- Create, start, stop, restart, clone, and delete nodes, with explicit review
  for destructive actions.
- Node details for identity, resources, sessions, metrics, logs, and resolved
  configuration.
- Reusable VM templates and ordered environment bootstrap bundles, including
  built-in Codex and Claude Code environments.
- Dynamic HTTP/WebSocket forwarding, node-qualified localhost addresses, and
  explicit TCP port mappings.
- Activity history with operation progress, failures, and cancellation;
  System tools for host capacity, doctor checks, daemon actions, and settings.
- Direct shell attachment, external terminal and tmux launch options, and an
  optional embedded shell. Daemon-owned sessions survive closing the TUI.
- CLI commands and JSON output for automation.

## Install

Homebrew installs CodeLima, Lima, Git, and the private renderer worker:

```sh
brew install brianrackle/codelima/codelima
```

Use the fully qualified formula name so Homebrew adds the tap and trusts the
formula. If a separate `brew tap` reports an untrusted formula or invalid tap
syntax, use the install command above. To add the tap separately:

```sh
brew trust --formula brianrackle/codelima/codelima
brew tap brianrackle/codelima
brew install brianrackle/codelima/codelima
```

Upgrade with:

```sh
brew update
brew upgrade codelima
codelima daemon update
codelima --version
```

Reopen the TUI after upgrading. Native archives for macOS arm64, Linux amd64,
and Linux arm64 are also available from
[GitHub Releases](https://github.com/brianrackle/codelima/releases).
Keep `codelima` and `codelima-renderer-worker` together when installing an
archive. No separate Ghostty library is required.

Requirements:

- macOS arm64, Linux amd64, or Linux arm64.
- Lima 2.1.0 or a compatible newer Lima 2.x release, with `limactl` on `PATH`.
- Apple Virtualization.framework on macOS, or QEMU with KVM access on Linux.
- Git.

## Quick start

```sh
codelima doctor --repair
cd ~/src/my-project
codelima .
```

Press `n` to create a node. Choose a name, directory, template, workspace mode,
and whether to import host credentials. **Start after create** is enabled by
default; shell attachment is optional. Press `Ctrl+s` to submit. The first start
can take longer while Lima downloads the image and installs the environments.

Select a node and press `Enter` to inspect it, or `x` to open its shell.
`Ctrl+]` detaches back to the manager while leaving the session running;
`exit` ends the shell.

Run `codelima` without a path to see all nodes. `codelima .` shows nodes bound
to the current directory and its descendants. Press `0` to toggle that scope.
Browsing or refreshing the fleet does not open a shell.

## Manager guide

### Navigation

| Key | Action |
| --- | --- |
| `1`–`5` | Nodes, Templates, Ports, Activity, System |
| `0` | Toggle directory scope |
| Arrow keys or `j` / `k` | Select a row |
| `Enter` / `Esc` | Open node detail / return to the fleet |
| `/` | Filter by text or `status:`, `template:`, `dir:`, `port:` |
| `G`, `<` / `>` / `=` | Group, choose sort column, reverse sort |
| `Space` / `*` | Mark one node / all visible nodes |
| `n` | Create a node |
| `s` | Start or stop |
| `r` | Restart; retry a failed step on an unmarked failed node |
| `c` / `d` | Clone / delete |
| `o` | Open the node's first live HTTP route |
| `x` / `X` | Open a shell / choose a shell launch option |
| `a` / `g` / `m` | Configurations / environments / activity |
| `:` / `?` | Command palette / contextual key help |
| `q` | Quit from a global view, or return from detail |

Marked nodes remain marked when hidden by a filter. Batch reviews show the
captured targets and impact; accepted work continues in the daemon after the
TUI closes. Delete requires the exact node name or batch count. A failed batch
member does not prevent the remaining members from running.

The fleet adapts to compact terminals down to 80×24. Wider windows include a
selected-node preview. Settings can remap contextual keys; help and the command
palette show the configured bindings.

### Inspect a node

Node detail has Overview, Metrics, Logs, and Spec tabs, plus Shell when the
embedded mode is enabled. Use `Tab` / `Shift+Tab` to switch detail tabs.

Overview groups Identity, Resources, Sessions, Ports, and Recent events.
`Ctrl+Up` / `Ctrl+Down` changes panel focus; arrow keys select or scroll within
that panel. Identity includes observed guest architecture, KVM access, and
recorded credential import details when available.

Metrics shows CPU, memory, disk, history, and a process view. Logs collects
node events, bootstrap output, and Lima logs. Spec shows the resolved Lima
configuration, the values frozen when the node was created, and differences
from its current template.

In Sessions, `Enter` attaches to the selected session, `t` creates a guest
shell, `T` creates a host shell, and `K` reviews ending a session.

### Templates and environments

Press `2` for Templates. Use `n` to create, `e` to edit, or `c` to clone a
template. Configure CPU, memory, disk, image, ordered environments, and
bootstrap commands. `Shift+Up` / `Shift+Down` reorders environments, `F2`
previews the resolved configuration, and `Ctrl+s` saves.

The built-in sizes are:

| Template | vCPUs | Memory | Disk |
| --- | ---: | ---: | ---: |
| `xsmall` | 1 | 1 GiB | 10 GiB |
| `small` (default) | 2 | 4 GiB | 25 GiB |
| `medium` | 4 | 8 GiB | 50 GiB |
| `large` | 6 | 16 GiB | 75 GiB |
| `xlarge` | 8 | 32 GiB | 100 GiB |

Environments are reusable bootstrap and validation bundles. Open them with
`g` to edit commands, use your external editor, or test a bundle in a disposable
node. Dependency hints are advisory. Template edits affect future nodes;
existing nodes retain their frozen configuration.

The CLI calls templates **configurations**:

```sh
codelima configuration create \
  --slug web-agent \
  --image template:ubuntu \
  --agent-profile codex-cli \
  --environment codex \
  --bootstrap-command 'apt-get update && apt-get install -y ripgrep' \
  --vcpus 4 --memory 8GiB --disk 40GiB
codelima node create --slug api-redesign --configuration web-agent --directory .
codelima node start api-redesign
```

Built-in Codex and Claude Code environments use their official native
installers as the guest login user. Run agents from the guest shell without
`sudo`. Optional host credential import happens once on first start and copies
available SSH, Git, Codex, and Claude Code authentication into the guest.
Disable it in the create form or with `--no-import-auth`. Clones inherit their
source disk, including credentials already on it.

### Shells

The default `attach` mode connects your terminal directly to a daemon-owned
session. `x` reuses an available session; `X` offers guest and host shells,
external windows, tmux, and existing sessions.

```sh
codelima shell api-redesign
codelima terminal list
codelima terminal attach TERMINAL_ID
```

Choose a preferred launch mode and configure an external window command:

```sh
codelima settings set shell.mode tmux
codelima settings set shell.window 'ghostty -e {cmd}'
```

Supported modes are `attach`, `window`, `tmux`, and `embedded`. In embedded
mode, `F6` switches focus between the manager and terminal. Closing the TUI
does not end its daemon-owned sessions; stopping a node ends guest processes.

### Ports and services

HTTP and WebSocket listeners are discovered automatically. Reach a service
running on port 8080 in `api-redesign` from the host:

```sh
curl http://api-redesign.localhost:8080
```

Node-qualified addresses keep services separate when nodes share a port.
Bare `localhost:PORT` and loopback addresses use the first active claimant
unless pinned. Codex's login callback on port 1455 follows the newest listener.

Press `3` for Ports. Filter with `/`, open an HTTP route with `o`, copy an
address with `y`, or press `Enter` to inspect its node. `p` pins a shared HTTP
port to the selected node. The view also reports stopped, retrying, and
conflicting routes.

For raw TCP services, publish a port explicitly:

```sh
codelima node create --slug database --port 15432:5432
codelima node start database
```

The daemon opens published mappings after bootstrap and validation succeed.
Keep it running while using forwarded ports. A published TCP mapping reserves
its host port; daemon restart or update ends existing TCP connections.

### Activity, system, and settings

Press `4` for operation history, progress, failures, and cancellation. Press
`5` for host capacity, doctor findings, daemon controls, and settings.

```sh
codelima settings set color ansi
codelima settings keys
codelima settings set keys.node.delete '[ctrl+d, alt+d]'
codelima settings reset keys node.delete
```

Color modes are `auto`, `ansi`, and `truecolor`. In System, `u` updates the
daemon, `S` restarts it, `L` opens its log, and `D` captures diagnostics.
Restarting the daemon ends its terminal processes.

## Common CLI workflows

```sh
codelima node list
codelima node show api-redesign
codelima node logs api-redesign
codelima node restart api-redesign
codelima node stop api-redesign
codelima node delete api-redesign
```

Clone a node and start the clone:

```sh
codelima node clone api-redesign --slug api-redesign-2 --start
```

Cloning copies the disk and workspace settings. A running source stops for the
copy and then restarts; its guest shell processes end while host shells remain.

Use a guest-local copy of the project instead of a shared host mount:

```sh
codelima node create --slug experiment --workspace-mode copy
```

Global flags precede the command or path:

```sh
codelima --json node list
codelima --home ~/.codelima-work .
```

Run `codelima --help` or `codelima <group> <command> --help` for the full CLI.

## Troubleshooting

Run `codelima doctor --repair` for setup checks and supported repairs. On Linux,
ensure the process running the daemon can access `/dev/kvm` and find QEMU.

If terminals freeze, capture evidence while the failure is occurring:

```sh
codelima daemon diagnose
codelima daemon diagnose --terminal-id TERMINAL_ID --output ./tmp/freeze-evidence
```

The read-only capture does not restart the daemon. Review bundles for sensitive
paths, commands, and logs before sharing them. All views using the same
CodeLima home share one daemon; the default home is `~/.codelima`.

## Development

From a source checkout:

```sh
make init
make build
./bin/codelima doctor
./bin/codelima .
```

`make init` installs pinned Go, Zig, gopls, golangci-lint, pkgconf, and the static
Ghostty dependency under `.tooling/<os>-<arch>`. The first setup needs network
access. `make build` produces platform-specific binaries under `bin/` and a
`bin/codelima` compatibility link.

Install the Lima runtime separately if `limactl` is missing:

```sh
make install-lima
export PATH="$HOME/.local/bin:$PATH"
./bin/codelima doctor --repair
```

This checksum-verified installer places pinned Lima 2.1.0 under `~/.local` and
preserves an existing installation. Linux also needs QEMU and KVM access.
For nested Debian/Ubuntu arm64 development, project-local QEMU setup and the
process-local KVM helper are documented in [BUILD.md](BUILD.md#local-build).

Run the development checks and tools through Make:

```sh
make verify                 # formatting, lint, unit tests, Vaxis tests, build
make test-race
make test-integration
make fmt
make go-doc GO_DOC_ARGS='context.WithCancelCause'
make gopls GOPLS_ARGS='check internal/codelima/environment_test_run.go'
```

See [BUILD.md](BUILD.md) for packaging, release, and focused test targets,
[QA.md](QA.md) for manual verification, and [TODO.md](TODO.md) for outstanding
work.
