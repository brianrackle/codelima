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

The TUI opens a fleet table. Selecting or refreshing a node never opens a
shell. Press `Enter` for node details or `x` to explicitly open a shell; `Esc`
returns from detail to the same node.

When the TUI opens, its fixed-width `CodeLima` wordmark briefly shuffles like a
slot machine and settles from left to right. The effect runs independently of
navigation and terminal startup, then stops completely. A standalone,
dependency-free webpage version is available in
[`examples/codelima-logo-animation.html`](examples/codelima-logo-animation.html).

In the TUI:

1. Press `n` to create a sandbox.
2. Press `s` to start it.
3. Select the node and press `x` to open its shell.
4. Run your agent with the freedom it needs.
5. Press `Ctrl+]` to detach back to the manager; the session keeps running.

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

Upgrade an existing installation in place:

```sh
brew update
brew upgrade codelima
codelima daemon update
codelima --version
```

Version `0.3.5` restores guest OSC 52 clipboard delivery, including Codex
`/copy`, and removes the copy error from clicking without a selection. It
installs Codex and Claude Code natively as the guest login user and includes
bubblewrap with Codex. Run `codelima daemon update` and reopen attached TUIs;
untouched built-in agent environments migrate when their nodes next start.

Version `0.3.4` gives ordinary terminal tabs short, unnumbered `shell` and `host`
names, including common username/path shell titles. Application titles and
status indicators remain visible. Reopen the TUI after upgrading to use the
new names; application-provided suffixes such as ` | codelima` are preserved.

Version `0.3.3` uses program-reported tab titles, keeps background titles and
working/progress indicators current, and shows `🔔` until you visit the tab.
Run `codelima daemon update` and reopen attached TUIs after upgrading so both
the daemon and frontend use the new tab metadata updates.

Version `0.3.2` fixes outer-window resize corruption, dropped punctuation and
international text, and idle daemon handoff after resizing. Reopen attached
TUIs after upgrading to use the corrected drawing and input handling.

Version `0.3.0` promotes the libghostty beta to the regular `codelima` release.
The separate beta formula is retired. If you installed `codelima-beta`, first
install or upgrade `codelima` and run
`"$(brew --prefix codelima)/bin/codelima" daemon update`, then remove the old keg
with `brew uninstall codelima-beta`. Remove any beta-specific `PATH`
entry from your shell configuration so `codelima` selects the regular binary.
The existing CodeLima home and terminal state are reused; `daemon update`
attempts to preserve live terminals while switching to the invoking binary.

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

In a Debian/Ubuntu arm64 development VM, `make init-qemu-guest` installs QEMU
and firmware under `.tooling/linux-aarch64/qemu` using the configured APT
repositories, without changing system packages. Add its `bin` directory to
PATH alongside Lima. `make test-qemu-guest` boots and immediately quits a small
empty KVM machine. If the current process lacks access to `/dev/kvm`, these
checks use passwordless sudo to add the device's group only to the child
process; device permissions and user membership are unchanged. Use
`make run-kvm KVM_ARGS='COMMAND ARGUMENTS'` for other development commands.
Pass fixture variables after `env` in KVM_ARGS so they cross the sudo boundary.

Run `make verify` for formatting, lint, tests and a build. Run
`make go-doc GO_DOC_ARGS='context.WithCancelCause'` to inspect a Go API, or
`make gopls GOPLS_ARGS='check internal/codelima/environment_test_run.go'` for
source diagnostics. Use
`make test-tui-input` for focused shortcut and input-lifecycle regressions, and
`make test-tui-resize` for window resizing and terminal geometry regressions. To
check a downloaded archive for the current platform, place it and its matching manifest together
under `./tmp/` and run `make test-package-artifact DIST_DIR=./tmp/release-check`.
Remove those downloaded verification files afterward.

For renderer development, `make test-vaxis-fork` runs the local fork's tests;
`make test-vaxis-upstream` installs project-local mise tooling and runs its
upstream formatting, lint and test checks. Build and release details are in
[BUILD.md](BUILD.md).

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

Both built-in agents use their official native installers, running as Lima's
unprivileged login user. Their binaries and update state live in that user's
home directory; no Node.js or npm installation is required. System setup
installs prerequisites such as curl, certificates, and Git. The Codex environment
also installs Ubuntu's `bubblewrap` package and verifies `bwrap --version`. Stable links
under `/usr/local/bin` expose the user-owned commands in guest shells.
Bootstrap completes only after that login user successfully executes each
agent's `--version` command. Run agents directly, without `sudo`, including
`claude --dangerously-skip-permissions` or `codex --yolo` inside the sandbox.

After upgrading an existing CodeLima installation, run:

```sh
codelima doctor --repair
codelima environment show codex
codelima environment show claude-code
```

Seed revision 9 adds bubblewrap to untouched Codex environments and replaces
untouched older built-in installer and validator
definitions. Customized or deleted environments and customized agent profiles
remain user-controlled. Node bootstrap remains frozen at creation except for
exact known defective built-in command sequences: the next `node start`
replaces those sequences, records `node.bootstrap.migrated`, and reruns the
native user-owned installation without requiring node recreation. Existing npm
package files are left in place, but the public commands point to the native
installations. Agent credentials and unrelated npm packages are preserved.

## Guide

### The TUI

The TUI opens the fleet manager. Browsing or refreshing nodes never creates a
shell. Wide terminals show a selected-node preview below the table; widths
below 100 columns use a compact table. `Enter` opens node detail, and `Esc`
returns to the same selection.

Overview shows Identity, Resources, Sessions, Ports and Recent events.
`Ctrl+Up` / `Ctrl+Down` cycles panel focus; `Up` / `Down` selects or scrolls
inside that panel. `Tab` / `Shift+Tab` cycles the node's detail tabs. Compact
windows keep identity beside the focused Sessions, Ports or Recent panel;
focusing Resources replaces identity until focus moves again.
In Sessions, `Enter` attaches the selected session, `t` creates a fresh guest
shell, `T` creates a fresh host shell, and `K` confirms ending that exact
session. Ctrl+] returns from attachment. Session age comes from the daemon;
an unknown age or activity stays unknown. In Ports, `o` opens the selected
live HTTP route and `y` copies its URL. Mouse rows select, the wheel moves
within a panel, and panel links open Spec, Metrics, Ports and Logs.

An empty scope shows a first-run guide with the stored `small` template's
resources and the latest host checks. `n` creates in the displayed directory
scope; all-node mode uses the launch directory. Failed or unavailable host
checks explain why creation is blocked there. `R` re-checks without repair and
reloads template defaults; `5` opens Doctor. PgUp/PgDn or the mouse wheel reveal
complete long findings. A filter with no matches stays in the searchable table.

| Key | Action |
| --- | --- |
| `1`–`5` | Nodes, Templates, Ports, Activity, System |
| `0` | Toggle all nodes / launch-directory scope |
| `Up` / `Down` or `j` / `k` | Select a node |
| `/` | Filter names/directories, or use `status:`, `template:`, `dir:`, `port:` |
| `G`, `<` / `>`, `=` | Toggle directory grouping, choose sort, reverse order |
| `Space`, `*` | Mark one node / all visible nodes; marks survive filtering |
| `n`, `s`, `c`, `d` | Create, start/stop, clone, delete |
| `r` | Restart selected/marked nodes; an unmarked failed node retries its failed step |
| `o` | Open the selected node's first current, live HTTP route |
| `x`, `X` | Open a shell / show shell choices |
| `Tab` / `Shift-Tab` | Cycle node detail tabs |
| `a`, `g`, `m` | Configurations, environments, activity |
| `:`, `?` | Search commands / keyboard shortcuts |
| `q` | Quit from a global view; return from node detail |

Press `:` to search commands by name, node, context or shortcut. Results are
grouped into Nodes, Actions and Go to; matching letters are underlined. Use
Up/Down or the wheel to select, Tab to complete and Enter (or a result click)
to run. Esc closes. Pasting text searches without running it. An empty query
shows the latest available commands used in this frontend session, or the full
catalogue when none are available. History is not saved after exit.

For example, search `start api`, `status: failed`, or `Logs bootstrap`.
Per-node commands retain their target even if the fleet selection changes.
Unavailable results require a new selection or query. `start all in` reviews
the directory's captured targets, resumes failed bootstrap steps with Retry
and starts stopped/created nodes in a daemon-owned batch; unrelated marks are
preserved. Up to four targets run in parallel. Other targets continue after a
failure or frontend exit. Memory warnings describe cached allocation, not free
OS memory.

Press `?` for contextual key help. The current view leads the columns; compact
terminals show one group at a time. Tab/Shift-Tab or Left/Right changes groups;
Up/Down, PgUp/PgDn, Home/End or the mouse wheel scrolls the focused group.
Click a column to focus it. `/` searches all groups by key, context or description
(for example `overview session` or `system restart`). Search accepts pasted
text; Enter never runs a binding. Tab returns to columns, and Esc or `?` closes
help while preserving the underlying view. Keys act once per press; navigation
and text editing repeat while held. Key remapping is not implemented yet.

Press `n` for the create form. Names, directories and optional `HOST:GUEST`
ports validate inline; Tab accepts a suggested name when one is already used.
Enter on Directory opens a browser. Enter on Template opens a filterable picker
with resource, environment and memory-fit details; its new-template action
returns with the saved template selected. Capacity shortfalls warn but do not
block creation. Tab/Shift-Tab and arrows reach every field at compact sizes.

`Space` toggles workspace, credential and launch choices. The form defaults to
starting the new node; attachment is optional and waits for successful startup.
Ctrl-S submits, then opens the new node's Overview. Leaving cancels automatic
navigation/attachment while accepted work continues. A rejected admission keeps
the form editable; an unconfirmed reply offers Ctrl-S to retry the same request.
Esc asks before discarding edits.

In Templates (`2`), `n` creates from `small`, `e` edits and `c` clones into the
same full editor. Type resource values such as `20G`, or use Left/Right to step
through sizes. Enter opens installed image or agent-profile choices. Space
toggles an environment; Shift-Up/Down changes its execution order. `F2` opens
the complete resolved preview at compact sizes. Existing nodes keep their
frozen settings when you save.

Enter on Bootstrap opens command blocks. Each block can contain multiple lines;
paste preserves line breaks and literal tabs (shown as `⇥`).
Tab switches blocks, Ctrl-N adds one, Ctrl-X removes one and Alt-Up/Down changes
their order. Ctrl-S returns command edits to the template; Ctrl-S in the template
saves it. Esc asks before discarding edits. `R` on a built-in template loads its
defaults into the editor for review and saving; `small` cannot be renamed.

The recipe lists show global usage even when Nodes is scoped to one directory.
Right opens details; Left returns to the list. At narrow widths these occupy
the same panel. Page Up/Down scrolls a list or details; the mouse wheel scrolls
the panel under it. Template details identify nodes whose frozen values drift
from the current recipe.

Tab switches to Environments. Right focuses its command list; Up/Down selects
a block, Enter edits it, `o` adds one, Shift-Up/Down reorders it and `D` removes
it from a draft. `E` opens the selected block in `$EDITOR` (default `vi`) and
returns to the draft for review. Ctrl-S in the command editor applies its edits;
Ctrl-S in the environment draft saves. `R` reviews shipped built-in commands.
The last-run line uses recorded node execution, and says when none is available.

Environment details flag literal curl uses without a recognized earlier
apt/apt-get install. This static check is advisory: the image, earlier environments
or dynamic scripts may already provide curl. Where an earlier install can be
edited, Enter on the warned command or clicking its suggestion opens an unsaved
command draft with curl added. Apply the command, then save the environment;
Esc can discard the proposal. `e` retains ordinary editing. Provider hints use
current saved definitions, including edits to built-ins. Syntax and size limits
are reported without blocking editing or execution.

Press `t` on a saved environment to test its commands on a disposable xsmall VM.
Activity opens the live output; `K` cancels execution and still deletes the VM.
Esc closes the output while the test continues. Reopen its Activity row later
to read retained output, including after VM deletion. The test uses a private
empty workspace, imports no host credentials and publishes no ports. Its image
and resources come from the current xsmall configuration. Cleanup retries after
runtime errors and resumes after daemon restart without rerunning commands.
If admission is unconfirmed, `t` on the same environment retries that request.

For example, `/status:running template:small dir:"acme api"` combines filters.
Selection follows node identity through sorting and refreshes. Missing metrics
show `—`. Metric sorts place unavailable readings last and temporarily flatten
directory groups. Batch actions include marked nodes hidden by the filter and
show one confirmation; deleting a batch requires its node count. Marks replace
the selected preview with all-target resource/session counts and a projected
after-stop global allocation. Hidden targets remain included even when no rows
match. Unknown observations stay explicit; these projections describe the
latest host snapshot, not a resource reservation.

Wide fleet rows show the last eight seconds of CPU as a sparkline. The
selected-node preview shows 60 seconds, including samples retained before
this window opened, CPU average/peak and a memory history chart with observed
available bytes. Runtime, mount and nesting settings come from the selected
Lima instance's configuration; workspace access is shown only for its matching
mount. Missing settings remain `?`. The preview also includes creation/auth
state and the Lima instance name. Marked rows include reported working tasks.
The preview and Overview show each agent's recorded validation separately:
`✓` is a successful check, `?` is missing evidence, and `↳` marks a result
inherited from another node. “Validated as login user” requires the built-in
version checks to have actually run on this node. Custom profile checks are
labeled separately. Results describe the recorded execution, not continuous
agent health; stale results are labeled and stopped nodes retain their history.
Open Spec to inspect the frozen commands and execution records.
Missing samples remain blank; wide memory and disk cells
show observed used/total GiB with gauges, and compact cells show percentages.
At 80 columns inactive views use their number, leaving space for connectivity
and global CPU/memory allocation. Flat tables include each node's directory.

Port filters include configured guest ports and routes discovered by the daemon.
A `?` marks missing or stale forwarding observations; retained port numbers may
still match filters. `o` opens the first live HTTP route for the selected ready
node, using its recorded node-qualified address. Unavailable, stale, held,
stopped, conflicted and TCP routes cannot open through this shortcut. `3` opens
Ports for inspection. Running lifecycle rows show recorded phase progress at
both widths; phase counts describe completed work, not estimated time.

Fleet failure rows retain the applicable bootstrap/validation step, recorded
exit code and duration. Recovered interrupted attempts have no invented finish
time; a live lifecycle claim or newer metadata takes precedence. Stopped rows
label the confirmed last stop, rather than claiming VM uptime. The wide
preview's three recent events belong to the selected node and stay independent
of Activity pagination. Unavailable or stale evidence is labeled explicitly.

Session activity uses terminal integration: progress reports working, error or
paused, and prompt markers report at prompt. Otherwise activity is unknown;
a title alone does not prove work or idle time. Integrated sessions show `idle`
with the time since the prompt or most recent accepted input. This shared clock
survives reopening the TUI, renderer recovery and live daemon updates. Stale or
disconnected activity stays unknown. A marked target shows an idle duration only
when all its sessions are observed idle, using the most recently active session.
Marked impact distinguishes guest
sessions that stopping ends from host sessions and includes hidden targets.

Open a node with Enter and Tab to Metrics. `+`/`-` select 1m, 5m, 15m or 1h;
Space pauses/resumes. CPU and memory charts use observed samples, with gaps
when unavailable. The daemon retains up to one hour, lost on restart. Disk is
the guest root filesystem; host workspace mounts are excluded. Wide views
also show per-core CPU, memory cache, available memory and swap.

The process table samples only while Metrics is open and unpaused. `p` switches
CPU/memory order, arrows select a process, and `y` sends its PID to the host
clipboard. Process CPU uses one core as 100%, so a multithreaded process may
exceed 100%. The first sample shows `—` until a second reading is available.
The table shows the top 64 processes and retains both CPU and memory leaders
so sorting remains accurate while paused.

In node detail, `l` opens Logs. `[`/`]` choose Events, Bootstrap, Lima or
Serial; `f` toggles follow, `w` wraps long lines, and `L` cycles severity.
Arrows and the mouse wheel scroll; `g` goes to the top and `G` resumes at the
end. `/` searches literal text without regard to case and pauses follow.
Enter keeps the search, Esc cancels editing, and `n`/`N` move between matching
lines. `y` sends the selected full line to the host clipboard. `o` suspends the
TUI and opens the original file in `$PAGER` (default `less`); for example,
`PAGER='less -S' ./bin/codelima` keeps long lines unwrapped in the pager.

Logs reads the current file's last 2 MiB, up to 5,000 lines, and labels clipped
history. Bootstrap and validation output is retained in private
`nodes/<id>/bootstrap.log` files, with a 4 MiB cap plus one `.1` generation.
The Bootstrap source fills on the next bootstrap or validation run; output
from earlier versions cannot be reconstructed. Missing files are shown
explicitly. Reading logs does not start a guest.

The Spec tab shows frozen values, current template drift, and the actual
`instance.lima.yaml`. The amber dot appears when a compared value differs,
including changes inside assigned environments. `←`/`→` or `Ctrl+↑`/`Ctrl+↓`
select a panel; arrows, Page Up/Down and the mouse wheel scroll it. Compact
terminals show the selected panel. Steps show their frozen source, latest
outcome, observed duration and exit code. Enter opens that attempt's captured
output in Logs with follow paused. Older output may have rotated out; older
nodes explicitly show unrecorded history, and clones label inherited runs.

In Spec, `N` opens a new node form with the current template and node directory;
`c` clones the frozen node; `e` edits the current template. `v` opens the stored
Lima YAML in `$PAGER`, and `y` copies its path. Missing templates or YAML do not
hide the frozen recipe.

For a failed node, press `r` or run `codelima node retry NODE` to resume its first
recorded failed step, skipping a recorded successful prefix. A command may have
partially changed the guest before failing, so review its output first.
`codelima node start NODE` still reruns every step of an incomplete bootstrap;
after successful bootstrap it only revalidates the agent. Canceled attempts
and old failures without step records require ordinary start. `s` stops a VM
that remains running after bootstrap failure.

For other nodes, `r` restarts the selected node; marked fleet rows get one
confirmation listing every target. Detail actions apply only to the open node.
`codelima node restart NODE` explicitly stops and starts under one lifecycle
claim. It confirms shutdown before booting, preserves completed bootstrap, and
reruns incomplete setup. Restart uses the stop-confirmation preference because
guest sessions end. Each admitted restart continues after its caller exits.

Marked start/stop, restart and delete actions review the complete captured list,
including hidden marks. Bulk deletion requires typing the exact target count.
Stop and restart end guest sessions while host shells continue; deletion ends
both guest and host sessions belonging to each deleted node.
The daemon admits all targets together, reserves them through batch completion,
and runs up to four at once within its global limit of sixteen lifecycle
operations. A batch accepts up to 64 targets. Queued targets and running phases
appear in fleet rows; Activity retains individual receipts and a batch summary.
Open the batch to inspect every outcome or press K to cancel its remaining work.
Canceling a running child's receipt affects only that node. Completed work is kept;
canceling startup stops that VM. After daemon interruption, completed outcomes
remain and uncertain/unstarted targets are marked interrupted for explicit review.
An unfinished deletion shows `delete incomplete` once fresh daemon evidence
confirms its owner has released the node. Fix the reported provider problem and
press `d` to review deletion again; active or stale ownership still blocks retry.

Lifecycle actions launched from the TUI or CLI (`node create`, `start`, `retry`,
`restart`, `stop`, `clone`, `delete`) run in the daemon and continue when the caller exits.
CLI progress goes to stderr; stdout retains the command's result format.
The CLI honors daemon autostart; with it disabled, run `codelima daemon start`
first. A responsive daemon with a different version requires explicit
`codelima daemon update`. Finish lifecycle operations before updating the daemon;
an explicit daemon shutdown interrupts them without automatically replaying
partially executed commands when the daemon restarts.

Use `codelima node create --slug api-redesign --start` to create and boot a node
under one receipt. This request continues after the caller exits; canceling it
after creation also stops its VM. Ordinary `node create` only creates the node.
Activity and node detail show the combined request, including a failed first
boot and its created node identity.

Inspect and explicitly cancel work from any terminal:

```sh
codelima operation list --limit 20
codelima operation show OPERATION_ID
codelima operation cancel OPERATION_ID
codelima --json operation list --before CURSOR_FROM_PREVIOUS_NEXT_FIELD
```

Cancel requests return a receipt; use `show` to confirm cleanup has finished.
`show` includes retained output and recorded lifecycle phases. Skipped work
has no invented duration, and interrupted work has an unknown finish time.
If a CLI loses its connection or stops waiting, its error includes the receipt
ID. Inspect that ID before retrying: a lost response does not prove the action
failed. Direct Go SDK services retain their existing inline execution behavior.

Node Overview shows the latest startup's measured phases and retained output.
While it runs, `x` queues attachment to that specific startup; it opens only
after successful completion and confirmed node readiness. `Esc` cancels the
queue, and leaving the node detail view also cancels it. Startup continues
when you navigate away. `s` requests cancellation of the active startup and
stops the VM; Metrics becomes available after startup ends.

A failed Overview shows the recorded command, source, exit status and duration
when available, alongside the VM's separately observed state. `x` inspects a
running failed VM, `l` opens the failed attempt's output, and `r` retries the
recorded suffix. `e` opens the source environment and `E` the source template;
those edits affect future nodes, while this node retains its frozen commands.

Press `4` for shared Activity history. Select a record with the arrows;
`Enter` opens its node and `K` requests cancellation. Canceling start or retry
also stops the VM. `f` cycles all/warnings/errors, `/` filters the current
history page, and `y` copies the selected record. Use `[`/`]` for newer/older
pages and `g` to return to newest. Click the severity choices, footer controls,
or a row's action hint to use the same actions with the mouse. History keeps
its columns aligned; older dates and the visible row range identify where you
are in retained history. At compact widths, row actions use `⏎` and the footer
keeps the main controls visible. Lifecycle requests and completions share
history with notifications from every TUI. New requests identify CLI/TUI origin;
older receipts omit unknown origin. Operation receipts are retained for seven
days. General messages retain up to seven days or 10,000 entries, whichever
limit comes first; each message is limited to 4 KiB. They survive daemon restart.
Messages labeled “this window” await persistence and may be lost on exit or
when this window's 200-entry queue fills. Delivery retries on reconnection.
Footer notices expire after six seconds; their history remains in Activity.
Successful in-place daemon updates also record the old and new versions and
the number of preserved terminal sessions, even with no TUI open. The successor
queues this notice after sending its commit acknowledgment; a crash before
persistence can still lose a pending notice.
Terminal bells and notifications are recorded by the daemon even with no TUI
open. Repeated identical notices are counted; bursts between snapshots show a
count and the latest notice. Restoring a session does not replay old notices.
Enter on a terminal record attaches that exact session; an ended session reports
that it is gone and keeps its history. Escape cancels an attachment still being
resolved. Host, route and update records open Host, Ports and Daemon respectively.
If a live daemon update rolls back, Activity reloads messages already accepted
by the replacement before recording new ones.
The daemon also records route additions/removals, degraded/restored forwarding,
localhost ownership changes, and host CPU/memory overcommit and recovery while
no TUI is open. Capacity follows observed VMs across all directories; unknown
observations cannot imply recovery. Native warnings and errors appear in the
footer for six seconds. Unchanged observations do not repeat notices.
Capacity notices follow the daemon's runtime observations, so a delayed runtime
refresh can delay the notice even when a VM has already started or stopped.
Up to 256 native events can await persistence in memory; retries keep their IDs.
A full queue omits newer events and later records the omitted count.
Successful live updates transfer pending events and observation baselines.
Crashes before persistence can still lose pending events; committed events
follow normal retention. Older daemons that do not transfer this queue cannot
recover those pending notices during their first upgrade.
Running rows show current phase/step text and completed/total phases. The bar
counts observed phase boundaries, including skipped work; it does not predict
time remaining. Compact terminals put progress on a second line. Older receipts
without phase evidence show “progress unavailable”. Finished rows show duration
only when an actual finish time was recorded.

At 140 columns and wider, the header shows the host model, logical CPUs and
memory. The allocation summary includes CodeLima VMs in every directory, even
when the fleet is scoped or filtered. A failed node still counts while its VM
is up. Disk space is measured on the LIMA_HOME volume; missing observations
show `—`. These are configured allocations, not current host CPU/RAM usage.

System has Doctor, Daemon, Host and Settings panels. Use `Tab`/`Shift-Tab` to
move between them and arrows or the mouse wheel to scroll. Smaller terminals
show the focused panel; wide terminals of at least 34 rows show all four.
The System badge counts open Doctor warnings, failed checks and observed
allocation warnings across views. Checks run at startup and once per minute;
entering System or pressing `C` re-checks immediately. A `?` marks unknown or stale results. Select a Doctor
row and press Enter, or click details, to read its full message. `R` runs
metadata repair: unreadable records move to `_quarantine/`, and existing
built-in/stale-claim repairs apply. Completed repair messages remain in Activity;
a fresh check determines which findings are still open. Doctor reads the latest
settings file without rewriting malformed YAML during a normal check.

The Daemon panel refreshes every three seconds while System is open. It shows
observed PID/version, session and subscriber counts, seat ownership, forwarding
listeners/degraded ports/lapsed routes, telemetry cadence and reclaim state.
Subscribers include event observers as well as TUIs. Failed refreshes retain
an explicitly stale snapshot. Focus Daemon and press Enter or click details
for full errors and sample times; arrows/wheel reveal additional rows.

In System, press `u` to update the daemon while keeping live sessions. This
control fails explicitly if live handoff is unavailable; it never falls back
to a restart. `S` opens a restart confirmation: existing shell processes end,
and saved tabs reopen as fresh shells when `daemon.restore: respawn` is set
(the default); `forget` discards them. Keep the TUI open until restart finishes.
`L` opens the daemon log, with `C` to reload and `o` to open your pager.
`D` captures diagnostic evidence in the background. You can close its view
and reopen it with `D` while it runs; completion records the saved path in
Activity. These actions also appear in the command palette.

By default, `x` attaches the node's daemon session directly to your terminal and suspends
the manager. `Ctrl+]` detaches; `exit` ends the session. `codelima shell NODE`
uses the same managed shell, and `codelima terminal attach TERMINAL_ID` resumes
a specific session from `codelima terminal list`. Supplying a command after
`codelima shell NODE` runs that command through the existing execution path.

`X` opens a grouped, scrollable shell chooser. Under New session, Enter on
Attach here creates a fresh guest session; `w` opens one in the configured
terminal window, `t` in tmux, and `h` in the node's directory on this host.
Unavailable launchers and guest readiness show their reasons. Existing sessions
show observed activity, age and unacknowledged bells; select with arrows/Enter,
mouse or `1`–`9`. Numbers stay bound to their original sessions until you reopen
the chooser, so a closed session cannot redirect a delayed key. Arrows and
PgUp/PgDn reach sessions beyond nine. Esc closes.

Shift+Enter on Attach here, Shift+w or Shift+t saves that mode as `x`'s default.
The footer shows the current default. `x` reuses a suitable session when one
exists; the chooser's New session choices create fresh ones. Open **System**,
cycle to Settings, then Enter to edit shell mode, the window command, host credential import,
stop confirmations, directory grouping or color. Edits preserve YAML comments
and unknown keys. These preferences can also be changed from the CLI:

```sh
codelima settings set shell.mode tmux
codelima settings set shell.window 'ghostty -e {cmd}'
codelima settings set color ansi
```

The window command is a quoted argument list; `{cmd}` must be a standalone
argument and expands to the managed session's attach command. Use an executable
wrapper for shell expressions. Stop confirmation defaults to guest sessions
that are working, paused or lack a fresh idle prompt; host shells keep running.
`confirm_stop` accepts `busy`, `always` and `never`. The stop review lists guest
sessions and observed resource release. Press `x` to inspect its named guest
first; Ctrl+] returns to the review without ending that session. Space toggles
whether idle guests skip future reviews; the preference saves only on submit.
Use `s` or Enter to stop and Esc to cancel. PgUp/PgDn or the mouse wheel scrolls
long reviews. The detach key is fixed at Ctrl+].

Single-node deletion shows virtual disk capacity, configured ports and the
copy/mounted workspace consequence. Type the exact node slug, including case
and without surrounding spaces, then Enter. Esc cancels immediately. Guest-only
changes are lost; the host directory is retained. Changing selection does not
change the reviewed target, and removed or renamed nodes require a new review.

Set `shell.mode` to `embedded` in Settings to enable `X` then `e`, which opens
a fresh embedded Shell tab. It reserves **F6 only** while the shell has focus. All
other keys, including Option/Alt arrows, reach the shell. Press F6 to return
to TUI focus, then use `[` / `]` to switch tabs, `t` for a guest tab, `T` for a
host tab, `w` to close, or `F7` to search. Tab creation/closing and focus changes
act once per press; navigation and text editing repeat while held. Releases
from a focus-changing key cannot act on the newly opened form or shell.

The Shell detail is visible only in embedded mode, even when no sessions are
open. Its session strip keeps the active tab visible when resized; host tabs
have coral accents. Click a session, the guest/host creation controls, the focus
label, another detail tab or the Nodes breadcrumb to navigate. The focus label
sits outside the terminal content. Forwarding status reflects current route
observations and explicitly reports unavailable or stale data. Switching away
from embedded mode hides Shell and returns to Overview without closing sessions.

Inside forms, use `Tab`, `Up`, and `Down` to move between fields, `Enter` or
`Right` to open choices, `Ctrl+s` to submit, and `Esc` to cancel. The Create
Node form proposes the current directory and its slug-safe leaf name. These
muted defaults disappear when an explicit value is typed.

The major-version implementation is still in progress. The
[delivery ledger](plans/tui-vnext/README.md) records incomplete design surfaces
and runtime contracts, including durable activity and complete forms.

Host and guest terminals are tabs on the same node. Ordinary guest tabs are
named `shell` and host tabs `host`, without numbers. Common shell titles such
as `bash` or `user@host: /path` use those compact defaults. Application task
titles still appear (prefixed with `host:` on host tabs), with progress and
bell indicators beside them. When the shell reports its ordinary title again,
the compact default returns. Duplicate names are allowed; position and the
active highlight distinguish tabs. A red top bar identifies a host tab. Host
tabs stay useful while the VM is stopped because they run on the host in the
node's project directory.

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
codelima node restart api-redesign
codelima node stop api-redesign
codelima node delete api-redesign
```

Clone a node to branch an experiment:

```sh
codelima node clone api-redesign --slug api-redesign-2
codelima node clone api-redesign --slug api-redesign-3 --start
```

Cloning copies the disk and preserves the source directory, template link,
frozen values, bootstrap state and workspace mode. A running source stops for
the copy and then restarts; its guest shell processes end, while host shells
keep running. The clone stays stopped unless `--start` is supplied. Optional
startup runs after source restoration under the same daemon receipt, even if
the CLI or TUI exits. A failed child start leaves the clone available to inspect
and retry.

In the TUI, `c` opens a review with an available suggested name. Edit the name,
use Tab then Space (or click) to toggle **Start clone when done**, and press
Ctrl+s to submit. Esc cancels. The review shows observed source/session impact;
the daemon checks the name again when it executes the request.

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

The daemon opens published TCP mappings after bootstrap and validation succeed,
and relays them over the guest tunnel. Keep the daemon running while using these
ports. A daemon restart or update ends existing TCP connections; clients can
reconnect afterward. A published TCP mapping reserves its host port, so HTTP
hostname multiplexing on the same port reports a conflict.

Existing running nodes created with static Lima mappings keep those mappings.
An explicit `codelima node stop NODE` followed by `codelima node start NODE`
migrates them to daemon forwarding while preserving `HOST:GUEST` values. Until
that migration, their static mappings do not wait for bootstrap readiness.

Press `3` in the manager for Ports. It lists discovered HTTP/WebSocket routes and
published TCP mappings, including stopped, held, retrying and conflicting routes.
Process names come from the guest when available; `—` means unknown. Use `/` to
filter by port, node, process or address, arrows to select, `o` to open an HTTP
route, `y` to copy its address and Enter to open that exact node. `C` refreshes;
PgUp/PgDn scroll complete ownership and diagnostic details. Compact terminals
show addresses on a second line. Mouse controls and the command palette expose
the same actions.

For shared HTTP ports, `p` pins bare `localhost:PORT` and loopback addresses to
the selected node. Named `{node}.localhost` routes keep their own target. Pins
and discovery order survive daemon restart/update. A pin lasts until its listener
disappears, the node loses readiness, stops or is deleted; a temporary tunnel
failure retains the pin and returns a retryable 502. Without a pin, ordinary
ports prefer the first active claimant and port 1455 prefers the newest listener.
If a pin reply is lost, refresh Ports to inspect the owner before acting again.
Published TCP mappings reserve their whole host port and cannot be pinned.

Routing preferences live in the private `_daemon/forwarding.json` file under the
CodeLima home. An unreadable file is preserved and reported in Ports; restore a
known-good copy while the daemon is stopped before attempting another pin or
live update.

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
codelima daemon diagnose
# Choose the frozen terminal and a new destination directory:
codelima daemon diagnose --terminal-id term_example --output ./tmp/freeze-evidence
# From a source checkout, the build-free wrapper uses the same collector:
make diagnose-terminal-freeze
```

The read-only capture records daemon status, terminal state, logs, process
details, and a native macOS sample. The installed command and System `D` save
private bundles under `CODELIMA_HOME/_diagnostics/terminal-freeze-*`; the Make
wrapper defaults to `./tmp/terminal-freeze-*`. It does not
stop, update, signal, or send input to the daemon. Review the bundle for
sensitive paths, commands, logs, and metadata before sharing it. The target
uses `CODELIMA_HOME` when set and otherwise defaults to `~/.codelima`.
Each RPC has a five-second deadline; optional probe failures are retained in
`probes.json`, error and exit files. Log tails are capped at 256 KiB, copied
files and command output at 1 MiB; daemon JSON retains the protocol frame limit.
Use `--sample-seconds 0` to omit native samples (maximum 30 seconds). Capture
does not start a missing daemon. A successful capture means evidence was saved,
not that every probe succeeded.
Malformed settings do not block capture and are left unchanged.

For an explicit process replacement, run `codelima daemon restart`. It has the
same shell/process impact as System `S`. If the reply to an update or stop is
lost, inspect `codelima daemon status` before retrying. If the frontend exits
after shutdown but before startup, use `codelima daemon start` to recover.

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
new binary but restarts terminal child processes. Handoff versions 4 and later
chunk replay so later updates do not have this limitation.

### Embedded terminal features

Press **F7** in a terminal tab for literal history search. Type the query,
use Enter/Shift+Enter for the next/previous match, and Esc or F7 to close.
Search progresses incrementally through native terminal history; switching
tabs cancels the old query. Drag to select and copy text. Repeated clicks select
a word, line, then command output (the last requires shell-integration marks).
Hold Shift to select locally when the guest application captures the mouse.
Selection and search follow the seat because the native viewport is shared.
A plain click without dragging clears the selection without copying.

Paste is admitted as one operation, with a **64 KiB UTF-8 limit**. Oversize,
unsafe or queue-full pastes fail without sending a prefix. Guest clipboard
writes accept up to **64 KiB of UTF-8 text** and go only to the current seat's
attached frontend; clipboard reads and Kitty acknowledged writes are unsupported.
The TUI forwards OSC 52 to the outer terminal, which owns clipboard handling
for both local and remote sessions. Its clipboard permissions still apply.
The status message reports a copy request because OSC 52 delivery cannot
confirm that the outer terminal accepted it.

To check guest-to-host copying, run this in a guest shell, then paste into a
host editor:

```sh
printf '\033]52;c;%s\007' "$(printf 'guest clipboard probe' | base64 | tr -d '\n')"
```

With multiple CodeLima windows, focus the window whose host clipboard should
receive the copy. Restore your previous clipboard contents after testing.

Tabs use the title reported by the running program, such as a Codex session
title, without an added node name or tab number. For example,
`codelima 1 · Test this | codelima` becomes `Test this | codelima`. Empty titles
and common shell username/path titles display `shell` or `host`, without
numbers. Host application titles retain the `host:` prefix. Application titles
are bounded to 40 characters.
Titles, application-provided working spinners, progress and bell indicators
continue updating whether or not their tab or CodeLima window is focused.
Switching tabs does not change a session's working status.
The `🔔` suffix means a terminal bell arrived while you were away from the tab.
Visiting the tab in the focused window clears it; a later bell can show it
again. Bells received while you are viewing the tab are already acknowledged.
Acknowledgement is local to each TUI window. It does not identify an agent's
current state.
Progress and notification badges also appear in tabs. Notification text is
retained in shared Activity for daemon sessions and local history for fallback
sessions. These notices do not request desktop popups.

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
