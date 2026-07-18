# CodeLima

CodeLima is a Go CLI and shell-first TUI for directory-bound coding VMs. It uses Microsandbox as its sole runtime and the official Microsandbox Go SDK as its sole integration; CodeLima never shells out to the `msb` CLI and has no CLI fallback.

The model has three user-facing objects:

- A configuration is a reusable, directory-independent VM recipe: image, agent profile, environments, direct bootstrap commands, vCPUs, memory, and disk.
- An environment is a reusable ordered list of bootstrap commands that configurations can reference.
- A node is a VM bound to one host directory. A directory can have multiple nodes, and every node records the configuration that created it.

Projects are not part of schema v3. Passing a directory to CodeLima scopes the TUI to nodes bound to that directory or its descendants.

## Requirements

- macOS arm64, Linux amd64, or Linux arm64
- A host capable of running Microsandbox (including its virtualization requirements)
- `git`

The Go module pins the official SDK at `v0.6.6`. On its first runtime dependency check, the SDK ensures its matching `msb` and `libkrunfw` support files are installed under `~/.microsandbox`; an initial download may therefore require network access. CodeLima does not execute that `msb` binary and has no CLI fallback.

Keep both `CODELIMA_HOME` and `MSB_HOME` reasonably short. Microsandbox derives Unix-domain socket paths beneath them, and operating systems impose a small fixed path-length limit. Prefer paths such as `~/.codelima-v3` and `~/.msb` over deeply nested directories.

## Install

Install the latest packaged release with Homebrew:

```sh
brew tap brianrackle/codelima
brew install codelima
```

The formula installs CodeLima, its bundled `libghostty-vt` library, and `git`. Microsandbox remains a host prerequisite and installs its matching runtime support files during CodeLima's first dependency check. Release archives are published for macOS arm64, Linux amd64, and Linux arm64.

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

See `BUILD.md` for packaging and release details.

## Use a separate home

Schema v3 intentionally does not migrate old Lima-backed or schema-v2 homes. To run old and new CodeLima builds side by side, give the new binary its own home:

```sh
CODELIMA_HOME="$HOME/.codelima-msb" ./bin/darwin-arm64/codelima .
```

or:

```sh
./bin/darwin-arm64/codelima --home "$HOME/.codelima-msb" .
```

`--home` must precede the command or directory argument. The old build can continue using `~/.codelima`; each home has its own daemon, metadata, and terminal sessions.

## Quick start

Inspect the global settings and repair/seed a fresh home:

```sh
codelima settings show
codelima doctor --repair
```

The reserved `default` configuration starts with:

- image `ghcr.io/superradcompany/debian-systemd:12`
- agent profile `codex-cli`
- environments `codex` and `claude-code`
- 2 vCPUs
- 4096 MiB memory
- 20480 MiB disk

It is editable, but it cannot be renamed or deleted. Creating another configuration copies the current default once; later default edits do not change that copy.

The two built-in coding-agent environments install both CLIs in nodes created from the default configuration. The `codex-cli` agent profile selects Codex for validation and launch behavior; Claude Code remains available as `claude`.

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

Configuration values are frozen into the node at creation. Updating `default` affects future nodes only.

## Configurations

List and inspect configurations:

```sh
codelima configuration list
codelima configuration show default
```

Create a reusable recipe. Memory and disk accept MiB or GiB values:

```sh
codelima configuration create \
  --slug large-codex \
  --image ghcr.io/superradcompany/debian-systemd:12 \
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

Create a node in the current directory with the default configuration:

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

The host and guest shells are both tabs of the node target. Host mode is indicated by the red top bar. Host tabs resolve their working directory from stored node metadata, so they remain available when the node is stopped or Microsandbox is temporarily unavailable.

## Local development servers

The daemon discovers listening guest TCP ports and routes HTTP and WebSocket traffic dynamically:

```text
http://{node-slug}.localhost:{guest-port}
```

For a node named `api-dev` serving on guest port 8080:

```sh
curl http://api-dev.localhost:8080
```

No fixed list such as 3000/5173/8080 is preconfigured. Two nodes can use the same guest port because the hostname selects the node. The server may bind guest loopback; traffic is tunneled through the daemon's Microsandbox SDK SSH helper.

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

An interactive TUI claims daemon input ownership when it connects, making any older client observe-only, and its authenticated connection remains open while idle. Returning to that TUI and opening a terminal therefore requires neither a manual `terminal takeover` nor a daemon restart.

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
