# Patterns

## Schema-owned metadata home

`CODELIMA_HOME` is authoritative for configurations, environments, nodes, daemon state, indexes, and locks. Durable writes use temp-file-plus-rename semantics. `_config/schema.version` is checked before metadata is interpreted; schema v3 rejects schema-v2 and legacy Lima homes instead of attempting an in-place migration.

Read paths create only the directory skeleton required to inspect a recognized home. Seed and repair work runs under the relevant flock set from mutating readiness, `doctor --repair`, or TUI startup. Runtime-backed node lists fetch sandbox observations once, index by sandbox name, and merge transient status in memory.

## Reusable configuration, frozen node

A configuration is global to one metadata home and never owns a directory. It contains image, agent profile, environment references, direct bootstrap commands, vCPUs, memory, and disk.

Node creation resolves a configuration and copies every effective value into node/bootstrap metadata. Existing nodes never consult a mutable configuration for runtime behavior. This snapshot-at-creation boundary prevents an innocent configuration edit from changing or invalidating a running VM.

The reserved `default` configuration is editable but not renamable or deletable. On a fresh home it references the built-in `codex` and `claude-code` environments, so nodes created from it install both coding agents. Seed/repair never overwrites an existing default configuration. New configurations are deep copies of its current value rather than children that inherit future changes.

## Canonical directory binding

Every node persists one canonical absolute `directory_path`. An omitted creation directory means the process current directory. Paths must exist, be directories, and remain outside `CODELIMA_HOME`.

Directory scoping uses `filepath.Rel`-style containment, never string-prefix matching; `/work/api-old` is not a descendant of `/work/api`. Multiple nodes may share a directory. Node slugs remain globally unique within a home so ID-or-slug command lookup is unambiguous.

`copy` mode seeds the canonical directory into a guest-local tree exactly once. `mounted` mode exposes the canonical host directory read/write at the same path and is the default for new nodes across service, CLI, and TUI creation paths. Explicit `copy` remains available, and blank legacy node metadata still resolves to `copy`. Runtime lifecycle and shells use the node's frozen directory and workspace mode only.

## Microsandbox SDK boundary

The official Microsandbox Go SDK pinned at `v0.6.6` is the only runtime integration. Its `EnsureInstalled` API provides the matching runtime support files before version validation; CodeLima never executes the installed `msb` and has no CLI fallback. Lifecycle, listing, guest exec, interactive attach, file copy, snapshots, mounts, published ports, and network policy use typed SDK operations.

Sandbox creation uses `WithImage`, `WithCPUs`, `WithMemory`, and `WithOCIUpperSize` for the node's frozen values. SDK 0.6.6 copies one file at a time, so recursive seeding walks the host tree, creates guest directories, copies regular files, and recreates symbolic links through typed guest exec. Unsupported special files fail explicitly.

Copied regular files must not be assumed to retain host executable bits. Bootstrap commands that run repository scripts invoke an explicit guest interpreter, for example `sh ./script/setup`, unless the bootstrap first sets and verifies the required mode.

Keep runtime homes short because Microsandbox derives Unix-domain sockets beneath them and the kernel path limit is much smaller than normal filesystem path limits.

## Dynamic node-hostname forwarding

The daemon owns HTTP/WebSocket forwarding for discovered guest listeners. Host routing is `{node-slug}.localhost:{guest-port}`; the hostname chooses the node, so several nodes can use the same guest port. Traffic travels over a multiplexed SDK-backed SSH helper and works for guest-loopback listeners.

Explicit `HOST:GUEST` ports remain an advanced raw-TCP escape hatch selected at node creation. They are validated and frozen because SDK port configuration cannot be changed after sandbox creation.

## Pressure-triggered host integration recovery

Host integration workarounds use a sample, threshold, actuate, verify, and cooldown controller rather than an unconditional timer. The macOS VirtioFS controller samples the system file table, acts only at the configured pressure boundary, compares the post-action sample, and applies the same 30-second cooldown after every attempt. Its lifecycle is owned by the daemon and its latest state is included in the daemon snapshot.

Guest cache reclamation stays scoped to running mounted nodes and to clean filesystem metadata (`drop_caches=2`). Do not add `sync` or page-cache dropping to this pattern: unrelated dirty-write policy and broad guest memory reclamation are outside the descriptor-pressure problem.

## Runtime observation versus durable lifecycle

`node.yaml` persists CodeLima-owned lifecycle and stable runtime configuration. Running/stopped truth discovered from Microsandbox is attached as a transient `RuntimeObservation` and is not written during ordinary reads. Mutating reconciliation persists only at explicit lifecycle boundaries.

An incomplete node directory without `node.yaml` remains discoverable through `doctor` and `node cleanup-incomplete`. Applied cleanup tears down a matching sandbox before deleting its only durable reference.

## Terminal identity and actors

The `internal/codelima/terminal` package owns typed `TargetKey`, `TerminalID`, and terminal kinds. Schema-v3 front ends create node targets only. Guest and host tabs share `node:<id>` while their kinds distinguish `node-shell` from `node-host-shell`.

Every managed terminal starts from `Service.TerminalLaunchSpec`. Guest shells re-enter `codelima shell <node>`; host shells start in the node's canonical directory. Terminal creation resolves this durable node metadata without live runtime reconciliation: host shells do not depend on Microsandbox availability, and guest-shell child commands perform their own runtime checks. Front ends never assemble launch commands independently.

Daemon terminal runtimes are serialized actors. Clients send commands for input, resize, snapshot, focus, clipboard, and close; they do not call Ghostty or PTY state concurrently. Dirty notifications are edge-triggered and clients pull snapshots, preventing redraw loops.

Terminal tabs are independent terminal instances attached to one node target. Per-target state keeps ordered tab IDs, a monotonic counter, active selection, and the last open error. `Option+t` adds a guest tab and `Option+Shift+t` adds a host tab; both use the same switch, close, refresh, and active-tab paths. Selection and refresh do not create tabs; explicit open/focus actions do.

## Exact daemon protocol and live update

The private Unix-socket daemon protocol is newline-delimited JSON with an exact binary-version handshake, a bounded frame size, peer-credential checks, typed error responses, and explicit input ownership. Interactive TUI connection setup must finish by claiming input ownership, even when `hello` reports an existing owner; this makes the newest interactive TUI authoritative and prevents delayed terminal opens from discovering an observe-only connection. Apply read deadlines to protocol phases that must make prompt progress: the initial handshake and event subscription. Clear the deadline for authenticated request clients and subscribed event clients because a valid TUI session may be idle indefinitely. Schema/session format changes bump their own version constants.

Daemon terminal sessions are recoverable cache state, not authoritative node metadata. `restore: forget` replaces that cache without parsing it. Under `restore: respawn`, an unsupported or malformed session is atomically renamed beside the live path, a fresh current-version session is written, and startup continues with a warning. Keep strict schema rejection for authoritative stores; use quarantine-and-reset only for state whose loss changes convenience rather than data truth.

Live update transfers authenticated listeners and PTY file descriptors over `unixpacket` with SCM_RIGHTS. Import is staged, acknowledged, and committed; a failed import rolls back to the old daemon. Integration paths use a short project-rooted temp directory so handoff socket names stay within kernel limits.

## TUI data flow

The production TUI loads a flat node list, optionally scoped by canonical directory containment. Rows show node slug, configuration label, relative directory within the active scope, and live status. Configuration and environment menus are global; node actions use only the selected node.

Dialogs, selectors, menus, and messages occupy the right pane. Long runtime mutations execute as background operations with resource keys that reject conflicting work while navigation, refresh, and unrelated terminal tabs remain responsive.
