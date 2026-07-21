# CodeLima

CodeLima is a Go CLI and shell-first TUI for directory-bound coding VMs. It uses Lima 2.x as its sole runtime, with VZ on macOS arm64 and QEMU/KVM on Linux. Nodes are full-distribution Ubuntu VMs configured through inspectable Lima templates and commands.

The model has three user-facing objects:

- A configuration is a reusable, directory-independent VM recipe: image, agent profile, environments, direct bootstrap commands, vCPUs, memory, and disk.
- An environment is a reusable ordered list of bootstrap commands that configurations can reference.
- A node is a VM bound to one host directory. A directory can have multiple nodes, and every node records the configuration that created it.

Projects are not part of schema v4. Passing a directory to CodeLima scopes the TUI to nodes bound to that directory or its descendants.

## Requirements

- macOS arm64, Linux amd64, or Linux arm64
- Lima 2.1.0 or a compatible newer Lima 2.x release
- Apple Virtualization.framework on macOS arm64, or QEMU with KVM on Linux
- `git`

Install Lima with `brew install lima`. CodeLima validates `limactl`, resolves the configured template, and lets Lima download its upstream guest image on first node creation.

Keep both `CODELIMA_HOME` and `LIMA_HOME` reasonably short. Lima derives Unix-domain socket paths beneath `LIMA_HOME`, and operating systems impose a small fixed path-length limit. `LIMA_HOME` must be on a filesystem that supports Unix sockets; network mounts such as 9p may store files successfully but fail during SSH/hostagent startup. Prefer paths such as `~/.codelima-v4` and `~/.lima`.

Each running node has visible Lima hostagent and VZ/QEMU processes. The
CodeLima daemon owns one long-lived `limactl watch --json` process for all
nodes, so the TUI's recurring refresh does not spawn `limactl list`. Stopping a
node removes its VM-driver and hostagent processes. Cloning a running node
briefly stops the source because Lima requires a stopped disk, creates a
stopped clone, and then restores the source to running.

## Install

Install the latest packaged release with Homebrew:

```sh
brew tap brianrackle/codelima
brew install codelima
```

The formula installs CodeLima, its bundled `libghostty-vt` library, `git`, and Lima. Release archives are published for macOS arm64, Linux amd64, and Linux arm64.

## Build

The repository manages its toolchain under `.tooling/<os>-<arch>`:

```sh
make init
make verify
```

`make build` writes `bin/<os>-<arch>/codelima` and refreshes `bin/codelima` as a convenience symlink. Other useful recipes are:

```sh
make fmt
make lint
make test
make test-race
make test-integration
make smoke
```

The Ghostty integration requires cgo. The Makefile enables it and uses the host `cc` when available, falling back to the managed Zig compiler installed by `make init` in minimal development sandboxes. Tests run serially by default to avoid filesystem-resource spikes on virtualized development hosts; override `GO_TEST_PARALLEL` or `GO_RACE_TEST_PARALLEL` after qualifying a host.

See `BUILD.md` for packaging and release details.

## Use a separate home

Schema v4 intentionally does not migrate Microsandbox-backed schema-v3 homes, schema-v2 homes, or legacy Lima layouts. To run old and new CodeLima builds side by side, give the new binary its own home:

```sh
CODELIMA_HOME="$HOME/.codelima-v4" ./bin/darwin-arm64/codelima .
```

or:

```sh
./bin/darwin-arm64/codelima --home "$HOME/.codelima-v4" .
```

`--home` must precede the command or directory argument. The old build can continue using `~/.codelima`; each home has its own daemon, metadata, and terminal sessions.

## Quick start

Inspect the global settings and repair/seed a fresh home:

```sh
codelima settings show
codelima doctor --repair
```

`small` is the protected implicit default configuration. It starts with:

- image `template:ubuntu`
- agent profile `codex-cli`
- environments `codex` and `claude-code`
- 1 vCPU
- 1024 MiB memory
- 10240 MiB disk

It is editable, but it cannot be renamed or deleted. Creating another configuration copies the current `small` configuration once; later `small` edits do not change that copy. Omitting `--configuration` when creating a node selects `small`.

CodeLima seeds four ready-to-use size configurations:

| Configuration | vCPUs | Memory | Disk |
| --- | ---: | ---: | ---: |
| `small` | 1 | 1 GiB | 10 GiB |
| `medium` | 4 | 8 GiB | 50 GiB |
| `large` | 6 | 16 GiB | 75 GiB |
| `xlarge` | 8 | 32 GiB | 100 GiB |

The configurations use the same initial image, agent profile, and `codex`/`claude-code` environments. `medium`, `large`, and `xlarge` are ordinary configurations that can be edited or deleted. Seed and repair add a missing size to an older schema-v4 home, preserve customized values, and do not restore a deleted optional size. Because `small` is required as the implicit default, an upgraded home restores it if it was deleted while retaining its customized values. Upgraded homes retire the former `default` record from live configuration lists; existing nodes that were created from it retain their frozen values and historical association.

The two built-in coding-agent environments install both CLIs in nodes created from `small`. The `codex-cli` agent profile selects Codex for validation and launch behavior; Claude Code remains available as `claude`.

```sh
codelima node create --slug codelima-dev --directory .
codelima node start codelima-dev
codelima shell codelima-dev
```

Inside the node, both agents are available:

```sh
codex --yolo
claude
```

Configuration values are frozen into the node at creation. Updating `small` affects future nodes only.

## Configurations

List and inspect configurations:

```sh
codelima configuration list
codelima configuration show small
codelima configuration show medium
```

Create a reusable recipe. Memory and disk accept MiB or GiB values:

```sh
codelima configuration create \
  --slug large-codex \
  --image template:ubuntu \
  --agent-profile codex-cli \
  --environment codex \
  --bootstrap-command 'apt-get update && apt-get install -y ripgrep' \
  --vcpus 4 \
  --memory 8GiB \
  --disk 40GiB
```

Update, clone, and delete:

```sh
codelima configuration update large-codex --memory 12GiB
codelima configuration clone large-codex --slug large-codex-copy
codelima configuration delete large-codex-copy
```

A configuration referenced by any live node cannot be deleted. Repeated `--environment` and `--bootstrap-command` flags preserve order. Use `--clear-environments` or `--clear-bootstrap-commands` to clear either list.

## Environments

Environments are reusable bootstrap bundles:

```sh
codelima environment create \
  --slug web-tools \
  --bootstrap-command 'apt-get update' \
  --bootstrap-command 'apt-get install -y nodejs npm'

codelima environment list
codelima environment show web-tools
codelima environment update web-tools --bootstrap-command 'npm install -g pnpm'
codelima environment delete web-tools
```

The built-in `codex` and `claude-code` environments are seeded by a mutating command, `doctor --repair`, or TUI startup. An environment referenced by a configuration cannot be deleted.

## Nodes and directories

Create a node in the current directory with the implicit `small` configuration:

```sh
codelima node create --slug api-dev
```

Select another configuration or directory explicitly:

```sh
codelima node create \
  --slug api-large \
  --configuration large-codex \
  --directory /Users/me/src/api
```

Multiple nodes may be bound to the same directory. Node slugs are globally unique within one `CODELIMA_HOME` so every command can address a node unambiguously.

Lifecycle and inspection commands:

```sh
codelima node list
codelima node show api-dev
codelima node start api-dev
codelima node status api-dev
codelima node logs api-dev
codelima node stop api-dev
codelima node delete api-dev
```

Cloning requires a new slug and keeps the source node's directory, configuration association, frozen resources, environments, bootstrap state, and workspace mode:

```sh
codelima node clone api-dev --slug api-experiment
```

Workspace modes:

- `copy` seeds a guest-local copy of the directory on first start.
- `mounted` mounts the host directory read/write at the same absolute path in the guest.

New nodes default to `mounted`, so host and guest edits are immediately shared. Select isolated copy mode explicitly when needed:

```sh
codelima node create --slug api-isolated --workspace-mode copy
```

## TUI

Open all nodes:

```sh
codelima
```

Open only nodes bound to the current directory or a descendant:

```sh
codelima .
```

The left pane is a flat list of nodes with configuration, relative directory, and runtime status. Configuration and environment management are global actions; there are no project rows.

Important bindings:

- `Up` / `Down`: select a node
- `i`: toggle info and terminal views
- `Option+Backtick` or `F6`: toggle tree and terminal focus
- `Option+t`: open another guest terminal tab for the selected node
- `Option+Shift+t`: open a host terminal tab rooted at the node directory
- `Option+Left` / `Option+Right`: switch tabs
- `Option+w`: close the active tab
- `n`: create a node
- `a`: manage configurations
- `g`: manage environments
- `s`: start or stop the selected node
- `c`: clone the selected node
- `d`: delete the selected node
- `q`: quit

Inside a form, `Tab`, `Up`, and `Down` move between fields. `Left` and `Right` move the cursor within editable values; on a selector field, `Right` or `Enter` opens its choices. `Ctrl+s` submits and `Esc` cancels.

When the initially selected node is running, the TUI opens its existing or newly created guest tab in the right pane and highlights `Terminal` by default while keeping keyboard focus in the node list. A stopped initial node remains on `Info` and does not start a guest shell. After startup, `i` is a sticky explicit choice: moving between nodes does not replace the selected pane mode.

The host and guest shells are both tabs of the node target. Host mode is indicated by the red top bar. Host tabs resolve their working directory from stored node metadata, so they remain available when the node is stopped or Lima is temporarily unavailable.

Quitting the TUI detaches from daemon-owned tabs instead of closing them. Reopening the same CodeLima home reconnects the surviving tabs in their original creation order. Multiple path-scoped TUI processes may use the same home: a window refresh preserves daemon tabs owned by nodes outside that window's directory scope, so closing and reopening disjoint windows does not delete one another's tabs. Live daemon update also preserves tab order and rebuilds wrapped terminal content at its captured geometry.

Multiline paste is delivered to terminal applications as one bracketed paste rather than a sequence of simulated key presses. Newlines remain in the input buffer and do not execute pasted commands; press Enter explicitly when the pasted text is ready.

Guest shells retain normal terminal line editing: arrow keys and Readline
controls operate inside the guest, while `Ctrl+c` interrupts the active guest
job without closing its CodeLima tab.

Expanding a terminal from the split pane to full width requests a redraw with a
terminal resize signal, not synthetic shell input. Switching focus with
`Option+Backtick` or `F6` therefore does not type `^L` into a shell or clear its
existing terminal history.

Ordinary terminal input is delivered through an ordered background queue, so typing remains responsive while the daemon is serving terminal snapshots. The TUI redraws typed input when the daemon publishes the resulting fresh snapshot instead of repainting the previous screen after every key. Terminal snapshots are event-driven: idle tabs issue no recurring snapshot requests, and hidden tabs defer full-grid transfer until they become visible. Active output remains coalesced to at most 20 snapshots per second.

While the full-screen TUI is open, CodeLima writes service, Ghostty, and Lima
diagnostics to `$CODELIMA_HOME/_logs/codelima.log` instead of terminal stderr,
so background warnings cannot overwrite the screen. The file rotates at about
5 MiB and keeps one `codelima.log.1` generation. Put `--log-level debug` before
the directory argument to retain debug records when investigating a session:

```sh
codelima --log-level debug .
```

## Local development servers

The daemon discovers listening guest TCP ports and routes HTTP and WebSocket traffic dynamically:

```text
http://localhost:{guest-port}
http://127.0.0.1:{guest-port}
http://{node-slug}.localhost:{guest-port}
```

For a node named `api-dev` serving on guest port 8080:

```sh
curl http://localhost:8080
curl http://127.0.0.1:8080
curl http://api-dev.localhost:8080
```

No fixed list such as 3000/5173/8080 is preconfigured. The first active node discovered on a port claims the generic `localhost` and `127.0.0.1` URLs while it remains listening. When it stops, the daemon assigns the earliest remaining claimant during its one-second reconciliation loop. A temporary conflict with another host process is also retried every second; claims are in-memory and are not persisted.

Two nodes can use the same guest port because `{node-slug}.localhost` always selects that specific node, regardless of which node owns the generic URL. The server may bind IPv4 or IPv6 guest loopback (`127.0.0.1`, `::1`, or `localhost`); traffic is tunneled through one persistent Go SSH connection built from Lima's private per-instance SSH configuration. `codelima daemon snapshot` reports the generic `default_node` for every active port; it remains empty while a host bind is conflicted and no claim has succeeded.

Raw TCP services can still use explicit `--port HOST:GUEST` mappings at node creation. Explicit host ports must be unique among simultaneously running nodes.

## Daemon and terminal automation

The daemon owns terminal runtimes, dynamic forwarding, input ownership, and live-update handoff:

```sh
codelima daemon start
codelima daemon status
codelima daemon snapshot
codelima daemon stop
```

The TUI starts or connects to the daemon automatically according to `_config/settings.yaml`.

After rebuilding a development binary while a daemon is already running, apply it with `./bin/codelima daemon update`. With no path argument, the command sends the exact binary being invoked and can bridge from the prior handoff-capable daemon protocol while preserving PTYs. The handoff uses framed Unix streams and SCM_RIGHTS on both macOS and Linux. A legacy macOS daemon that reports the old unsupported `unixpacket` transport is restarted by the update command; VMs remain running and saved tabs respawn, but those terminal processes restart once because the legacy daemon cannot transfer their descriptors. Shutdown waits on the authoritative daemon lock rather than stale socket pathnames, allows time based on the number of terminals, and terminates only the still-matching daemon identity if graceful teardown gets stuck. TUI autostart applies the same recovery when an earlier daemon has closed its socket but still owns the lock. Other handoff failures still roll back to the old daemon. Ordinary clients remain exact-version only, so a stale daemon cannot silently accept newer input such as semantic paste. An already-open TUI must currently be quit and reopened after the update so it reconnects to the replacement daemon; the TUI reports this after commit, stops reading the permanently closed event stream, and remains idle instead of retrying `EOF`. Asynchronous input failure is shown instead of silently dropping text.

An interactive TUI claims daemon input ownership when it connects, making any older client observe-only, and its authenticated connection remains open while idle. When multiple TUI windows remain open, focusing a window silently reclaims ownership before its next terminal action and makes the previously focused window observe-only. Routine window switching does not show an ownership warning and requires neither a manual `terminal takeover` nor a daemon restart, including after an idle interval.

On macOS, the daemon also protects mounted VirtioFS workspaces from system file-table exhaustion. It samples the host-wide `kern.num_files` against the host-wide `kern.maxfiles` every two seconds and, at 20% by default, asks running mounted nodes to release clean dentry/inode caches. Every attempt has a 30-second cooldown. The operation does not run `sync`, does not discard dirty data, and does not interrupt active file handles; the tradeoff is temporarily colder path-lookup caches.

The daemon-only settings are:

```yaml
daemon:
  autostart: true
  restore: respawn
  virtiofs_reclaim: true
  virtiofs_reclaim_threshold_percent: 20
```

The threshold accepts 1 through 95. Set `virtiofs_reclaim: false` to disable the macOS workaround. `codelima daemon snapshot` reports whether the integration is supported, current host usage, the last reclaim result, and the next eligible attempt.

`_daemon/session.json` contains only terminal restoration intent. With `restore: respawn`, CodeLima preserves an unsupported or malformed file beside that path, writes a fresh current-version session, logs the recovery, and continues starting the daemon. With `restore: forget`, it replaces old session state directly. This recovery never changes nodes, VM disks, or workspace files.

Automation can open guest or host tabs for a node target:

```sh
NODE_ID="$(codelima --json node show api-dev | sed -n 's/.*"id": *"\([^"]*\)".*/\1/p' | head -1)"
codelima --json terminal open "node:$NODE_ID" --kind node-shell
codelima --json terminal open "node:$NODE_ID" --kind node-host-shell
codelima terminal list
```

Use `terminal read`, `terminal send`, `terminal close`, and `terminal takeover` for snapshot reads and explicit input ownership. Client and daemon versions must match exactly.

## Metadata layout

Schema v3 uses:

```text
CODELIMA_HOME/
├── _config/
│   ├── schema.version
│   ├── settings.yaml
│   └── agent-profiles/
├── _daemon/
├── _index/
│   ├── configurations/by-slug/
│   ├── environments/by-slug/
│   └── nodes/by-instance/
├── _locks/
├── configurations/<id>/configuration.yaml
├── environments/<id>/environment.yaml
└── nodes/<id>/
    ├── node.yaml
    ├── bootstrap.yaml
    ├── sandbox-instance.ref
    └── events.jsonl
```

There is no `projects/` directory. A home with schema version 2 is rejected with instructions to choose a fresh home; automatic migration is intentionally unsupported.

## Command summary

```text
codelima [--home PATH] [--json] [--log-level LEVEL] [PATH]
codelima doctor [--repair]
codelima settings show
codelima environment create|list|show|update|delete
codelima configuration create|list|show|update|delete|clone
codelima node create|list|cleanup-incomplete|show|start|stop|clone|delete|status|logs|shell
codelima shell <node> [-- command...]
codelima daemon run|start|stop|status|snapshot|update
codelima terminal open|close|list|read|send|takeover
```

Use global flags before the command group, for example `codelima --home ~/.codelima-msb --json node list`.
