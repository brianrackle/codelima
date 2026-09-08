# codelima

**Give coding agents a machine of their own. Then run as many as you can use.**

> **Disclaimer:** This project is 100% vibe-coded. I have never read a single
> line of the code. However, I do 100% of my work within the codelima TUI and
> shells, so it is actively tested for real-world use. It still has some issues
> with daemon hangs and intermittent performance issues.

Coding agents are at their best when they can install packages, run services,
start containers, and change a project without stopping every few minutes to
ask for permission. Giving an agent that freedom directly on your laptop is
uncomfortable. Taking the freedom away wastes the reason to use an agent in the
first place.

codelima resolves that tension with full Linux sandboxes powered by
[Lima](https://lima-vm.io/). Inside a sandbox, an agent can run with broad
permissions and do real engineering work. Outside it, your host remains your
host.

Your code does not disappear into the VM. By default, the project is mounted
read/write at the exact same absolute path, so edits appear immediately in your
host editor, Git client, and filesystem. The operating system boundary is
isolated; the working tree is deliberately shared.

And codelima is built for more than one agent:

- Run several agent sessions in one sandbox, or spread them across many.
- Run several sandboxes against one project.
- Run different experiments, branches, and services side by side.
- Work across many projects without turning your terminal into a maze.
- Stop sandboxes you are not using and bring them back quickly.
- Close the TUI without losing live terminal sessions.

The point is not to manage virtual machines. The point is to unleash coding
agents and keep control of all the work they can do.

codelima is self-hosted: all codelima development happens inside codelima.
Nested virtualization makes the loop complete—you can run codelima inside
codelima. Codex and Claude Code are supported out of the box, so agents can get
to work without hand-building their environments first.

## See the whole workshop

codelima wraps sandboxes, agent sessions, host shells, services, and experiments
in one keyboard-driven TUI.

```sh
cd ~/src/my-project
codelima .
```

`codelima .` opens a project-local view: only sandboxes attached to the current
directory or its descendants appear. Run `codelima` with no path to see every
sandbox across every project.

Each node in the left pane uses a compact property block: the node name is
followed by indented `Config`, `CWD`, live `Status`, `CPU`, `Memory`, and `Disk`
lines. Running-node usage is sampled once per second. CPU is normalized to
`0..100%` across the node's vCPUs; memory and guest root-disk usage are shown as
used/total binary units. Scoped views show the working directory relative to
the path used to open the TUI.

Selecting a running node shows its terminal by default, reuses any existing
node tab, and opens a guest tab when none exists. Selecting a stopped node shows
its info view without opening a guest shell. Press `i` to inspect the alternate
view for the current node.

When the TUI opens, its fixed-width `CodeLima` wordmark briefly shuffles like a
slot machine and settles from left to right. The effect runs independently of
navigation and terminal startup, then stops completely. A standalone,
dependency-free webpage version is available in
[`examples/codelima-logo-animation.html`](examples/codelima-logo-animation.html).

In the TUI:

1. Press `n` to create a sandbox.
2. Press `s` to start it.
3. Press `Option+t` to open a guest terminal tab.
4. Press `Option+Backtick` to move focus into the terminal.
5. Run your agent with the freedom it needs.

```sh
codex --yolo
```

Create another sandbox and another agent. Open a host tab when you need Git or
editor commands outside the VM. Switch projects without giving up the sessions
already doing useful work.

codelima calls each directory-bound sandbox a **node** in the CLI and TUI.

## Why it changes the agent workflow

### No permission bottleneck

Let agents install dependencies, change system configuration, launch
long-running processes, and explore freely inside a VM. The sandbox contains
system-level consequences while the mounted project keeps useful code changes
on the host.

### Many agents, not one terminal

A node is an independent machine with its own CPU, memory, disk, processes, and
terminal tabs. Open several tabs when agents should share one environment. Give
the same project to several nodes when you want parallel implementations or
isolated experiments. Give different projects their own nodes when you want to
keep several streams of work moving at once.

### Services feel local

Start a web server in a sandbox and reach it from the host without maintaining a
fixed port list:

```text
http://localhost:{port}
http://127.0.0.1:{port}
http://{node}.localhost:{port}
```

If `api-redesign` is serving on port 8080:

```sh
curl http://api-redesign.localhost:8080
```

The daemon listens on both host loopback families, so these hostname routes
work whether the client resolves them to `127.0.0.1` or `::1`.

Two nodes can serve the same guest port at the same time. Their node-qualified
hostnames keep the traffic separate, while the first active node on a port also
claims the short `localhost` form. Codex's browser-login callback is the narrow
exception: when a new listener appears on its default port 1455, generic
`localhost:1455` follows that newest listener so `codex login` in a second node
receives its own browser callback. If browser callbacks are unavailable in the
host environment, use `codex login --device-auth` where account and workspace
policy permit it.

### Sessions survive the interface

A persistent daemon owns terminal sessions and network forwarding. Quit the TUI
and reopen it later; surviving guest and host tabs reconnect in their original
order. The work does not belong to a fragile UI process.

### The host and sandbox stay in step

Mounted workspaces are read/write and keep the same absolute path on both sides
of the VM boundary. Tools, scripts, and agent instructions that refer to the
working directory do not need a sandbox-specific path. Host edits appear in the
sandbox, and agent edits appear on the host.

Use `--workspace-mode copy` when an experiment should have a guest-local copy
instead.

### Real machines when the work needs them

Each node can have its own vCPU, memory, and disk allocation. On supported Apple
silicon Macs, codelima enables nested virtualization automatically, making KVM
available inside the guest for workloads that need another virtualization
layer. Linux uses Lima's QEMU/KVM path.

## Features

- Full-screen TUI for multiplexing sandboxes, agent sessions, services, host
  shells, and experiments
- Multiple independent sandboxes for one directory
- Directory-scoped and all-project views
- Per-node live CPU, memory, and guest root-disk usage refreshed once per second
- Dynamic HTTP and WebSocket forwarding to the host
- Node-qualified `*.localhost` routing when services share a port
- Read/write mounted workspaces, with optional isolated copy mode
- Identical working-directory paths on the host and in the guest
- Daemon-owned terminal sessions that survive TUI restarts
- Multiple guest and host terminal tabs per node
- Fast sandbox creation, deletion, start, and stop workflows
- Configurable vCPU, memory, and disk resources per sandbox
- Automatic nested virtualization on supported Apple silicon hosts
- KVM access inside supported guests for nested virtualization workloads
- Reusable configurations and environment bootstrap bundles
- CLI and JSON interfaces for scripting and automation
- Lima 2.x with VZ on macOS and QEMU/KVM on Linux

## Install

Homebrew installs codelima, Lima, Git, and the private renderer worker. Ghostty
is statically linked into that worker; no separate Ghostty library is needed:

```sh
brew tap brianrackle/codelima
brew install codelima
```

Release archives are available for macOS arm64, Linux amd64, and Linux arm64
from [GitHub Releases](https://github.com/brianrackle/codelima/releases).
Keep `codelima` and `codelima-renderer-worker` beside each other when installing
an archive manually. Source builds and release verification are documented in
[BUILD.md](BUILD.md).

To build from a checkout, run `make build`. Its `make init` prerequisite installs
the pinned Go and Zig toolchains, development tools, a real `pkg-config`
implementation (`pkgconf`), and the static Ghostty dependency under
`.tooling/<os>-<arch>`. No Homebrew or system `pkg-config` installation is needed
for source builds; the first bootstrap requires network access.

Requirements:

- macOS arm64, Linux amd64, or Linux arm64
- Lima 2.1.0 or a compatible newer Lima 2.x release
- Apple Virtualization.framework on macOS, or QEMU with KVM on Linux
- Git

Run the doctor before creating your first node:

```sh
codelima doctor --repair
```

The first start may take longer while Lima downloads the Ubuntu image and
codelima installs the built-in Codex and Claude Code environments.

Both built-in agents use their supported npm packages. CodeLima installs
Node.js 22, configures npm's global prefix as `~/.local` for Lima's
unprivileged login user, and installs `@openai/codex` plus
`@anthropic-ai/claude-code` without root-owned npm state. Stable links under
`/usr/local/bin` keep `codex` and `claude` available in ordinary guest shells.
Bootstrap completes only after that login user successfully executes each
agent's `--version` command.

After upgrading an existing CodeLima installation, run:

```sh
codelima doctor --repair
codelima environment show codex
codelima environment show claude-code
```

Seed revision 6 replaces untouched older built-in installer and validator
definitions. Customized or deleted environments and customized agent profiles
remain user-controlled. Node bootstrap remains frozen at creation except for
exact known defective built-in command sequences: the next `node start`
replaces those sequences, records `node.bootstrap.migrated`, and reruns the
user-owned installation without requiring node recreation.

## Guide

### The TUI

| Key | Action |
| --- | --- |
| `Up` / `Down` | Select a node |
| `n` | Create a node |
| `s` | Start or stop the selected node |
| `c` | Clone the selected node |
| `d` | Delete the selected node |
| `i` | Switch between node info and terminal views |
| `Option+Backtick` or `F6` | Toggle node-list and terminal focus |
| `Option+t` | Open a guest terminal tab |
| `Option+Shift+t` | Open a host terminal tab in the project directory |
| `Option+Left` / `Option+Right` | Switch terminal tabs |
| `Option+Shift+Left` / `Option+Shift+Right` | Move the active terminal tab |
| `Option+w` | Close the active terminal tab |
| `a` | Manage node configurations |
| `g` | Manage environment bootstrap bundles |
| `m` | Show background-task messages |
| `q` | Quit from the node-list focus |

Inside forms, use `Tab`, `Up`, and `Down` to move between fields, `Enter` or
`Right` to open choices, `Ctrl+s` to submit, and `Esc` to cancel.

The Create Node form proposes the slug-safe leaf name of the current directory
as the node slug. The slug and current-directory defaults are shown in muted
text and disappear when you type an explicit value.

On macOS, configure the terminal with `macos-option-as-alt = true` when
available. codelima also recognizes the standard US-layout Option glyphs for
its core shortcuts.

When the terminal reports key repeats and releases, the focus shortcut toggles
once per press. Holding or releasing `Option+Backtick` or `F6` keeps the new
focus until the next press.

Host and guest terminals are tabs on the same node. A red top bar identifies a
host tab. Host tabs stay useful while the VM is stopped because they run on the
host in the node's project directory.

A guest tab — and `codelima shell` — logs you in as the VM's ordinary user, the
same one that owns your workspace and the installed agents, with passwordless
`sudo` when you want it. That is what lets Claude Code run with
`--dangerously-skip-permissions` inside a node: it refuses that flag as root.

### One project, several agents

Create two nodes bound to the current directory:

```sh
codelima node create --slug api-redesign
codelima node create --slug test-hardening
codelima node start api-redesign
codelima node start test-hardening
```

Open the project view and give each node its own terminal tab:

```sh
codelima .
```

Both sandboxes see the same mounted working tree by default but have independent
operating systems and processes. Use separate worktrees when concurrent agents
should not edit the same files.

### Agents that are already signed in

The first time a new node starts, codelima copies your git identity and agent
logins into it so the agent inside can push and work without a second login:
your standard `~/.ssh` keys, the `github.com` lines of your `known_hosts`, your
`~/.gitconfig`, and the Codex and Claude Code credential caches (read from the
macOS Keychain when Claude Code keeps them there), plus the two `~/.claude.json`
keys Claude Code checks before it treats those credentials as a signed-in
account — without them it would ask you to log in anyway. Anything you do not have is
skipped with a warning — it never fails the node. Everything lands with private
modes in both of the guest's homes, so it works in your terminal, in a `sudo`
session, and for the agent's own tooling alike, and the contents never appear in
a log or a node event.

This happens once, at first start, and never again: each node refreshes its own
copy of those tokens from then on, so a later import would sign the node out.
Pass `--no-import-auth` to `codelima node create`, choose "skip" in the create
dialog's Host Credentials field, or set `import_host_auth: false` in
`~/.codelima/_config/settings.yaml` to opt out. A cloned node inherits whatever
its source had, because it boots from the same disk.

### Many projects at once

Open a scoped TUI for each project:

```sh
codelima ~/src/frontend
codelima ~/src/backend
```

Or open the global workshop:

```sh
codelima
```

All views can share one daemon. Closing one path-scoped view does not discard
the tabs belonging to another.

### Shape a sandbox for the job

codelima includes five starting sizes:

| Configuration | vCPUs | Memory | Disk |
| --- | ---: | ---: | ---: |
| `xsmall` | 1 | 1 GiB | 10 GiB |
| `small` | 2 | 4 GiB | 25 GiB |
| `medium` | 4 | 8 GiB | 50 GiB |
| `large` | 6 | 16 GiB | 75 GiB |
| `xlarge` | 8 | 32 GiB | 100 GiB |

`small` is the default. Choose another size at creation:

```sh
codelima node create \
  --slug large-refactor \
  --configuration large \
  --directory .
```

Create reusable configurations when a workflow needs more than a size:

```sh
codelima configuration create \
  --slug web-agent \
  --image template:ubuntu \
  --agent-profile codex-cli \
  --environment codex \
  --bootstrap-command 'apt-get update && apt-get install -y ripgrep' \
  --vcpus 4 \
  --memory 8GiB \
  --disk 40GiB
```

Configuration values are frozen into a node when it is created, so changing a
configuration affects future nodes without rewriting running experiments.

### Work from the CLI

```sh
codelima node list
codelima node show api-redesign
codelima node start api-redesign
codelima shell api-redesign
codelima node logs api-redesign
codelima node stop api-redesign
codelima node delete api-redesign
```

Clone a node to branch an experiment:

```sh
codelima node clone api-redesign --slug api-redesign-2
```

Create a guest-local workspace instead of a host mount:

```sh
codelima node create \
  --slug destructive-experiment \
  --workspace-mode copy
```

Add an explicit raw TCP mapping when dynamic HTTP/WebSocket forwarding is not
the right fit:

```sh
codelima node create \
  --slug tcp-service \
  --port 15432:5432
```

Run `codelima --help` for the complete command surface and
`codelima <group> <command> --help` for command flags. Put global flags before
the command or path:

```sh
codelima --json node list
codelima --home ~/.codelima-work .
```

### Troubleshoot a terminal freeze

All TUIs using one `CODELIMA_HOME` share its daemon. If every tab and VM freezes
together, capture the live daemon before restarting it:

```sh
make diagnose-terminal-freeze
```

The read-only capture records daemon status, terminal state, logs, process
details, and a native macOS sample under `./tmp/terminal-freeze-*`. It does not
stop, update, signal, or send input to the daemon. Review the bundle for
sensitive paths, commands, logs, and metadata before sharing it. The target
uses `CODELIMA_HOME` when set and otherwise defaults to `~/.codelima`.

Agents can invoke the repository skill directly as
`$diagnose-codelima-terminal-freezes`. Run the capture on the host that owns the
daemon, not inside a guest VM, and prefer the platform-scoped binary when
`./bin/codelima` may point at a guest build:

```sh
make diagnose-terminal-freeze \
  DIAG_ARGS='--binary ./bin/darwin-arm64/codelima --terminal-id term_example'
```

An attached TUI automatically reconnects and installs an authoritative daemon
state after a socket failure or daemon live update. It does not replay terminal
input whose outcome is uncertain. While synchronization is in progress, new
terminal mutations are briefly rejected instead of being applied to stale
state. Healthy request connections may remain idle indefinitely; handshake
timeouts are cleared before their long-lived response readers start.

Any number of TUIs can attach to the same daemon at once — including from an
SSH session on another machine — and every one of them can type: keystrokes,
paste, scrolling, and tab controls are never rejected for coming from the
"wrong" window, and scripted `codelima terminal` commands interleave with
live typing instead of interrupting it. The seat selects the window whose
size, focus, theme, selection and search state a terminal adopts. The seat
moves only on explicit signals — launching a TUI, focusing its window, or
`codelima terminal takeover` — never on background reconnects or CLI
activity, and a window that does not hold the seat simply renders the seat
holder's geometry cropped until focus returns.

Daemon-owned shells and PTYs remain in the Go control plane. Each terminal has
its own separately packaged Ghostty renderer-worker process, immutable screen
cache, bounded replay journal, and terminal-local restart budget. If a native
renderer call hangs, CodeLima kills only that renderer and reconstructs its
screen while preserving the shell PID and keeping other terminals and daemon
status responsive. Active renderer changes coalesce behind a 20 FPS publication
ceiling, while idle terminals run no snapshot timer. Key encoding does not
publish an unchanged screen before the shell echoes it, and snapshot sequence
changes preserve cursor- and viewport-only redraws. If an unthrottled
fullscreen program produces output faster than its renderer consumes it,
CodeLima backpressures only that terminal's PTY instead of restarting the
renderer or flooding TUI event connections; daemon control and other terminals
remain responsive.
`codelima --json daemon snapshot` includes connection-independent
`terminal_runtimes` diagnostics such as renderer PID, generation, queue depth,
pending operation, restart count, journal size, and partial-recovery state.

If a daemon built with handoff version 3 reports `handoff message size ... is
outside 1..1048576`, the update rolled back and its terminals are still owned
by the old daemon. Inspect `terminal_runtimes.*.journal_bytes` in `daemon
snapshot`. To preserve as many live shells as possible, close only expendable
high-history tabs until the old inline manifest fits, then retry the update.
The alternative is `daemon stop` followed by `daemon start`, which deploys the
new binary but restarts terminal child processes. Handoff versions 4 and 5
chunk replay so later updates do not have this limitation.

### Embedded terminal features

Press **F7** in a terminal tab for literal history search. Type the query,
use Enter/Shift+Enter for the next/previous match, and Esc or F7 to close.
Search progresses incrementally through native terminal history; switching
tabs cancels the old query. Drag to select and copy text. Repeated clicks select
a word, line, then command output (the last requires shell-integration marks).
Hold Shift to select locally when the guest application captures the mouse.
Selection and search follow the seat because the native viewport is shared.

Paste is admitted as one operation, with a **64 KiB UTF-8 limit**. Oversize,
unsafe or queue-full pastes fail without sending a prefix. Guest clipboard
writes are limited to bounded text and go only to the current seat's attached
frontend; clipboard reads and Kitty acknowledged writes are unsupported.
Outer-terminal clipboard permissions still apply. Titles, bell/progress and
notification badges appear in tabs; notification text is retained in the local
messages view, not sent as unsolicited desktop notifications.

Host default foreground/background, palette and light/dark changes are queried
with a bounded deadline and propagated to hidden tabs too. An unanswered color
query leaves the corresponding native default unspecified. The renderer uses
native formatting for `terminal read`; `--source recent` includes the most
recent 2,000 history rows plus the visible screen, with a 4 MiB output bound.

Static in-band Kitty RGB/RGBA/PNG images display when the outer terminal supports
Kitty graphics and reports cell pixel dimensions. The frontend preserves
source cropping, clipping, pixel offsets and z-order, uploading at most 64 KiB
per draw. Assets and prepared scenes each have a 16 MiB budget. Unsupported
outer terminals keep the text view; file/shared-memory transfers, animation,
registered glyphs and Unicode-placeholder placements are not supported.

Compatible renderer replacement/live update uses a bounded native checkpoint
plus its ordered output tail. Cross-build recovery, exhausted checkpoint quota
and live/in-flight image state use the independent bounded raw fallback, which
can be partial. This is not a promise to restore images or transient selection
and search handles from a checkpoint. History is capped at 10,000 rows/64 MiB.
Idle history compression is enabled; set
`CODELIMA_GHOSTTY_IDLE_COMPRESSION=0` before starting the daemon to disable it.

These changes use the exact statically linked worker described in
[ADR 132](decisions/adopt_static_libghostty_vt_with_bounded_worker_contracts_132.md).
Native release qualification and unavailable manual environments remain tracked
in [TODO #41](TODO.md).

## How codelima works

codelima uses Lima as its VM runtime. VZ provides lightweight virtualization on
macOS arm64; QEMU/KVM provides it on Linux amd64 and arm64. Nodes are ordinary
full-distribution Ubuntu VMs described by inspectable Lima templates.

Three concepts make the system reusable:

- A **node** is one directory-bound sandbox.
- A **configuration** is a recipe for its image, agent profile, environments,
  bootstrap commands, vCPUs, memory, and disk.
- An **environment** is an ordered, reusable set of bootstrap commands.

The persistent daemon owns terminal sessions and discovers guest services. The
TUI is a reconnectable view onto that durable state, not the owner of it.
Physical TUI sockets are disposable; terminal identity and lifetime belong to
the daemon. Every attached view can type into a terminal; only the seat — the
window whose geometry and focus the terminal adopts — is exclusive, and it
follows user focus rather than connection churn. Ghostty rendering is isolated one process per terminal so native
liveness is never a daemon-wide lock. Full renderer snapshots are dirty-driven
and coalesced; the one-second node CPU, memory, and disk sampler does not force
per-terminal snapshot work. Sustained output uses ordered terminal-local
backpressure with a separate renderer health lane, so queue pressure does not
become restart or reconnect churn. Live update transfers bounded renderer
history in multiple frames, so a full journal cannot overflow the 1 MiB
handoff frame limit. Closing a daemon-backed tab removes it from the local TUI
immediately; accepted input drain and the bounded daemon cleanup complete in
the background so a slow connection cannot freeze the cursor or adjacent-tab
selection.

Run `codelima doctor` to inspect Lima, host virtualization, nested
virtualization, and configuration health.

Keep custom `CODELIMA_HOME` and `LIMA_HOME` paths short and place `LIMA_HOME` on
a local filesystem that supports Unix sockets. Lima derives socket paths below
that directory:

```sh
CODELIMA_HOME="$HOME/.codelima-v4" \
LIMA_HOME="$HOME/.lima" \
codelima .
```

Schema-v4 homes intentionally do not migrate older codelima metadata. Use
`--home` or `CODELIMA_HOME` to start with a separate home when evaluating a new
build alongside an older one.

## The name

codelima is built on the awesome [Lima](https://lima-vm.io/) project and takes
inspiration from the name [Colima](https://github.com/abiosoft/colima).
