# CodeLima Specification

Status: schema v3 implementation contract

## Purpose

CodeLima manages directory-bound coding VMs through Microsandbox. It exposes a CLI, a shell-first TUI, and a private daemon API while keeping reusable VM policy independent from host directories.

## Non-goals

- Project hierarchy, project lineage, workspace snapshots, and patch proposals
- Automatic migration from Lima or schema-v2 metadata
- An `msb` CLI integration or fallback
- Applying configuration edits retroactively to existing nodes
- Container or alternate VM providers in schema v3

## Runtime requirements

- Go is the implementation language and module/toolchain manager.
- The official Microsandbox Go SDK is pinned to `v0.6.6`.
- The SDK ensures its matching Microsandbox runtime support files are installed before dependency validation.
- Supported release targets are darwin/arm64, linux/amd64, and linux/arm64.
- Microsandbox is the only runtime and provider.

## Object model

### Configuration

A `Configuration` is a reusable, directory-independent VM recipe:

- `id`: immutable unique identifier
- `slug`: unique lowercase user-facing identifier
- `image`: OCI image
- `agent_profile_name`: installed agent validation/launch profile
- `environments`: ordered environment slugs
- `bootstrap_commands`: ordered direct guest bootstrap commands
- `vcpus`: positive whole CPU count
- `memory_mib`: positive memory size
- `disk_mib`: positive OCI upper-layer size
- creation, update, and optional deletion timestamps

Each home has a reserved `default` configuration. It is editable but cannot be renamed or deleted. Its initial values are:

- image `ghcr.io/superradcompany/debian-systemd:12`
- agent profile `codex-cli`
- environments `codex` and `claude-code`
- 2 vCPUs
- 4096 MiB memory
- 20480 MiB disk
- no direct bootstrap commands

Creating a configuration copies the current default once. Cloning copies the selected source once. Configurations do not inherit later changes.

Deletion is refused while any live node references the configuration.

### Environment

An `Environment` is a reusable ordered list of guest bootstrap commands:

- `id`
- unique lowercase `slug`
- `bootstrap_commands`
- creation, update, and optional deletion timestamps

Configurations reference environments by slug. Resolution preserves configuration environment order and command order, followed by direct configuration bootstrap commands. Node creation stores the resolved command sequence. Deletion is refused while a live configuration references the environment.

### Node

A `Node` is a Microsandbox VM bound to one canonical host directory:

- immutable `id`
- globally unique lowercase `slug`
- `configuration_id` and transient display `configuration_slug`
- canonical absolute `directory_path`
- optional `parent_node_id` for clones
- runtime/provider and sandbox identity
- frozen image, vCPUs, memory, disk, environments, bootstrap commands, and agent profile
- optional explicit published ports and network policy
- workspace mode and guest/mount paths
- durable bootstrap and lifecycle state
- creation, update, and optional deletion timestamps

Multiple nodes may bind the same directory. Directory paths must exist, be directories, and remain outside `CODELIMA_HOME`. An omitted creation directory means the process current directory.

Configuration-owned fields cannot be overridden by node creation. This makes the node a frozen record of the configuration that created it.

Node cloning requires a new slug and retains the source directory, configuration ID, frozen effective values, bootstrap completion, and workspace mode. The cloned VM is persisted stopped/created.

## Workspace modes

- `copy`: no host mount; the directory tree is recursively seeded into the guest on first successful start.
- `mounted`: one writable mount exposes the host directory at the same absolute path in the guest.

New nodes default to `mounted`. `copy` remains an explicit creation option. Existing persisted nodes retain their recorded mode; blank workspace metadata from versions that predate explicit modes continues to mean `copy`.

Recursive copy creates directories, copies regular files through the SDK, and recreates symbolic links through guest exec. Unsupported special files fail explicitly.

## CLI contract

Global form:

```text
codelima [--home PATH] [--json] [--log-level LEVEL] [PATH]
codelima [global flags] <group> <command> [flags]
```

Global flags precede a command or path.

Command groups:

```text
doctor [--repair]
settings show
environment create|list|show|update|delete
configuration create|list|show|update|delete|clone
node create|list|cleanup-incomplete|show|start|stop|clone|delete|status|logs|shell
shell <node> [-- command...]
daemon run|start|stop|status|snapshot|update
terminal open|close|list|read|send|takeover
```

There is no `project` command. `config show` is replaced by `settings show` so “configuration” unambiguously means the reusable VM recipe.

`node create` requires `--slug`; `--configuration` defaults to `default`; `--directory` defaults to the current directory; and `--workspace-mode` defaults to `mounted`. It may explicitly select `copy`, explicit `--port HOST:GUEST`, and a network policy. It cannot override image, agent, environments, bootstrap, or VM resources.

`node clone <source> --slug <new>` requires the new slug and does not accept configuration-owned overrides.

`codelima` opens the TUI for all nodes. `codelima PATH` opens the TUI with nodes whose directory is PATH or a true descendant. Directory containment is path-aware, not a string-prefix match.

Human output uses tables for lists and YAML for records. `--json` returns a stable `{ok,data}` or `{ok,error}` envelope.

## TUI contract

The left pane is a flat node list. Each row shows:

- node slug
- configuration slug
- directory relative to the active path scope when scoped
- runtime/lifecycle status

There are no project rows. Global actions manage configurations and environments. Node actions create, start, stop, clone, and delete nodes.

The directory field in the create dialog is blank with the canonical current directory displayed as a muted placeholder. Configuration defaults to `default` and workspace mode defaults to `mounted`. Clone requires an explicit blank-by-default slug and retains the source directory/configuration.

Guest and host terminals are ordinary tabs of the selected node target. `Option+t` opens a fresh guest tab; `Option+Shift+t` opens a fresh host tab rooted in the node directory. `Option+Left`, `Option+Right`, and `Option+w` switch and close either kind uniformly. The red top bar follows the active node-host tab. Terminal creation reads durable node metadata without live runtime reconciliation, so a host tab remains available while the node is stopped or Microsandbox is unavailable.

## Microsandbox SDK mapping

Sandbox creation uses typed SDK options:

- image → `WithImage`
- vCPUs → `WithCPUs`
- memory MiB → `WithMemory`
- disk MiB → `WithOCIUpperSize`
- mounted directory → SDK mount
- explicit ports → SDK port configuration
- network policy → SDK policy configuration

Lifecycle, list, copy, exec, attach, and clone/snapshot behavior also use SDK APIs. The SDK `EnsureInstalled` API may download its pinned runtime support bundle; no production path may discover or execute the `msb` CLI.

## Node lifecycle

Creation validates the home, configuration, directory, agent profile, ports, network policy, and unique slug/sandbox identity before creating the VM. Effective configuration and bootstrap state are persisted only after SDK creation succeeds. A failed persistence removes the newly created sandbox.

Start reconciles live runtime state, starts the sandbox if needed, seeds copy-mode workspaces once, runs frozen agent installation and bootstrap commands once, validates the agent, and persists running state. Failed bootstrap persists failed lifecycle state and a failure event.

Stop is idempotent against an already-stopped sandbox. Delete marks termination in progress, tears down the sandbox, then soft-deletes node metadata. Incomplete metadata cleanup tears down a referenced live sandbox before removing its directory.

Runtime list/status truth is reconciled from one SDK list call and is not persisted by ordinary reads.

## Dynamic forwarding

The daemon discovers guest TCP listeners for running nodes and listens on host loopback at the same port where available. HTTP and WebSocket traffic is routed by request host:

```text
localhost:{guest-port}
127.0.0.1:{guest-port}
{node-slug}.localhost:{guest-port}
```

The first active node on a port serves both generic host forms. Two nodes may use the same guest port; the node hostname selects the SDK SSH tunnel explicitly. Guest services may bind loopback only. Routes disappear when a node stops and recover after daemon restart. Bind conflicts are retried without crashing the daemon.

Explicit published ports remain available for non-HTTP raw TCP workflows and are frozen at node creation.

## Terminal and daemon contract

Terminal targets are `node:<id>`. Supported schema-v3 terminal kinds are:

- `node-shell`: guest interactive shell in the node workspace
- `node-host-shell`: host interactive shell in the node directory

The daemon owns PTYs, Ghostty terminal state, forwarding, and input ownership. Its private Unix-socket protocol uses newline-delimited JSON, exact binary-version negotiation, peer-credential validation, bounded frames, and typed errors. Semantic paste is protocol 3; a stale protocol-2 daemon must reject ordinary clients rather than accept an event type it cannot interpret. Daemon lifecycle management is the sole compatibility exception: update and startup recovery may authenticate with a handoff-capable persisted daemon identity only to replace or stop that daemon, and a missing update path always resolves to the invoking client binary. An interactive TUI that connects as observe-only immediately requests `input.takeover`; older clients receive revocation and remain observe-only. When an older TUI window regains host focus, it requests takeover again before processing the next user terminal action. Paste keys are buffered into bounded semantic paste requests; the daemon terminal actor re-creates bracketed-paste boundaries around each payload while preserving LF newlines. Handshake and event-subscription phases have read deadlines; authenticated request connections and subscribed event connections remain usable across arbitrary idle periods.

Terminal sessions persist enough launch state for daemon restart policy. With `restore: forget`, persisted terminal state is replaced without parsing. With `restore: respawn`, unsupported or malformed session state is quarantined beside `_daemon/session.json`, an empty current-version session is written, and daemon startup continues; node and VM state are unaffected. Handoff version 3 transfers PTY descriptors via SCM_RIGHTS over an authenticated Unix stream with four-byte length-prefixed frames and rolls back if import fails. Control frames reject descriptors, and descriptor batches reject duplicate or unknown terminal IDs. A new importer may consume the previous Linux `unixpacket` version 2 once. The exact legacy-Darwin unsupported-transport error falls back to a daemon restart that preserves VMs and respawns saved tabs. Shutdown persists session intent first, closes terminals concurrently, and treats release of `_locks/daemon.lock` as the authoritative readiness signal. Grace is sized for terminal count; after it expires, only the still-matching daemon identity may be terminated. Startup applies the same recovery to a compatible socket-closed daemon that still owns the lock. Protocol, session, and handoff format revisions are versioned explicitly.

On macOS, the daemon samples the host-wide system file table every two seconds. When host-wide `kern.num_files` reaches the configured percentage of host-wide `kern.maxfiles`, it runs `echo 2 > /proc/sys/vm/drop_caches` in every running mounted node and compares host usage before and after. The default threshold is 20%; every attempt has a 30-second cooldown, with no separate ineffective-action backoff. No `sync` or page-cache drop is permitted. Non-macOS snapshots report this integration as unsupported.

## Storage contract

Schema version is `3`:

```text
CODELIMA_HOME/
  _config/schema.version
  _config/settings.yaml
  _config/agent-profiles/<name>.yaml
  _daemon/
  _index/configurations/by-slug/<slug>
  _index/environments/by-slug/<slug>
  _index/nodes/by-instance/<sandbox>
  _locks/
  configurations/<id>/configuration.yaml
  environments/<id>/environment.yaml
  nodes/<id>/node.yaml
  nodes/<id>/bootstrap.yaml
  nodes/<id>/sandbox-instance.ref
  nodes/<id>/events.jsonl
```

Schema v3 does not create a `projects/` directory. Schema version 2 and recognized Lima homes fail with `PreconditionFailed` and instruct the user to choose a fresh `--home`/`CODELIMA_HOME`. Other non-empty unrecognized homes are rejected.

Settings contain daemon behavior only, including macOS VirtioFS reclaim enablement and its 1–95% host file-table threshold. Reusable VM defaults live in the editable `default` configuration.

## Error categories

- `InvalidArgument`: malformed command, slug, path, size, port, or policy
- `NotFound`: missing configuration, environment, or node
- `PreconditionFailed`: protected/default mutation, live reference, schema mismatch, invalid lifecycle transition, or unavailable directory
- `DependencyUnavailable`: missing/incompatible Microsandbox runtime or daemon
- `MetadataCorruption`: malformed or inconsistent persisted metadata
- `UnsupportedFeature`: reserved runtime/provider values
- `Internal`: unexpected implementation failure

## Verification requirements

Completion requires:

- unit tests for configuration protection/copy semantics, frozen nodes, directory containment, clone semantics, schema rejection, and typed SDK resources
- CLI tests proving project/config groups are absent and schema-v3 commands work
- TUI tests for flat nodes, configuration labels, path-relative directories, configuration selection, and node-scoped guest/host tabs
- daemon/integration tests for lifecycle, exact protocol, persistence, live-update commit/rollback, and terminal continuity
- race tests and lint/format/gopls diagnostics
- every applicable manual flow in `QA.md`
