# CodeLima Specification

Status: schema v4 implementation contract

## Purpose

CodeLima manages directory-bound coding VMs through Lima 2.x. It exposes a CLI, a shell-first TUI, and a private daemon API while keeping reusable VM policy independent from host directories.

## Non-goals

- Project hierarchy, project lineage, workspace snapshots, and patch proposals
- Automatic migration from schema-v3, schema-v2, or legacy Lima metadata
- Embedding a VMM SDK or supporting a second runtime
- Applying configuration edits retroactively to existing nodes
- Container or alternate VM providers in schema v4

## Runtime requirements

- Go is the implementation language and module/toolchain manager.
- Lima 2.1.0 is the minimum; compatible newer Lima 2.x releases are accepted.
- `limactl` is the supported runtime API boundary.
- Supported release targets are darwin/arm64, linux/amd64, and linux/arm64.
- Lima is the only runtime and provider.

## Object model

### Configuration

A `Configuration` is a reusable, directory-independent VM recipe:

- `id`: immutable unique identifier
- `slug`: unique lowercase user-facing identifier
- `image`: Lima template locator
- `agent_profile_name`: installed agent validation/launch profile
- `environments`: ordered environment slugs
- `bootstrap_commands`: ordered direct guest bootstrap commands
- `vcpus`: positive whole CPU count
- `memory_mib`: positive memory size
- `disk_mib`: positive VM disk size
- creation, update, and optional deletion timestamps

Each home seeds five built-in size configurations. They share image `template:ubuntu`, agent profile `codex-cli`, environments `codex` and `claude-code`, and no direct bootstrap commands. They differ only in resources:

| slug | vCPUs | memory MiB | disk MiB |
| --- | --- | --- | --- |
| `xsmall` | 1 | 1024 | 10240 |
| `small` | 2 | 4096 | 25600 |
| `medium` | 4 | 8192 | 51200 |
| `large` | 6 | 16384 | 76800 |
| `xlarge` | 8 | 32768 | 102400 |

`small` is the implicit default: it supplies the values for node creation and configuration creation, it is editable, and it cannot be renamed or deleted. The other four are ordinary editable and deletable built-ins. There is no live `default` configuration; the slug `default` is permanently reserved and cannot be created or renamed to. An upgraded home soft-deletes its former `default` record instead of removing it, so existing nodes still resolve it by ID and keep displaying their historical association with unchanged frozen values.

Creating a configuration copies `small` once. Cloning copies the selected source once. Configurations do not inherit later changes.

Deletion is refused while any live node references the configuration.

### Environment

An `Environment` is a reusable ordered list of guest bootstrap commands:

- `id`
- unique lowercase `slug`
- `bootstrap_commands`
- creation, update, and optional deletion timestamps

Configurations reference environments by slug. Resolution preserves configuration environment order and command order, followed by direct configuration bootstrap commands. Node creation stores the resolved command sequence. Deletion is refused while a live configuration references the environment.

### Node

A `Node` is a Lima VM bound to one canonical host directory:

- immutable `id`
- globally unique lowercase `slug`
- `configuration_id` and transient display `configuration_slug`
- canonical absolute `directory_path`
- optional `parent_node_id` for clones
- runtime/provider and sandbox identity
- frozen image, vCPUs, memory, disk, environments, bootstrap commands, and agent profile
- optional explicit published ports
- workspace mode and guest/mount paths
- durable bootstrap and lifecycle state
- creation, update, and optional deletion timestamps

Multiple nodes may bind the same directory. Directory paths must exist, be directories, and remain outside `CODELIMA_HOME`. An omitted creation directory means the process current directory.

Configuration-owned fields cannot be overridden by node creation. This makes the node a frozen record of the configuration that created it.

Node cloning requires a new slug and retains the source directory, configuration ID, frozen effective values, bootstrap completion, and workspace mode. If the source is running, CodeLima stops it before cloning and restores it afterward. The cloned VM is persisted stopped/created.

## Workspace modes

- `copy`: no host mount; the directory tree is recursively seeded into the guest on first successful start.
- `mounted`: one writable mount exposes the host directory at the same absolute path in the guest.

New nodes default to `mounted`. `copy` remains an explicit creation option. Existing persisted nodes retain their recorded mode; blank workspace metadata from versions that predate explicit modes continues to mean `copy`.

Recursive copy uses `limactl copy`; workspace preparation and bootstrap execute through the Lima shell boundary.

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

`node create` requires `--slug`; `--configuration` defaults to `small`; `--directory` defaults to the current directory; and `--workspace-mode` defaults to `mounted`. It may explicitly select `copy` and explicit `--port HOST:GUEST`. It cannot override image, agent, environments, bootstrap, or VM resources.

`node clone <source> --slug <new>` requires the new slug and does not accept configuration-owned overrides.

`codelima` opens the TUI for all nodes. `codelima PATH` opens the TUI with nodes whose directory is PATH or a true descendant. Directory containment is path-aware, not a string-prefix match.

Human output uses tables for lists and YAML for records. `--json` returns a stable `{ok,data}` or `{ok,error}` envelope.

## TUI contract

The left pane is a flat node list. Each fixed-height node block shows:

- node slug
- configuration slug
- directory relative to the active path scope when scoped
- runtime/lifecycle status
- aggregate guest CPU usage, normalized to `0..100%` and refreshed once per second
- guest memory used/total, calculated as `MemTotal - MemAvailable`
- guest root-filesystem disk used/total

There are no project rows. Global actions manage configurations and environments. Node actions create, start, stop, clone, and delete nodes.

The directory field in the create dialog is blank with the canonical current directory displayed as a muted placeholder. Configuration defaults to `small` and workspace mode defaults to `mounted`. Clone requires an explicit blank-by-default slug and retains the source directory/configuration.

Guest and host terminals are ordinary tabs of the selected node target. `Option+t` opens a fresh guest tab; `Option+Shift+t` opens a fresh host tab rooted in the node directory. `Option+Left`, `Option+Right`, and `Option+w` switch and close either kind uniformly. The red top bar follows the active node-host tab. Terminal creation reads durable node metadata without live runtime reconciliation, so a host tab remains available while the node is stopped or Lima is unavailable.

## Lima runtime mapping

Node creation resolves `image` with `limactl template copy --fill`, parses the
YAML structurally, and renders:

- image → preserved distribution image list from the resolved template
- vCPUs, memory MiB, and disk MiB → exact Lima VM resource fields
- mounted directory → exactly one writable Lima mount; copy mode → no mount
- explicit ports → static Lima port rules followed by ignore-all
- macOS arm64 → VZ with VirtioFS
- Linux amd64/arm64 → QEMU/KVM with 9p
- containerd, audio, video, automatic package upgrades, and inherited mounts → disabled

The private rendered template must pass `limactl validate` before `limactl
create`. Lifecycle, list, copy, shell, and clone use Lima commands. Lima logs in
as an unprivileged distribution user, so noninteractive CodeLima guest commands
are wrapped once with `sudo -H --` to preserve the existing root contract.

Runtime commands that still resolve to the built-in definitions execute as argv
without a host shell, so host shell profile output can never corrupt
machine-parsed `limactl` output. A runtime command customized in settings or
node metadata executes through `sh -lc`, which is the transport its template
placeholders and shell syntax are written against.

## Node lifecycle

Creation validates the home, configuration, directory, agent profile, ports, and unique slug/sandbox identity before creating the VM. `sandbox.ref` and the exact instance index are persisted before runtime creation so interrupted rollback remains recoverable. Effective node/bootstrap metadata is persisted after Lima creation succeeds. A failed operation removes only the matching incomplete instance and metadata when teardown succeeds.

Start reconciles live runtime state, starts the sandbox if needed, seeds copy-mode workspaces once, runs frozen agent installation and bootstrap commands once, validates the agent as Lima's unprivileged login user, and persists running state. Frozen custom bootstrap state remains authoritative. As a narrow repair exception, a command sequence or agent-profile validator that exactly matches a known defective former built-in definition is upgraded to the current built-in definition on start, recorded as `node.bootstrap.migrated`, and rerun before completion. Failed bootstrap persists failed lifecycle state and a failure event.

Stop is idempotent against an already-stopped instance. Delete marks termination in progress, tears down the instance, then soft-deletes node metadata. Incomplete metadata cleanup tears down a referenced live instance before removing its directory.

Direct CLI reads reconcile from `limactl list --json`. The daemon seeds one
list and then owns `limactl watch --json`; recurring TUI and forwarding reads
use the observation cache and do not spawn a recurring list process. Runtime
truth is not persisted by ordinary reads. Once per second, the daemon's
existing persistent SSH peer for each running node reads aggregate Linux CPU
counters, memory totals, and root-filesystem disk usage with listener discovery.
Consecutive CPU samples produce normalized `0..100%` guest utilization; the
first or an invalid CPU sample is unavailable. Memory is `MemTotal -
MemAvailable`, while disk usage is taken from `df -Pk /` and does not represent
the separately mounted host workspace. This telemetry is attached only to
daemon `node.list` responses.

## Dynamic forwarding

The daemon discovers guest TCP listeners for running nodes and listens on host loopback at the same port where available. HTTP and WebSocket traffic is routed by request host:

```text
localhost:{guest-port}
127.0.0.1:{guest-port}
{node-slug}.localhost:{guest-port}
```

The first active node on a port normally serves both generic host forms. Two nodes may use the same guest port; the node hostname selects the persistent Lima SSH tunnel explicitly. Port 1455, Codex CLI's browser-login callback, is the narrow generic-claim exception: the newest discovered listener owns `localhost:1455` and `127.0.0.1:1455`, and the next-newest active route takes over when it disappears. Guest services may bind loopback only. Forwarding state degrades before it disappears: a transient discovery or transport failure keeps the host listener bound and answering a retryable 502, and routes are removed only on a confirmed node stop or delete. Routes and generic claims are reconstructed from discovery after a daemon restart. Bind conflicts are retried without crashing the daemon. The full contract is `plans/DYNAMIC_PORT_FORWARDING_SPEC.md`.

Explicit published ports remain available for non-HTTP raw TCP workflows and are frozen at node creation.

## Terminal and daemon contract

Terminal targets are `node:<id>`. Supported schema-v4 terminal kinds are:

- `node-shell`: guest interactive shell in the node workspace
- `node-host-shell`: host interactive shell in the node directory

The daemon owns PTYs, Ghostty terminal state, tab order, forwarding, and input ownership. Its private Unix-socket protocol uses newline-delimited JSON, exact binary-version negotiation, peer-credential validation, bounded frames, and typed errors. Semantic paste was introduced in protocol 3; daemon-owned `terminal.move` tab reordering is protocol 4. Stale daemons must reject ordinary clients rather than accept an event or mutation they cannot interpret. Daemon lifecycle management is the sole compatibility exception: update and startup recovery may authenticate with a handoff-capable persisted daemon identity only to replace or stop that daemon, and a missing update path always resolves to the invoking client binary. An interactive TUI that connects as observe-only immediately requests `input.takeover`; older clients receive revocation and remain observe-only. When an older TUI window regains host focus, it requests takeover again before processing the next user terminal action. Paste keys are buffered into bounded semantic paste requests; the daemon terminal actor re-creates bracketed-paste boundaries around each payload while preserving LF newlines. Handshake and event-subscription phases have read deadlines; authenticated request connections and subscribed event connections remain usable across arbitrary idle periods.

Terminal sessions persist enough launch state for daemon restart policy. With `restore: forget`, persisted terminal state is replaced without parsing. With `restore: respawn`, unsupported or malformed session state is quarantined beside `_daemon/session.json`, an empty current-version session is written, and daemon startup continues; node and VM state are unaffected. Handoff version 3 transfers PTY descriptors via SCM_RIGHTS over an authenticated Unix stream with four-byte length-prefixed frames and rolls back if import fails. Control frames reject descriptors, and descriptor batches reject duplicate or unknown terminal IDs. A new importer may consume the previous Linux `unixpacket` version 2 once. The exact legacy-Darwin unsupported-transport error falls back to a daemon restart that preserves VMs and respawns saved tabs. Shutdown persists session intent first, closes terminals concurrently, and treats release of `_locks/daemon.lock` as the authoritative readiness signal. Grace is sized for terminal count; after it expires, only the still-matching daemon identity may be terminated. Startup applies the same recovery to a compatible socket-closed daemon that still owns the lock. Protocol, session, and handoff format revisions are versioned explicitly.

On macOS, while VirtioFS reclaim is enabled, the daemon runs `echo 2 > /proc/sys/vm/drop_caches` in every running mounted node once every 60 seconds. This is a workaround for an Apple Virtualization VirtioFS defect, not a performance optimization, so it is unconditional: nothing about host activity, guest activity, or a previous run's outcome may suppress or defer it, and a failed run neither backs off nor latches. No `sync` or page-cache drop is permitted. The daemon snapshot reports enablement, platform support, the interval, the last run's timestamp, the next run's timestamp, the node count the last run reclaimed, and the last run's error if it had one. The workaround is macOS-only because the defect is: Linux hosts mount guest workspaces over 9p, and their snapshots report this integration as unsupported.

## Storage contract

Schema version is `4`:

```text
CODELIMA_HOME/
  _config/schema.version
  _config/settings.yaml
  _config/agent-profiles/<name>.yaml
  _daemon/
  _index/configurations/by-slug/<slug>
  _index/environments/by-slug/<slug>
  _index/nodes/by-slug/<slug>
  _index/nodes/by-instance/<sandbox>
  _locks/<domain>.lock
  _locks/nodes/<id>.lock
  _locks/nodes/<id>.op.lock
  _quarantine/<timestamp>-<id>/
  configurations/<id>/configuration.yaml
  environments/<id>/environment.yaml
  nodes/<id>/node.yaml
  nodes/<id>/bootstrap.json
  nodes/<id>/sandbox.ref
  nodes/<id>/instance.lima.yaml
  nodes/<id>/events.jsonl
  nodes/<id>/context.jsonl
```

`_index/nodes/by-slug` and `_index/nodes/by-instance` resolve live slugs and Lima instance names without scanning every node file; both are maintained by `SaveNode` and dropped when a node is soft-deleted, renamed onto a new instance, or quarantined. `context.jsonl` is created empty beside a node's first save and is never rewritten by a save.

Schema v4 does not create a `projects/` directory. Schema versions 2 and 3 and recognized legacy Lima homes fail with `PreconditionFailed` and instruct the user to choose a fresh `--home`/`CODELIMA_HOME`; a schema-v3 rejection does not mutate the old home. Other non-empty unrecognized homes are rejected; the only exempt entries are the macOS Finder artifacts `.DS_Store` and `.localized`, which a home may contain and still count as fresh.

A node record that cannot be read or parsed never fails a listing. Every list path skips it, logs one warning naming the path, and continues, so one damaged `node.yaml` cannot hide the other nodes from the TUI, the daemon, or `node list`. Point lookups by id, slug, or instance name still fail with `MetadataCorruption` rather than reporting the node absent. `doctor` reports each skipped record; `doctor --repair` quarantines it, moving the whole `nodes/<id>/` directory to `_quarantine/<timestamp>-<id>/`, dropping the by-slug and by-instance index entries that resolved to it, and writing a `quarantine.yaml` manifest beside the moved files. Nothing is deleted: a human recovers the record by moving the directory back. `_quarantine/` is created on first use and is not enumerated by any node surface.

## Concurrency contract

No file lock is held across runtime work. Global domain locks (`_locks/nodes.lock`, `_locks/configurations.lock`, `_locks/environments.lock`) protect cross-node state only — listings, uniqueness, and the by-slug/by-instance indexes — and are held for metadata read-modify-write. Per-node locks (`_locks/nodes/<id>.lock`) protect one node's directory, so lifecycle operations on independent nodes never contend. Locks are always taken in one call, global domains before per-node locks and each tier sorted; the sole permitted nesting is an already-held global set while per-node locks are taken. Acquisition polls non-blockingly so a cancelled context stops waiting, and a wait longer than one second is logged with the lock's name.

A node admits one lifecycle operation at a time. While VM create/start/stop, workspace seeding, bootstrap, and validation run with no lock held, the node's persisted status is the durable guard (`provisioning` for a start, `terminating` for a delete) and `_locks/nodes/<id>.op.lock` is its liveness token, claimed non-blockingly and never waited on. A second operation on the same node fails immediately with `PreconditionFailed` rather than queueing. If a process dies mid-operation the kernel drops the token, so a persisted in-flight status with no token holder is a stale claim: the next operation on that node records `node.lifecycle.recovered`, resets the node to `failed`, and proceeds; `doctor` reports stranded nodes and `doctor --repair` recovers them without a lifecycle operation.

The seed-and-repair pass checks its version stamp before taking any lock, so an already-seeded home never queues behind another operation; when it does have work, it takes the three domain locks, re-checks the stamp, and then takes the metadata lock of every node whose file it may rewrite.

CodeLima owns the `daemon` key of `settings.yaml` — autostart, restore policy, and macOS VirtioFS reclaim enablement — and writes no other key. Reusable VM defaults live in the editable `small` configuration. Because the loader reads the whole file, a settings refresh round-trips the document: comments, key order, and every key outside `daemon` survive unchanged, including keys a newer CodeLima wrote. The `daemon` block itself is rewritten wholesale, which is how a retired setting is dropped; a file still carrying one is refreshed exactly once. Retired and unrecognized keys never fail a load.

## Error categories

- `InvalidArgument`: malformed command, slug, path, size, port, or template
- `NotFound`: missing configuration, environment, or node
- `PreconditionFailed`: protected/default mutation, live reference, schema mismatch, invalid lifecycle transition, or unavailable directory
- `DependencyUnavailable`: missing/incompatible Lima runtime or daemon
- `MetadataCorruption`: malformed or inconsistent persisted metadata
- `UnsupportedFeature`: reserved runtime/provider values
- `Internal`: unexpected implementation failure

## Verification requirements

Completion requires:

- unit tests for configuration protection/copy semantics, frozen nodes, directory containment, clone semantics, schema rejection, Lima rendering, observation, and SSH restrictions
- CLI tests proving project/config groups are absent and schema-v4 commands work
- TUI tests for flat nodes, configuration labels, path-relative directories, configuration selection, and node-scoped guest/host tabs
- daemon/integration tests for lifecycle, exact protocol, persistence, live-update commit/rollback, and terminal continuity
- race tests and lint/format/gopls diagnostics
- every applicable manual flow in `QA.md`
