# codelima

**A Lima VM manager for your terminal.**

> [!WARNING]
> Version 1.0.0 changes CodeLima from primarily a terminal emulator for Lima VMs to a **Lima VM manager**.

CodeLima manages Linux development VMs using [Lima](https://lima-vm.io/).
Each VM, called a **node**, is tied to a project directory.

By default, the project directory is mounted read/write at the same absolute
path on the host and guest. Use copy mode for a workspace stored in the VM,
or separate Git worktrees for agents that should edit independently.

## Features

- View all nodes or filter by project. Sort, group, and monitor resource usage.
- Create and clone nodes; start, stop, restart, or delete them individually
  or in batches. Review destructive actions before they run.
- Inspect node metrics, logs, configuration, and shell sessions.
- Reuse VM templates and setup commands, including built-in Codex and Claude
  Code environments.
- Forward HTTP, WebSocket, and TCP services to the host.
- Track operations, inspect failures, and cancel work from Activity. Check
  host capacity and manage the daemon from System.
- Attach to a shell directly, open a terminal window or tmux, or use the
  embedded shell. Sessions survive closing the TUI.
- CLI commands and JSON output for automation.

## Install

Homebrew installs CodeLima, Lima, and Git:

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

Filters do not clear marked nodes. Batch reviews list the affected nodes;
submitted operations continue after the TUI closes. Delete requires the exact
node name or batch count. If one node fails, the rest of the batch continues.

The TUI supports terminals down to 80×24. Wider windows include a preview of
the selected node. You can remap keys in Settings; help and the command palette
show your configured bindings.

### Inspect a node

Node detail has Overview, Metrics, Logs, and Spec tabs, plus Shell when the
embedded mode is enabled. Use `Tab` / `Shift+Tab` to switch detail tabs.

Overview groups Identity, Resources, Sessions, Ports, and Recent events.
`Ctrl+Up` / `Ctrl+Down` changes panel focus; arrow keys select or scroll within
that panel. Identity shows guest architecture, KVM access, and credential
import details when available.

Metrics shows CPU, memory, disk, history, and a process view. Logs collects
node events, bootstrap output, and Lima logs. Spec shows the resolved Lima
configuration, the values saved when the node was created, and differences
from its current template.

In Sessions, `Enter` attaches to the selected session, `t` creates a guest
shell, `T` creates a host shell, and `K` asks for confirmation to end a session.

### Templates and environments

Press `2` for Templates. Use `n` to create, `e` to edit, or `c` to clone a
template. Set CPU, memory, disk, image, environments, and
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

Environments group commands that install software and check the installation.
Press `g` to edit them, open your external editor, or test them in a temporary
node. Dependency hints are suggestions. Template edits apply to new nodes;
existing nodes keep the configuration saved at creation.

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

The default `attach` mode connects your terminal directly to a shell session.
`x` reuses an available session; `X` offers guest and host shells,
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
leaves sessions running; stopping a node ends guest processes.

To use CodeLima as a terminal emulator, enable the embedded shell:

```sh
codelima settings set shell.mode embedded
codelima .
```

Select a running node and press `x` to open its Shell tab. Press `F6` to return
to manager focus before using `t` for a new guest tab, `T` for a host tab,
`[` and `]` to switch tabs, `w` to close a tab, or `F7` to search history.
`Alt+Shift+Left` and `Alt+Shift+Right` move the active tab without wrapping.
On macOS, Alt is Option. These commands also appear in `?` help and support
custom bindings through `settings keys`. While the terminal has focus, every
key except the configured focus shortcut reaches the shell.

Host shells also work for stopped nodes. Open node detail, select Shell with
`Tab`, and press `T`, then `F6` to type into the host shell.

### Ports and services

HTTP and WebSocket listeners are discovered automatically. Reach a service
running on port 8080 in `api-redesign` from the host:

```sh
curl http://api-redesign.localhost:8080
```

Each node has its own `{node}.localhost` hostname, so nodes can serve the same
port. Bare `localhost:PORT` and loopback addresses route to the first node
serving that port unless pinned. Codex's login callback on port 1455 follows
the newest listener.

Press `3` for Ports. Filter with `/`, open an HTTP route with `o`, copy an
address with `y`, or press `Enter` to inspect its node. `p` pins a shared HTTP
port to the selected node. The view also reports stopped, retrying, and
conflicting routes.

For TCP services, map a host port to a guest port:

```sh
codelima node create --slug database --port 15432:5432
codelima node start database
```

The daemon opens published mappings after bootstrap and validation succeed.
Keep it running while using forwarded ports. A published TCP mapping reserves
its host port; daemon restart or update ends existing TCP connections.

### Activity, system, and settings

Press `4` to follow or cancel operations and inspect failures. Press `5` to
check host capacity, run doctor checks, manage the daemon, and change settings.

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

If terminals freeze, capture diagnostics before restarting:

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

The optional native ABI check, `make test-ghostty-vt-schema`, also needs host
Python 3 with `venv` support. Make installs its pinned jsonschema dependency
in a project-local virtual environment.

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
