# Patterns

## Schema-owned metadata home

`CODELIMA_HOME` is authoritative for configurations, environments, nodes, daemon state, indexes, and locks. Durable writes use temp-file-plus-rename semantics. `_config/schema.version` is checked before metadata is interpreted; schema v4 rejects schema-v2, schema-v3, and legacy Lima homes instead of attempting an in-place migration.

Read paths create only the directory skeleton required to inspect a recognized home. Seed and repair work runs under the relevant flock set from mutating readiness, `doctor --repair`, or TUI startup. Runtime-backed node lists fetch sandbox observations once, index by sandbox name, and merge transient status in memory.

## Reusable configuration, frozen node

A configuration is global to one metadata home and never owns a directory. It contains image, agent profile, environment references, direct bootstrap commands, vCPUs, memory, and disk.

Node creation resolves a configuration and copies every effective value into node/bootstrap metadata. Existing nodes never consult a mutable configuration for runtime behavior. This snapshot-at-creation boundary prevents an innocent configuration edit from changing or invalidating a running VM.

The `small` configuration is the editable but non-renamable and non-deletable implicit default. It references the built-in `codex` and `claude-code` environments on a fresh home, so nodes created without an explicit configuration install both coding agents. Seed/repair preserves existing `small` values except for explicit, revision-gated migrations that match the complete former stock preset; seed revision 4 moves untouched 1/1/10 records to the new 2/4/25 default while customized records remain unchanged. New configurations are deep copies of its current value rather than children that inherit future changes.

Built-in sizes are persisted configurations, not synthetic UI rows. Seed them through the store so CLI and TUI consumers agree, order them by size instead of alphabetically, and treat any existing record—including a soft-deleted tombstone—as user-owned. A seed-revision bump may add a genuinely missing size to an older home, but repair must never overwrite a customized configuration or resurrect a deleted optional size. If a formerly optional record becomes a required protected default, restore its tombstone in place so custom values and identity survive. Retire the former `default` configuration with a tombstone rather than removing its record: it disappears from live lists while existing nodes can still hydrate their historical configuration ID and slug.

## Canonical directory binding

Every node persists one canonical absolute `directory_path`. An omitted creation directory means the process current directory. Paths must exist, be directories, and remain outside `CODELIMA_HOME`.

Directory scoping uses `filepath.Rel`-style containment, never string-prefix matching; `/work/api-old` is not a descendant of `/work/api`. Multiple nodes may share a directory. Node slugs remain globally unique within a home so ID-or-slug command lookup is unambiguous.

`copy` mode seeds the canonical directory into a guest-local tree exactly once. `mounted` mode exposes the canonical host directory read/write at the same path and is the default for new nodes across service, CLI, and TUI creation paths. Explicit `copy` remains available, and blank legacy node metadata still resolves to `copy`. Runtime lifecycle and shells use the node's frozen directory and workspace mode only.

## Lima CLI and rendered-template boundary

Lima 2.x is the only runtime integration. `LimaClient` contains the CLI
boundary, validates the compatible version range, parses JSON-lines output,
maps bounded command failures into CodeLima error classes, and terminates the
whole child process group on cancellation. Interactive shell transports are
the deliberate exception: keep `sh`/`limactl`/SSH in the foreground process
group inherited from the terminal wrapper. The wrapper owns the PTY session
and tab teardown already terminates that complete group; placing only the
runtime child in a new group without a matching `tcsetpgrp` handoff makes it a
background reader and breaks arrows, line editing, paste, and guest interrupts.
Keep argv data quoted at the runtime-command seam and never interpolate
unvalidated instance identities into destructive operations.

`instance.lima.yaml` is a private derived artifact, not durable product truth.
Resolve the selected `image` template with `limactl template copy --fill`,
modify parsed YAML structurally, atomically write mode `0600`, and require
`limactl validate` before create. Strip inherited mounts and containerd,
render only the selected workspace, use exact binary MiB values, and put a
final ignore-all rule after explicit port mappings so CodeLima remains the host
listener owner.

Host-only runtime features are capability-derived at the Lima boundary, not
stored as configuration preferences. Keep native detection behind a small
platform adapter with conservative false stubs. When a supported capability
must also apply to pre-existing Lima instances, encode it in newly rendered
YAML and repeat it as an idempotent lifecycle flag; nested virtualization uses
this pattern with Apple's authoritative VZ capability query and
`limactl start --nested-virt`.

Lima logs in as an unprivileged distribution user. Preserve CodeLima's existing
root guest-command contract at one boundary by prefixing noninteractive
commands with `sudo -H --`; do not scatter privilege changes through bootstrap
or workspace code.

Fullscreen terminal applications must contain every subprocess diagnostic at
the mode boundary before taking ownership of the screen. CLI mode may forward
runtime stderr to the caller's stderr, but TUI mode replaces the long-lived
runtime client's diagnostic writer with a structured file-log adapter before
the initial data load. Keep successful-command stderr as well as failed-command
stderr: tools such as Lima can report actionable warnings while returning zero.
Background operations may override the client through `withIO` so their output
continues to appear in the operation surface. No recurring refresh path may
retain a writer that points at the TUI process's original stderr.

Keep `LIMA_HOME` short and place it on a filesystem that supports Unix sockets.
Lima creates hostagent, QMP, serial, guest-agent, and OpenSSH control sockets
beneath each instance directory; ordinary network mounts may reject them even
when regular files work.

## Dynamic node-hostname forwarding

The daemon owns HTTP/WebSocket forwarding for discovered guest listeners. The earliest active route on a port claims both `localhost:{guest-port}` and `127.0.0.1:{guest-port}` until that guest listener disappears; the next active claimant takes over during the one-second reconciliation loop. Treat only the exact IPv4 loopback address as a generic alias, preserve the original request `Host` upstream, and keep the claim ephemeral rather than persisting it.

Host routing at `{node-slug}.localhost:{guest-port}` always chooses the named node, so several nodes can use the same guest port regardless of the generic claimant. Traffic travels over one persistent Go SSH client per running Lima node. Resolve the localhost endpoint and identity only from the Lima-owned `ssh.config`, validate its ownership, permissions, and containment under `LIMA_HOME`, and do not modify global SSH configuration. Discovery accepts IPv4 and IPv6 guest-loopback listeners, so each new tunnel connection tries `127.0.0.1` and then `::1`; keep accepted discovery addresses and dial targets aligned. Host bind conflicts remain observable and are retried every reconciliation.

Explicit `HOST:GUEST` ports remain an advanced raw-TCP escape hatch selected at node creation. They are validated and frozen in the rendered Lima instance template.

## Pressure-triggered host integration recovery

Host integration workarounds use a sample, threshold, actuate, verify, and cooldown controller rather than an unconditional timer. The macOS VirtioFS controller samples the system file table, acts only at the configured pressure boundary, compares the post-action sample, and applies the same 30-second cooldown after every attempt. Its lifecycle is owned by the daemon and its latest state is included in the daemon snapshot.

Guest cache reclamation stays scoped to running mounted nodes and to clean filesystem metadata (`drop_caches=2`). Do not add `sync` or page-cache dropping to this pattern: unrelated dirty-write policy and broad guest memory reclamation are outside the descriptor-pressure problem.

## Runtime observation versus durable lifecycle

`node.yaml` persists CodeLima-owned lifecycle and stable runtime configuration. Running/stopped truth discovered from Lima is attached as a transient `RuntimeObservation` and is not written during ordinary reads. The daemon owns one `limactl watch --json` process, seeds an observation cache with one direct list, and serves recurring TUI and forwarding refreshes from that cache. Lifecycle operations reconcile directly after mutation and update the cache. Mutating reconciliation persists only at explicit lifecycle boundaries.

An incomplete node directory without `node.yaml` remains discoverable through `doctor` and `node cleanup-incomplete`. Persist `sandbox.ref` plus its exact instance index before runtime create or clone so applied cleanup can tear down only that matching Lima instance before deleting its durable reference. Deferred rollback closures must capture the immutable target node, never a named return value that can become zero-valued on error.

## Terminal identity and actors

The `internal/codelima/terminal` package owns typed `TargetKey`, `TerminalID`, and terminal kinds. Schema-v4 front ends create node targets only. Guest and host tabs share `node:<id>` while their kinds distinguish `node-shell` from `node-host-shell`.

Every managed terminal starts from `Service.TerminalLaunchSpec`. Guest shells re-enter `codelima shell <node>`; host shells start in the node's canonical directory. Terminal creation resolves this durable node metadata without live runtime reconciliation: host shells do not depend on Lima availability, and guest-shell child commands perform their own runtime checks. Front ends never assemble launch commands independently.

Daemon terminal runtimes are serialized actors. Clients send commands for input, resize, snapshot, focus, clipboard, and close; they do not call Ghostty or PTY state concurrently. Dirty notifications are edge-triggered and clients pull snapshots, preventing redraw loops. Never poll immutable terminal grids on an idle timer: route dirty notifications through the single-owner UI loop, retain only a dirty version for hidden tabs, and let the active tab wake a capacity-one snapshot worker with a bounded frame cadence. Keep terminal input off the TUI event loop: enqueue semantic events onto one FIFO worker per daemon terminal, stop admission and drain accepted input on detach, surface asynchronous delivery failures, and redraw payload input only after a fresh snapshot arrives. Preserve semantic input boundaries across the client/daemon seam: buffer Vaxis paste keys until `PasteEndEvent`, send bounded UTF-8-safe semantic paste chunks, and let the daemon actor re-create paste-start/payload/paste-end so Ghostty remains the owner of bracketed-paste mode. Preserve LF in paste data; a pasted newline is text, not an Enter key.

Terminal tabs are independent terminal instances attached to one node target. Per-target state keeps ordered tab IDs, a monotonic counter, active selection, and the last open error. Selecting a running node ensures a terminal tab, reusing any existing node tab and opening a guest tab only when none exists. `Option+t` adds another guest tab and `Option+Shift+t` adds a host tab; all use the same switch, move, close, refresh, and active-tab paths. Moving swaps the active identity with one adjacent same-target tab without wrapping or changing activation. Never open a guest tab for a stopped node.

A path-scoped TUI node list is a projection, not an authoritative deletion set. Before refresh reconciliation closes a daemon-owned tab that is absent from the projection, confirm the node is missing or deleted in the global metadata store. Retain tabs on ambiguous metadata failures; explicit stop/delete operations remain the immediate close path. Apply the same rule whenever a scoped client can otherwise destruct shared daemon state.

Runtime maps are never an ordering source. Keep a daemon-owned ordered terminal-ID slice beside the runtime map and use it at every state boundary—list response, snapshot, session persistence, respawn, or live handoff. Persist order in the existing ordered session array, restore in that array order, and use durable creation time plus an identity tie-breaker only to normalize legacy or partially constructed state that has no explicit order.

## Incident capture before recovery

Cross-terminal failures must be captured while the original daemon is still
running. Keep incident tooling read-only: probe the daemon control plane
independently from at most one terminal actor, copy metadata and bounded log
tails, collect a non-terminating native process sample, record every probe exit
status, and continue when one probe fails. Never rebuild, stop, update, signal,
take input ownership, or send terminal data before the capture completes.

Store evidence under the project `tmp/` root, do not upload it automatically,
and warn that logs, argv, paths, and terminal metadata may be sensitive. A
successful capture command means the bundle was created, not that the daemon is
healthy. Interpret observations before hypotheses: a responsive status/list
surface with a failed terminal read isolates the terminal-engine boundary,
while a responsive terminal read with frozen clients shifts investigation to
event delivery and snapshot/redraw handling.

## Exact daemon protocol and live update

The private Unix-socket daemon protocol is newline-delimited JSON with an exact binary-version handshake, a bounded frame size, peer-credential checks, typed error responses, and explicit input ownership. Any request-shape change bumps the protocol version; semantic paste was introduced in protocol 3, and daemon-owned tab movement is protocol 4, preventing stale daemons from accepting mutations they cannot interpret. Interactive TUI connection setup must finish by claiming input ownership, even when `hello` reports an existing owner; this makes the newest interactive TUI authoritative and prevents delayed terminal opens from discovering an observe-only connection. Because handshake ownership becomes stale when another TUI connects, every TUI window-focus event sends the separate idempotent `input.takeover` request before subsequent user input. Revocation is an expected internal handoff and must not be rendered as a TUI error; surface only a failed takeover. Do not recover by replaying a failed terminal mutation. Apply read deadlines to protocol phases that must make prompt progress: the initial handshake and event subscription. Clear the server-side deadline for authenticated request clients and subscribed event clients because a valid TUI session may be idle indefinitely. A client event reader may use bounded idle reads, but it retries only timeout errors; context cancellation, shutdown/update commit, and permanent socket errors terminate the reader once. Schema/session format changes bump their own version constants.

Until coordinated automatic reconnection exists, daemon disconnection is a
latched TUI lifecycle state. Send shutdown, update commit, and permanent event
stream failure through a dedicated UI event; after the latch, do not probe the
dead request socket on window focus and do not let late terminal-worker errors
replace the actionable reopen instruction. A focus takeover failure must set
the same latch because a timed-out or partially completed stream request is not
safe to reuse. Keep the transport detail in structured logs and never replay an
ambiguous terminal mutation.

Daemon terminal sessions are recoverable cache state, not authoritative node metadata. `restore: forget` replaces that cache without parsing it. Under `restore: respawn`, an unsupported or malformed session is atomically renamed beside the live path, a fresh current-version session is written, and startup continues with a warning. Keep strict schema rejection for authoritative stores; use quarantine-and-reset only for state whose loss changes convenience rather than data truth.

Live update transfers PTY file descriptors over an authenticated, length-prefixed Unix stream with SCM_RIGHTS. Never assume stream read boundaries equal writes: frame every bounded JSON payload and collect ancillary descriptors while reading the exact frame. Reject descriptors on control frames and duplicate or unknown terminal descriptors rather than ignoring or leaking them. The CLI resolves and sends its own executable even when no path is given; never resolve an omitted replacement inside the old daemon. Daemon lifecycle management alone may authenticate with the running daemon's persisted protocol-2-or-newer identity to cross an exact-version boundary: update explicitly takes input ownership, while startup recovery may only stop the matching daemon. Import is staged, acknowledged, and committed; a failed import rolls back to the old daemon. Only the exact legacy-Darwin `unixpacket` unsupported error may fall back to a clean stop/start; do not turn generic handoff errors into destructive restarts. Integration paths and unit tests that create Unix sockets use `internal/testutil.TempDir`, which keeps disposable paths short and under the project-rooted `tmp/tests` directory so Darwin's smaller `sun_path` limit is exercised portably.

Treat terminal geometry as one invariant across the wrapper, emulator, and PTY. A handoff replacement must resize the emulator to the captured dimensions before attaching the transferred PTY and replaying output; assigning wrapper fields alone corrupts cell-row strides, while resizing after attachment can send an unnecessary `SIGWINCH` to the child.

Geometry repair must stay out of the terminal input stream. After primary-screen
width growth, resize Ghostty and the PTY first, then request a clean redraw with
`SIGWINCH` to the PTY child process group. Never inject `Ctrl-L` or another
application key as a resize workaround: a transport that is still starting can
echo it literally, and a ready shell can clear history the user expected to
keep. Alternate-screen, mouse-tracking, narrower, and scrolled-back sessions do
not receive the supplemental redraw signal.

The daemon lock, not the presence of socket or identity pathnames, is the authoritative shutdown/startup gate. Persist terminal restoration intent before slow cleanup, close terminal runtimes concurrently, and size legacy shutdown grace for the reported terminal count. After the bounded grace, signal only a process whose persisted token and PID still match and whose daemon lock remains held. Startup uses the same lifecycle-only recovery for a compatible daemon that has closed its socket but not yet released the lock.

## TUI data flow

The production TUI loads a flat node list, optionally scoped by canonical directory containment. Render each node as one fixed-height selectable block: the slug identifies the block, and indented property lines show its configuration, relative directory within the active scope, and live status. Derive viewport capacity from content rows divided by block height, and map mouse rows back through the same height so scrolling, highlighting, and property-line clicks keep one item identity. Configuration and environment menus are global; node actions use only the selected node.

Bounded presentation animations stay inside the UI event loop. Derive their frame from monotonic elapsed time so delayed events cannot rewind visible state, keep changing text cell-width stable, and stop their ticker as soon as the terminal state is reached. When only a small fixed region changes, update that region and use the normal Vaxis diff render instead of clearing and rebuilding terminal, tree, and overlay surfaces. Keep pure elapsed-time-to-frame behavior separate from the event producer so progression tests do not sleep.

Resolve node-pane defaults whenever selection changes or the selected VM crosses the running boundary. A running selected node uses terminal mode and opens or reconnects one guest tab; a non-running selected node uses info mode and opens no guest shell. Preserve an explicit `i` choice while the operator remains on the same node at the same runtime readiness, but restore the status-derived default after selecting another node.

Dialogs, selectors, menus, and messages occupy the right pane. Dialog key routing follows the active field's semantics: `Tab`/`Up`/`Down` move between fields, text inputs receive `Left` and `Right` for cursor movement, and `Right` is consumed as `choose` only when the field exposes a selector activation callback. Selector option labels may carry decision context, but option values remain stable domain identifiers; configuration rows append formatted vCPU, RAM, and disk while still submitting the slug. Long runtime mutations execute as background operations with resource keys that reject conflicting work while navigation, refresh, and unrelated terminal tabs remain responsive.
