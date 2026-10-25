# Patterns

## Structured resource gauges

Keep generated gauges as `tuiGaugeLine` values until drawing. Preserve the base
background and attributes when choosing fill/track foregrounds; sanitize and
clip all segments together. Do not infer metric styling from arbitrary text
that contains block glyphs (ADR 201).

## Contextual keyboard actions

Declare stable actions, defaults and finite focused contexts in the shared key
registry. Resolve an action while retaining the original physical event for
press/repeat/release claims. Dispatch pointer controls by action ID, never by a
synthetic default key. Keep text editing in its widget and terminal payload in
its terminal. Compile and validate a complete replacement before publishing it;
retain the last valid snapshot on errors. Use registry-backed hint tokens and
the format-aware helper when inserting bindings into formatted strings, so a
percent key remains literal. Inspectable metadata must return owned copies
(ADR 200).

## Live commands with captured identities

Build search catalogues from immutable frontend snapshots and store canonical
IDs in result selections and rendered mouse targets. Refresh before invocation;
retain a disappeared selected command as unavailable instead of selecting its
successor. Revalidate again at confirmation, then call the shared action owner
with the captured identity. Batch reviews capture both membership and action
kind, validating all members before the first operation. Never derive a target
from mutable row order or unrelated marks after review (ADR 193).

## Contextual help without action ownership

Keep shortcut descriptions separate from executable palette commands. A help
overlay captures its opening context and owns only focus, per-group scrolling,
search text and rendered mouse regions. Search every context and wrap complete
bindings at terminal-cell boundaries so compact layouts retain access to the
same information. Route paste framing through the existing input widget; do
not treat pasted text, inherited releases or repeats as activation. A catalogue
entry must describe a supported dispatcher, including focus restrictions.
Resolve displayed bindings from the immutable effective registry; keep help
read-only even when settings change (ADRs 192 and 200).

## Validated asynchronous forms

Keep editable values and dirty-discard behavior in the shared dialog model;
wrap it for domain-specific presentation and controls. Perform metadata/path
reads in workers and publish immutable completions carrying the exact form,
input value and relevant revision. A single-flight check queues only the latest
changed value. Parent overlay references preserve values across nested choices.
Never infer free capacity from a missing observation. Preserve an ambiguous
admission's payload and idempotency key; separate durable operation ownership
from receipt-bound frontend navigation and attachment intent (ADR 174).

## Shared notification history

Publish general messages with client-generated idempotency IDs and daemon
timestamps/sequences. Commit private atomic records before acknowledging them.
Bound bytes, count and age; preserve corruption evidence and surface degraded
message reads without hiding independent lifecycle cancellation controls.
Project request/completion rows from canonical receipts rather than duplicating
their output into a second journal. Treat origin as provenance, not mutation
identity, and omit unknown legacy provenance.

Keep unacknowledged frontend messages visibly local. Overlay an acknowledgement
until a snapshot from the same journal epoch covers its sequence; absence from
one history page is not evidence that a message has not been committed. Fence
late replies, retry the same ID after a lost acknowledgement, and bound the
pending queue. Do not write journal failures back into the journal (ADR 165).

## Interactive table rendering

Allocate table fields in terminal cells before rendering text, keeping action
columns reachable even when names or messages are truncated. Fill the complete
selected row and apply its background/reverse attribute to each field style;
retain semantic foreground colors. Compute hit regions from the same rendered
windows, clear them each draw, and omit targets for clipped controls. Dispatch
mouse controls through existing key handlers or typed row actions so search,
modal and navigation ownership remain consistent. Derive request glyphs from
the canonical request/completion ID contract, never from message text (ADR 172).

## Durable operation admission

Persist an idempotent admission receipt before launching slow work on a
daemon-owned context. Resolve resource identity before arbitration, then keep
runtime execution outside manager and metadata locks. End only a caller's wait
when its connection closes. Explicit cancellation and daemon shutdown have
different causes: cancellation may owe VM cleanup, while shutdown records an
interruption. Recover history without replaying uncertain side effects. Gate
live replacement against active operations until their ownership can be
transferred safely (ADR 161).

Keep a requested create-and-start chain within one durable admission. Reserve
the slug before creation and retain that key on existing-node operations, then
persist the created UUID before first boot. Release the create service token
before acquiring the start token, while retaining the daemon admission across
both phases. Include combined requests in startup queries and cancellation
cleanup; summary projections must retain their startup intent (ADR 173).
Runtime mutation decisions and their success checks must bypass observation
caches. An old stopped sample cannot prove that a canceled boot left no VM.

Connect CLI mutation paths lazily after parsing. Resolve caller-relative inputs
before crossing a process boundary and send progress to stderr when stdout
owns a result envelope. A lost admission response may hide a committed action:
preserve the client-generated receipt ID and offer inspection before retry.
Keep daemon execution copies free of frontend connection hooks. Startup must
not replace a responsive operation-owning daemon; use the update admission
gate rather than a check-then-stop sequence (ADR 162).

Record lifecycle phase boundaries before execution. Advance only after the
previous phase succeeds; a detail update must not reset its start time. Keep
skipped work and unknown finish times explicit, and retain command-level
attempt records separately. Stream mutation progress deliberately while
keeping query stdout private (ADR 163).

Query a node's latest start by admission time and canonical identity, independently
of paginated Activity history and later stop receipts. Fence detail replies by
request serial, navigation generation and node ID. Bind deferred attachment to
one node/operation pair; revoke it on navigation, cancellation or failure and
consume it once only after both the receipt and live node prove readiness.
New boot failures take precedence over older bootstrap evidence (ADR 164).

Project bounded progress from authoritative phase records before removing full
phase/output bodies from list responses. Count succeeded and skipped boundaries;
do not turn elapsed time into an estimated percentage. Keep missing evidence
explicit and let renderers consume the summary without per-row I/O (ADR 166).

When a failed daemon handoff returns ownership to a previous process, invalidate
its Activity cache under the update barrier before resuming listeners. Reload
successor-committed records and reset the epoch before allocating another
sequence; a process-local cache is not authoritative across ownership (ADR 167).

Publish small typed runtime projections after releasing runtime locks. Compare
observed transitions into a bounded queue, retain IDs across persistence retries,
and start its writer only after process ownership. Gate writes against live
handoff and retire a process that cannot recover ownership. Broadcast committed
records to frontend footers without sending them back through the outbox
(ADR 168). Share the observed allocation helper with host capacity rendering.
An in-place update notice belongs to the successor after its committed reply
succeeds. Derive retained counts from adopted objects and reuse the owned queue;
frontend completion and predecessor intent are insufficient evidence (ADR 169).
Compare terminal notification counters only on fresh snapshots. Seed adopted
metadata before admitting new PTY output, and close producer admission before
draining shutdown queues. Keep Activity navigation typed and match both terminal
and node identity; never substitute a live session for an ended history target
or interpret display text as an action (ADR 170).
Freeze native admission and snapshot its pending queue and observation baselines
under one mutex while the update barrier excludes persistence. Transfer stable
IDs and overflow accounting, then let the successor compare restored terminal
metadata with the last observation instead of assuming every restored notice
was announced. Preserve the original queue on rollback and wake observers after
ownership resumes. Bound and validate transfer data before activation (ADR 171).

Retain bounded output with absolute line sequences so polling clients neither
duplicate lines nor confuse a rotated tail with new output. Coalesce persistence
outside producer writes and flush before the terminal receipt. Keep a global
frontend projection independent of view navigation; fence page replies with a
request serial, identify selection by receipt ID, and cancel stale page loads.

## Execution evidence and explicit retry

Freeze command provenance at creation. Persist a unique running attempt before
the command, then its observed outcome with bounded rollback locks even when
the caller is canceled. Carry the attempt ID into logs and events so links do
not depend on ambiguous command prefixes. Keep slow execution outside locks.
Only skip a prefix with durable success evidence, and defer resetting records
until preflight succeeds. Changed commands invalidate old success evidence;
legacy records and interrupted runs must retain explicit unknown fields.
Cloned disk history keeps its original execution identity (ADR 160).
Anchor a selected command to its frozen step identity when refreshed outcomes
change row counts. Matching command text alone merges repeated commands.

## Frozen recipe inspection

Compare stored node/bootstrap values with the current recipe resolved through
its durable template ID. Include ordered environment commands and agent
settings; show missing dependencies explicitly and never infer historical
provenance from today's recipe. Read rendered instance YAML without runtime
preparation, with regular-file and size checks. Keep incomplete sections usable
alongside their errors. Fence asynchronous local reads by node identity,
request serial and navigation generation (ADR 159).

Pass contextual form defaults into construction so the initial-value snapshot
contains them. Applying a preset afterwards looks like a user edit and causes
false discard confirmations; retain empty inputs with default placeholders so
the first typed value still replaces the proposed default.

## Capacity observations and global allocation

Sample native host data outside the UI loop, preserving nil for unavailable
measurements. Combine capacity with the authoritative global node list before
applying directory scope or search. Use runtime observations when deciding
whether resources are allocated: a failed bootstrap can leave a VM running.
Deliver the immutable summary with the same generation as its node refresh.
Read-only checks must not create directories or invoke repair (ADR 153).

## Editable preferences and external shell launch

Patch only the selected YAML leaf under the settings lock, retaining comments,
key order and unknown mappings. Reject malformed documents before writing;
return a new configuration snapshot instead of mutating shared services.
Read defaults at the operation that freezes them into durable state. Expand
window command placeholders as argument splices and run executables directly;
keep session identity and CODELIMA_HOME explicit across process boundaries.
Menus scroll their selected row into view and offer Enter/mouse activation
even when entries have no shortcut (ADR 152).

## Physical terminal attachment

Capture present terminal state at a contiguous output watermark, then stream
only newer bytes. Never replay historical terminal queries or clipboard effects
into a physical terminal. Keep native response/effect ownership separate from
PTY lifetime; acknowledge delivered output before discarding the native fallback.
Tie attachment leases to physical connection lifetime and bound all held data.
Preserve the retired runtime long enough to drain its final output. Save and
restore outer-terminal termios on every return path. Tests should use real PTYs
and daemon sockets to verify detach, reattach, query ownership and process exit
in addition to parser and lease unit tests (ADR 151).

## Fleet projection and navigation reads

Keep durable identity separate from rendered row position. Fleet selection and
batch marks use node IDs; filtering/sorting produces a new view with directory
headings that cannot be selected. Prune marks against authoritative records,
not the current filter. Missing or nonfinite metrics remain unavailable and
sort last in either direction. Keep grouping preference independent of metric
sort order. Use the rendered projection for mouse hit testing (ADR 150).

Views that consume the same cached projection may share its generation-fenced
loader. Keep an in-flight read while navigating between consumers, cancel it
when leaving all consumers, and retain failed observations as explicitly stale.
Actions require current eligibility as well as durable identity. Derive marked
resource impact from the global allocation snapshot, never only visible rows;
missing readings must not become zeros (ADR 186).

Run view-specific service reads outside the UI loop and tag each response with
the requesting navigation generation. Ignore late replies after changing view
or selected node. Give text widgets and terminal payload priority over global
navigation. Claim focus-changing shortcut followups, but do not classify
ordinary shell keys as manager commands. Embedded shell focus reserves only F6.

## Observe progress in native process tests

Wait for specific output or state with a bounded deadline before triggering
handoff or asserting recovery. Native shell startup and external commands such
as `sleep` have platform-dependent overhead; a requested sleep duration is not
the loop's elapsed time. Preserve exact terminal IDs and shell PIDs across
handoff/rollback, and retain content-continuity assertions after progress occurs.

## Release version from a validated tag

Use `internal/release.ParseTag` for GitHub release metadata and Homebrew
formula generation. Accept only `vMAJOR.MINOR.PATCH`; reject prerelease suffixes
and mismatched manifest versions. Build from the requested tag, require versioned
release notes, and publish all native assets as a regular Latest release. Stage
the standard formula and any retired formula removal before checking the tap
diff, so promotion is atomic and repeated updates are no-ops (ADR 137).

Keep renderer dependency checks separate from platform host adapters. Only the
worker imports `internal/ghostty`; the CLI's macOS cgo source is restricted to
the Virtualization.framework capability adapter. Check source selection for
Linux and Darwin even on a Linux test host, and inspect `CGO_ENABLED` in the
actual packaged executables so packaging cannot silently select a false stub
on macOS (ADR 136). Apply frontend Option-as-Alt policy after refreshing native
terminal key options, which resets that host-specific setting upstream.

## Managed native build metadata tools

Bootstrap build-time tools into the platform-scoped `.tooling/<os>-<arch>`
tree and pass their absolute executable paths to consumers. Native cgo,
contract tests, benchmarks, and direct release packaging all use the same
managed `PKG_CONFIG`, with the matching static dependency's `.pc` directory
on `PKG_CONFIG_PATH`. Do not assume a user's shell or package manager provides
it, and do not replace a general metadata resolver with hard-coded flags.

The `pkgconf` bootstrap consumes checksum-verified, pinned upstream source and
the already-verified Zig compiler, so it does not need a host C compiler or
another package manager to install itself. Keep installer scratch and caches
project-local; publish only a complete verified install. Direct packaging
validates the native build identity before provisioning missing tools, so an
invalid input fails without unrelated downloads or output mutation.

## Asynchronous verification

Tests for daemon handoff, renderer publication, and other asynchronous state
transitions wait for an observable terminal condition under a bounded deadline,
then assert the complete result. A fixed sleep is not evidence that an event
has arrived and turns ordinary scheduler variance into a false data-loss
failure. Correctness deadlines that run under the race detector include room
for its instrumentation overhead; production responsiveness stays covered by
non-race automation and the timed manual QA flows.

When a test only needs deterministic runtime-command output, use the supported
configured-command seam instead of writing and immediately executing a scratch
script. Keep executable fakes for tests that specifically exercise argv or
binary transport. This avoids making unrelated cache and parsing tests depend
on transient executable-file behavior in loaded CI filesystems.

## Schema-owned metadata home

The reserved `_diagnostics/` directory may exist before first initialization.
Preserve it while recognizing a fresh home; it holds evidence, not runtime
intent. Do not extend that exception to unknown files or legacy layouts.

`CODELIMA_HOME` is authoritative for configurations, environments, nodes, daemon state, indexes, and locks. Durable writes use temp-file-plus-rename semantics. `_config/schema.version` is checked before metadata is interpreted; schema v4 rejects schema-v2, schema-v3, and legacy Lima homes instead of attempting an in-place migration.

Read paths create only the directory skeleton required to inspect a recognized home. Seed and repair work is triggered by mutating readiness, `doctor --repair`, or TUI startup, and checks its version stamp before taking any lock, so an already-seeded home never queues behind another operation; only a pass that has work takes the relevant flock set, re-checks the stamp under it, and locks each node whose file it may rewrite. Runtime-backed node lists fetch sandbox observations once, index by sandbox name, and merge transient status in memory.

## Reusable configuration, frozen node

A configuration is global to one metadata home and never owns a directory. It contains image, agent profile, environment references, direct bootstrap commands, vCPUs, memory, and disk.

Node creation resolves a configuration and copies every effective value into node/bootstrap metadata. Existing nodes never consult a mutable configuration for runtime behavior. This snapshot-at-creation boundary prevents an innocent configuration edit from changing or invalidating a running VM.

The `small` configuration is the editable but non-renamable and non-deletable implicit default. It references the built-in `codex` and `claude-code` environments on a fresh home, so nodes created without an explicit configuration install both coding agents. Seed/repair preserves existing `small` values except for explicit, revision-gated migrations that match the complete former stock preset; seed revision 4 moves untouched 1/1/10 records to the new 2/4/25 default while customized records remain unchanged. New configurations are deep copies of its current value rather than children that inherit future changes.

Built-in coding-agent environments share a native installer wrapper. Install
system prerequisites at the root bootstrap boundary, resolve Lima's login user
from `SUDO_USER`, reject UID 0, then download and execute each vendor installer
after dropping privileges. Download to a temporary file with exit cleanup;
never pipe a potentially failed download into a shell. Publish a stable
`/usr/local/bin` link only after success. Validate `--version` as the login user.
Keep each environment independently installable. Codex installs and validates system bubblewrap before running its native
installer. Seed revision 9 migrates only
exact known prior built-in definitions; customized and deleted records remain
authoritative. `NodeStart` replaces and reruns exact legacy bootstrap sequences
without changing custom commands or deleting old package files (ADRs 146–147).

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

Lima logs in as an unprivileged distribution user, and that login user is the
guest's native identity: it owns the workspace in both modes, the agent
installs, and the imported credentials. Which identity a guest command runs as
is an explicit argument at the runtime boundary, chosen by the calling surface
and never sniffed out of the command text. Anything the user typed — a managed
terminal, `codelima shell`, `codelima shell <node> -- <cmd>` — runs as the login
user; only service-issued provisioning is elevated, and the `sudo -H --` prefix
that elevates it lives at that one boundary. Do not scatter privilege changes
through bootstrap or workspace code (ADR 129).

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

The daemon owns HTTP/WebSocket forwarding for discovered guest listeners. The earliest active route on a port claims both `localhost:{guest-port}` and `127.0.0.1:{guest-port}` until that guest listener disappears; the next active claimant takes over during the one-second reconciliation loop. Product callback ports may define a narrower intent rule when their generic host is fixed: Codex login port 1455 follows the newest discovered listener and falls back to the next-newest route when it disappears, while ordinary ports retain earliest-claim stability. Bind each logical host port on both `127.0.0.1` and `::1`, serve both sockets from the same route registry, and roll back the other socket when either address family has a real bind conflict so hostname resolution cannot select a different owner. An unavailable IPv6 stack may fall back to IPv4; never replace explicit loopback binds with a wildcard listener. Treat `localhost` and any loopback literal — including `::1` and every `127.0.0.0/8` address — as a generic Host alias, since the forwarder binds both families and a client may name either; preserve the original request `Host` upstream. Persist claim identity/timestamps and explicit pins in the private versioned routing policy so daemon replacement preserves ownership (ADR 183). The claim is nonetheless sticky across a blink: a lapsed route keeps its original discovery time and a route rediscovered within a short window has that time restored, so earliest-claimant ordering returns the generic host to its previous owner instead of stranding it with whichever node happened to be present during the gap.

Host routing at `{node-slug}.localhost:{guest-port}` always chooses the named node, so several nodes can use the same guest port regardless of the generic claimant. Traffic travels over one persistent Go SSH client per running Lima node. Take the generated `ssh.config` path from Lima's machine-readable instance observation, but resolve its containment trust root from CodeLima's configured `LIMA_HOME`; `limactl list --json` does not own or need to repeat that process configuration. Parse the localhost endpoint and identity only from that Lima-owned config, validate its ownership, permissions, and containment under the resolved home, and do not modify global SSH configuration. Discovery accepts IPv4 and IPv6 guest-loopback listeners, so each new tunnel connection tries `127.0.0.1` and then `::1`; keep accepted discovery addresses and dial targets aligned. Host bind conflicts remain observable and are retried every reconciliation.

Forwarding state degrades before it disappears. A peer is healthy while its
listener scan succeeds, becomes suspect on the first failure or missed
keepalives, and lapses only after repeated failures span a grace window: lapsed
routes and their host listeners stay bound and answer a retryable 502, because a
closed listener is a connection refusal that clients cache. Remove routes only
on a confirmed stop or delete — absence from a successful node list, or a
not-running observation that persisted through its own grace window while the
guest also stopped answering. A guest that still answers its scan outranks a
stale or watch-synthesized cache entry indefinitely; a failed node list is not
evidence that any node stopped, so retain every peer and route, keep working the
known nodes, and report the condition in the daemon snapshot. Detect a
dead-but-not-closed peer with SSH keepalives and close it after a few misses so
blocked work fails at once and the next pass reconnects and re-points existing
routes; bound every proxied dial for the same reason. Reconcile nodes
concurrently under a small cap with per-node backoff that also rate-limits its
warnings, so one unreachable node cannot freeze the others.

Reuse that same one-second SSH peer for per-node guest telemetry instead of
starting another runtime process or multiplying samples by TUI client count.
Sample telemetry in its own guest command, separate from listener discovery, so
a slow or failing probe can never affect routes. Read the aggregate Linux
`/proc/stat` CPU counters, exclude `guest` fields already represented in
`user`/`nice`, and calculate busy
percentage only from two valid consecutive samples. Normalize aggregate CPU to
`0..100%`, clear it on resets, peer loss, or stopped nodes, and attach the
sample only to transient runtime observations returned by the daemon. Collect
instantaneous memory and disk values in that same observation: define used
memory as `MemTotal - MemAvailable`, and use `df -Pk /` for the guest root
filesystem so a host workspace mount is not confused with VM-disk consumption.
Reject malformed, overflowing, or used-greater-than-total values independently
so one missing metric does not disrupt listener discovery or the other metrics.

Explicit `HOST:GUEST` ports remain an advanced raw-TCP escape hatch selected at
node creation and frozen in node metadata. The daemon owns their listeners and
uses the same guest tunnel as discovery (ADR 182). Gate application routes on
bootstrap/lifecycle readiness without gating probes. Close listeners and cancel
dials outside the forwarder mutex; track and drain accepted connections, preserve
TCP half-close, and never close a shared peer from an individual relay. Reserve
the whole host port for TCP and report overlapping HTTP routes as conflicted.
Legacy Lima forwarding migrates through noninteractive `limactl edit` before
booting a stopped node; never stop a running node merely to change ownership.

Project Ports from cached routing and inventory under the short routing lock;
never trigger guest/terminal inspection for a UI query. Enrich labels from
optional bounded telemetry and clear them when probes fail. Serialize durable
pin writes with route identity changes, write before acknowledgement, and keep
filesystem operations outside the routing lock. Only the current daemon owner
may write; freeze discovery identity and flush behind the update barrier, resume
on rollback, and reject late shutdown writers.
Preserve corrupt preferences instead of replacing them with defaults. Frontend
mutations have separate generations from reads, never retry uncertain replies,
and never steal navigation on completion. Activity deduplicates physical
node/host-port endpoints while Ports retains protocol-specific rows (ADR 183).

## Pressure-triggered host integration recovery

Host integration workarounds use a sample, threshold, actuate, verify, and cooldown controller rather than an unconditional timer. The macOS VirtioFS controller samples the system file table, acts only at the configured pressure boundary, compares the post-action sample, and applies the same 30-second cooldown after every attempt. Its lifecycle is owned by the daemon and its latest state is included in the daemon snapshot.

Guest cache reclamation stays scoped to running mounted nodes and to clean filesystem metadata (`drop_caches=2`). Do not add `sync` or page-cache dropping to this pattern: unrelated dirty-write policy and broad guest memory reclamation are outside the descriptor-pressure problem.

## Runtime observation versus durable lifecycle

`node.yaml` persists CodeLima-owned lifecycle and stable runtime configuration. Running/stopped truth discovered from Lima and live CPU, memory, and disk samples are attached as a transient `RuntimeObservation` and are not written during ordinary reads. The daemon owns one `limactl watch --json` process, seeds an observation cache with one direct list, and serves recurring TUI and forwarding refreshes from that cache. Lifecycle operations reconcile directly after mutation and update the cache. Mutating reconciliation persists only at explicit lifecycle boundaries.

An incomplete node directory without `node.yaml` remains discoverable through `doctor` and `node cleanup-incomplete`. Persist `sandbox.ref` plus its exact instance index before runtime create or clone so applied cleanup can tear down only that matching Lima instance before deleting its durable reference. Deferred rollback closures must capture the immutable target node, never a named return value that can become zero-valued on error.

## Terminal identity and actors

Keep running PTYs nonblocking across every operation, including geometry
changes. `os.File.Fd` can restore blocking mode; a wrapper that calls it is
unsafe even if it only issues an ioctl. Use `terminalio.Resize`, which holds
the descriptor through `SyscallConn.Control`, and `terminalio.Descriptor` for
descriptor observation. Test idle handoff after real PTY resizes: output-only
handoff tests miss readers that stop observing cancellation after a resize.

Printable host input need not have a physical keyboard identity. Preserve its
observed UTF-8/codepoint/modifiers/action and let Ghostty encode an unidentified
key when no mapping exists. Test decoded legacy text as well as synthetic
physical-key events; never guess US-layout positions to recover Unicode text.

The `internal/codelima/terminal` package owns typed `TargetKey`, `TerminalID`, and terminal kinds. Schema-v4 front ends create node targets only. Guest and host tabs share `node:<id>` while their kinds distinguish `node-shell` from `node-host-shell`.

Every managed terminal starts from `Service.TerminalLaunchSpec`. Guest shells re-enter `codelima shell <node>`; host shells start in the node's canonical directory. Terminal creation resolves this durable node metadata without live runtime reconciliation: host shells do not depend on Lima availability, and guest-shell child commands perform their own runtime checks. Front ends never assemble launch commands independently.

Daemon terminal sessions are serialized, pure-Go owners of the shell, PTY,
ordered renderer journal, and immutable snapshot cache. Every Ghostty instance
runs in a dedicated, separately packaged renderer-worker process. The PTY
reader applies backpressure only to its own PTY when the bounded renderer
mutation lane is full; do not convert ordinary producer pressure into renderer
failure or broadcast one terminal error per rejected chunk. Keep tracked health
and lifecycle calls on a separate control lane so a non-returning native call
still causes terminal-local replacement while daemon control and other
terminals remain independent.
Fence renderer frames by role generation, use stable journal-event IDs, and
deduplicate renderer-generated PTY writes by source event plus ordinal before
committing them. Suppress renderer-generated application responses while
replaying old output; an omitted uncertain response is safer than duplicating
input to the live application. Fire-and-forget mutations retain unique IDs for
that deduplication but emit no response frame. Periodic health requests must
reach the same actor as Ghostty calls; a socket-only worker ping does not prove
native progress. Track the journal replay high-water mark across replacement so
a waiting PTY chunk is applied either by replay or by the new live link, never
both. A renderer failure may stale one cached screen; it must not change that
terminal's shell PID or delay another terminal or daemon status.
Renderer mutations acknowledge after applying to the actor and enqueue a
capacity-one dirty edge only when the renderer invalidates visual state; key
encoding must not publish an unchanged pre-echo screen. A separate publisher
builds the full immutable snapshot/read bundle at a bounded active cadence and
does not tick while idle.
Publish initialization and explicit recovery requests immediately. Work that
arrives during publication must retain one trailing edge so the newest state is
eventually visible. Log renderer failures and latency outliers, not normal
per-call start/completion pairs.

Clients send commands for input, resize, snapshot, focus, clipboard, and close;
they do not call Ghostty or PTY state concurrently. Normal read and snapshot
RPCs return immutable cache data only. Dirty notifications are edge-triggered
and clients pull snapshots, preventing redraw loops. Broadcast the fresh cache
publication once; do not precede it with a second stale dirty notification.
Focus RPCs are transition-driven rather than repeated by periodic TUI refresh.
Treat snapshot sequence, not only output generation or geometry, as the client
redraw identity so cursor- and viewport-only changes remain visible.
Each PTY read-pump generation leases its file wrapper, numeric descriptor, quit
channel, and completion channel at launch; it never follows mutable terminal
fields into a rollback generation. Handoff stops input admission, drains
accepted writes, signals that exact pump, consumes its buffered boundary output,
and observes completion before closing the outgoing PTY (ADR 131).
Never poll immutable
terminal grids on an idle timer: route dirty notifications through the
single-owner UI loop, retain only a dirty version for hidden tabs, and let the
active tab wake a capacity-one snapshot worker with a bounded frame cadence.
Keep terminal input off the TUI event loop: enqueue semantic events onto one
FIFO worker per daemon terminal, stop admission and drain accepted input on
detach, surface asynchronous delivery failures, and redraw payload input only
after a fresh snapshot arrives. Explicit tab close also stops admission
synchronously, but its accepted-input drain and bounded, single-shot daemon
close request run after the client-owned tab has been removed so remote latency
never holds the TUI event loop. Detach remains a synchronous drain because it
preserves the daemon-owned terminal. Preserve semantic input boundaries across
the client/daemon seam: buffer Vaxis paste keys until `PasteEndEvent`, send
one valid UTF-8 semantic paste of at most 64 KiB, and let the isolated renderer
encode it with native safe-paste policy before admitting the complete output
as one PTY write. Reject oversize/full-queue requests without partial input;
never split a bracketed paste into independently admitted chunks. Preserve LF
in paste data; a pasted newline is text, not an Enter key.

## Bounded native terminal adoption

Only the renderer executable imports `internal/ghostty`; portable data and
process ownership live outside it. Native source, patches, features and build
profile are an exact identity checked at compilation and worker initialization.
Never discover arbitrary shared libraries or silently substitute an encoder.
Use owned bulk results and explicit native errors, keep C callbacks bounded,
copy buffers before freeing them, and free every handle on cancellation/close.

Publish cells, metadata, graphics and visible text in one actor-owned cut.
Only immutable owned values cross into asynchronous caches. Fence all results
by terminal identity, renderer generation and request/query revision, including
graphics chunks and search completions. A timed-out reader can detach while
the native command keeps its independent hard liveness deadline.

Wrap opaque native checkpoints with exact build/policy identity, integrity,
complete presentation state and a contiguous applied journal watermark.
Validate into temporary state before commit, retain an independent raw fallback,
bound encoded and decoded allocations separately, and enforce aggregate quotas
in addition to per-terminal limits. Do not checkpoint while native image state
exists: absence of a visible placement is not absence of image state.

Effects need an owner, delivery policy and replay policy. Clipboard writes go
only to the seat holder's current event connection, never every observer, never during
replay, and never with a false host-completion acknowledgement. Guest metadata
is sanitized presentation text, not authority to navigate, notify the desktop
or execute commands. Local host-color discovery is cancellable, bounded and
off the UI actor; unspecified defaults are not RGB black.

Keep expensive graphics fetch/decode/raster work on one frontend-owned worker,
clip before allocation, bound aggregate scenes, and upload under a per-frame
wire-byte budget. Own host image identities independently of guest IDs. Release
placements on tab/overlay transitions, assets on replacement/close, and discard
stale results before installation. Do not leak image encoding goroutines into
an unbounded redraw path.

Terminal tabs are independent terminal instances attached to one node target. Per-target state keeps ordered tab IDs, a monotonic counter, active selection, and the last open error. Selecting a running node ensures a terminal tab, reusing any existing node tab and opening a guest tab only when none exists. `Option+t` adds another guest tab and `Option+Shift+t` adds a host tab; all use the same switch, move, close, refresh, and active-tab paths. Moving swaps the active identity with one adjacent same-target tab without wrapping or changing activation. Never open a guest tab for a stopped node.

Use `shell` and `host` as unnumbered tab defaults for empty titles, common
shell names, and `user[@host]: /path` or `user[@host]: ~/path` prompt titles.
Classify sanitized text before truncation; preserve unfamiliar titles, URIs
and task separators (`|` or `·`). OSC titles do not identify their source, so
this is a narrow presentation heuristic, not process detection. Retain bounded
application titles with `host:` on host tabs; append status badges separately.
Titles may change or coincide; they never replace durable terminal IDs or
determine tab ordering, selection, or lifetime (ADRs 143 and 145).

Push terminal metadata with sequenced dirty events so hidden-tab titles and
bells update without full-grid snapshot requests. Keep a separate metadata
sequence in the client so delayed screen replies cannot roll labels backward;
authoritative synchronization resets that sequence. Track acknowledged bell
counts on the TUI's session record, preserving them across reorder/reconnect
and discarding them on close. Only the visible terminal in the focused window
acknowledges bells; never reset the daemon's cumulative count (ADR 144).

A path-scoped TUI node list is a projection, not an authoritative deletion set. Before refresh reconciliation closes a daemon-owned tab that is absent from the projection, confirm the node is missing or deleted in the global metadata store. Retain tabs on ambiguous metadata failures; explicit stop/delete operations remain the immediate close path. Apply the same rule whenever a scoped client can otherwise destruct shared daemon state.

Runtime maps are never an ordering source. Keep a daemon-owned ordered terminal-ID slice beside the runtime map and use it at every state boundary—list response, snapshot, session persistence, respawn, or live handoff. Persist order in the existing ordered session array, restore in that array order, and use durable creation time plus an identity tie-breaker only to normalize legacy or partially constructed state that has no explicit order.

## Incident capture before recovery

Cross-terminal failures must be captured while the original daemon is still
running. Keep incident tooling read-only: probe the daemon control plane
independently from at most one terminal actor, copy metadata and bounded log
tails, collect a non-terminating native process sample, record every probe exit
status, and continue when one probe fails. Never rebuild, stop, update, signal,
take input ownership, or send terminal data before the capture completes.

The installed Go collector is shared by CLI, System and the build-free skill
wrapper. Default installed captures go under the metadata home's private
`_diagnostics/` directory; repository captures use project `tmp/`. Do not
upload automatically, and warn that logs, paths and terminal contents may be sensitive. A
successful capture command means the bundle was created, not that the daemon is
healthy. Interpret observations before hypotheses: a responsive status/list
surface with a failed cached read narrows investigation to that terminal's
session/cache/renderer; a responsive cached read is not proof of live rendering.
Record probe failures and partial output, cap command/file/log bytes, and give
each probe a deadline. Cancel only a probe's owned subprocess group, never the
inspected daemon/renderer. Native sample raw files stay within the private bundle
and are removed after retaining a bounded report (ADR 181).

## Exact daemon protocol and live update

The private Unix-socket daemon protocol is newline-delimited JSON with an exact binary-version handshake, a bounded frame size, peer-credential checks, typed error responses, and explicit input ownership. Any request-shape change bumps the protocol version; semantic paste was introduced in protocol 3, and daemon-owned tab movement is protocol 4, preventing stale daemons from accepting mutations they cannot interpret. Interactive TUI connection setup must finish by claiming input ownership, even when `hello` reports an existing owner; this makes the newest interactive TUI authoritative and prevents delayed terminal opens from discovering an observe-only connection. Because handshake ownership becomes stale when another TUI connects, every TUI window-focus event sends the separate idempotent `input.takeover` request before subsequent user input. Revocation is an expected internal handoff and must not be rendered as a TUI error; surface only a failed takeover. Do not recover by replaying a failed terminal mutation. Apply read deadlines to protocol phases that must make prompt progress: the initial handshake and event subscription. Clear the server-side deadline for authenticated request clients and subscribed event clients because a valid TUI session may be idle indefinitely. The request client must likewise clear its connection-wide handshake deadline before starting the long-lived response reader; an absolute deadline applies to future reads until explicitly cleared. Bound established request writes and pending calls individually. A client event reader may use bounded idle reads, but it retries only timeout errors; context cancellation, shutdown/update commit, and permanent socket errors terminate the reader once. Schema/session format changes bump their own version constants.

Treat every physical daemon connection as disposable. One stable logical
client identity may advance through many connection generations. After EOF,
sequence gaps, epoch changes, missed application heartbeats, update commit, or
permanent protocol failure, reject new mutations while the connection
supervisor dials, authenticates, and installs one compact authoritative state
cut. The TUI actor acknowledges that it installed the cut before the supervisor
may return to ready. Reclaim input as part of synchronization, but continue to
reject new mutations until that acknowledgment. Replace the full terminal set
from the cut; never infer deletion from a partial event stream.

The request connection has one response reader, an ID-keyed pending registry,
and serialized writes, so concurrent queries and mutations do not assume
response order and one blocked query cannot hold terminal input or ping behind
a client mutex. Never replay a submitted mutation after reconnect. Report
not-sent and outcome-unknown delivery separately.

Input takeover is applied synchronously on the connection reader, before any
later frame is dispatched to a lane. A following input frame must not race ahead
of the takeover merely because the takeover itself does not touch a terminal.
Input itself is ordered per terminal lane, so a takeover cannot be ordered by
sharing one of those lanes; only the reader sees every frame in wire order.

Every daemon client connection owns a bounded outbound mailbox and one writer
pump. Broadcast is nonblocking enqueue only; queue overflow or a write deadline
disconnects that client without touching terminal sessions. Preserve the first
failure cause and correlate both ends with connection ID, logical client ID,
daemon epoch, phase, initiator, queue depth, last progress, and byte/frame
counters. Later EOF and broken-pipe errors are consequences, not replacements
for the first record.

Daemon terminal sessions are recoverable cache state, not authoritative node metadata. `restore: forget` replaces that cache without parsing it. Under `restore: respawn`, an unsupported or malformed session is atomically renamed beside the live path, a fresh current-version session is written, and startup continues with a warning. Keep strict schema rejection for authoritative stores; use quarantine-and-reset only for state whose loss changes convenience rather than data truth.

Live update transfers PTY file descriptors over an authenticated, length-prefixed Unix stream with SCM_RIGHTS. Never assume stream read boundaries equal writes: frame every bounded JSON payload and collect ancillary descriptors while reading the exact frame. Keep the manifest metadata-only and transfer bounded renderer replay in ordered per-terminal chunks; raw replay that fits its journal can exceed one frame after JSON base64 expansion. Validate per-terminal and total replay allocation before accepting chunks, require contiguous offsets, and reject descriptors on replay or control frames. Reject duplicate or unknown terminal descriptors rather than ignoring or leaking them. The CLI resolves and sends its own executable even when no path is given; never resolve an omitted replacement inside the old daemon. Daemon lifecycle management alone may authenticate with the running daemon's persisted protocol-2-or-newer identity to cross an exact-version boundary: update explicitly takes input ownership, while startup recovery may only stop the matching daemon. Import is staged, acknowledged, and committed; a failed import rolls back to the old daemon. Only the exact legacy-Darwin `unixpacket` unsupported error may fall back to a clean stop/start; do not turn generic handoff errors into destructive restarts. Integration paths and unit tests that create Unix sockets use `internal/testutil.TempDir`, which keeps disposable paths short and under the project-rooted `tmp/tests` directory so Darwin's smaller `sun_path` limit is exercised portably.

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

Composite lifecycle operations retain one node liveness token across their
runtime phases. Internal helpers require that ownership and perform the same
uncached checks as standalone operations; they must not reacquire the token.
Persist phases before their side effects and tee each runtime stream once.
Caller exit stops waiting without canceling admitted work. Detail actions use
the open node, never retained fleet marks (ADR 185).

Empty states are projections of known inventory, not substitutes for a failed
filter or unavailable data. Reuse cached prerequisite reports and asynchronous
metadata loading; never run checks while drawing. Separate read-only re-check
from repair, keep keyboard/mouse/palette actions on the same guarded path, and
make complete diagnostics scrollable at compact sizes. Creation defaults must
use the scope advertised by the view; retain the launch directory as the
all-node fallback (ADR 184).

Apply outer resize events to Vaxis before deriving terminal pane dimensions
or drawing. Geometry is global state: process it before overlay input routing
and even when no terminal session exists. Otherwise the next draw computes
the old pane size and sends it back to the daemon. Resize regressions must
exercise `handleEvent` through drawing with real Vaxis buffers, including
overlays, shrink/grow cycles, minimum-size recovery and pixel-only changes;
checking only a fake terminal's immediate resize call misses this reversal.

Keep shortcut identity separate from event activation. Vaxis key matching
ignores press/repeat/release. One-shot actions use `tuiKeyActivates(key, true)`;
navigation uses `tuiKeyActivates(key, false)` to admit press and repeat only.
Global shortcuts declare `pressOnly` in the binding table so the payload
predicate still recognizes their releases. Dispatch every key through
`handleKey`, including overlays and search. Claim one-shot global shortcuts
and keys that change overlay/search/focus, consuming their repeats/releases
before the newly exposed surface can interpret them. Track the key identity
without modifiers because the modifier can be lifted first. A fresh press
clears the old claim immediately; avoid timing-based debounce. Ordinary typing
and navigation do not accumulate claims in legacy protocols without releases.
Handle paste as text before action matching, deliver both paste boundaries to
the active input, and preserve ordinary shell repeat/release events and its
deferred redraw policy (ADRs 134, 138 and 139).

The production TUI loads the daemon's flat node inventory on lifecycle pushes with a slow safety refresh, then scopes it by canonical directory containment. Apply one-second telemetry pushes in place without triggering inventory reads. Project fleet selection, marks, filtering and sorting by node identity; use the same projected rows for rendering and mouse hit testing. Configuration and environment views are global.

Retain prompt idle intervals in the daemon terminal publisher, using native
prompt evidence and the PTY owner's accepted-input timestamp. Never use a title,
quiet output or frontend arrival time as an idle signal. Preserve the interval
in the bounded Activity handoff baseline; suppress current claims when stale.
Keep renderer query replies separate from user input and preserve response
deduplication before accounting. See ADR 189 and `make test-prompt-idle`.

Project fleet evidence independently of paged Activity and full Spec/receipts.
Bound node IDs, text and recent counts; preserve step kind, execution node and
attempt time. Read only visible nodes plus selection, cancel on navigation,
and fence responses by request identity. A recovered interrupted receipt has
no finish timestamp: stale in-flight classification requires an unclaimed
lifecycle token and the same observed metadata revision; a newer running ID
takes precedence. Positive terminal progress/prompt signals may describe work,
but titles and absent signals never prove busy/idle state. Keep guest stop impact
separate from host and unclassified sessions, including hidden marks (ADR 187).

Keep richer preview data selected-node-only. Copy a bounded minute from the
existing metrics owner instead of adding history to every inventory row or
probing in a renderer. Share a bounded regular-file parser across Spec and
runtime inspection, but retain their distinct sources: rendered recipe versus
effective instance configuration. Match workspace host and guest paths before
showing access mode; absent capability fields remain unknown. Fence the result
by node identity and metadata revision (ADR 188).

Project agent validation from frozen command/attempt records, not an overall
successful start or current environment definition. Recheck command coherence
on a local copy, preserve newer pending checks and earlier failed prerequisites,
and distinguish inherited execution. Recognize exact built-in version commands
for login-user claims; arbitrary profile checks stay labeled as such. Keep the
projection selected-node-only and read-only, with no commands/output on the
fleet wire and no guest I/O in rendering (ADR 190).

Selectable detail panels retain canonical row IDs, not positions in the latest
snapshot. Reordering preserves selection; removal invalidates actions until
explicit navigation selects a replacement. Capture identity in confirmations
and validate it again on submit. Derive mouse regions from the exact rendered
rows, keep panel focus separate from global/detail tab navigation, and share
the existing inventory and route-readiness owners. Retain daemon creation time
through frontend adoption instead of deriving session age from a TUI's lifetime
(ADR 191).

An inventory-change push must refresh the cached projection, not just redraw
it. Coalesce reads, fence replies with a mutation generation, and separate an
order-only update from membership adoption/removal. A delayed ordering response
may rearrange known IDs but cannot resurrect closed records or discard records
created since the read. Full synchronization invalidates outstanding order
reads (ADR 191).

Retain aggregate metric history once in a bounded daemon-owned ring. Use an epoch and timestamp cursor for incremental frontend reads, deep-copy observations across ownership boundaries, and preserve missing intervals. Optional detail probes share the existing guest transport, use bounded output and deadlines, coalesce concurrent requests, and never affect route health. Fence frontend responses by request serial and navigation generation; stop requesting detail when its view is hidden or paused (ADR 155).

Bounded presentation animations stay inside the UI event loop. Derive their frame from monotonic elapsed time so delayed events cannot rewind visible state, keep changing text cell-width stable, and stop their ticker as soon as the terminal state is reached. When only a small fixed region changes, update that region and use the normal Vaxis diff render instead of clearing and rebuilding terminal, tree, and overlay surfaces. Keep pure elapsed-time-to-frame behavior separate from the event producer so progression tests do not sleep.

Browsing nodes uses manager mode and never creates a shell. Raw attachment
suspends the manager; embedded terminal mode is explicit. Preserve selection
by node identity across refresh and detach. A VM becoming ready updates its
available actions without changing focus or creating a terminal.

A session chooser captures canonical node and terminal IDs. Keep numeric
shortcuts stable for its lifetime: retain ended rows as unavailable and append
new arrivals. Render mouse targets with those IDs and revalidate on activation.
Revalidate the captured node again after an asynchronous preference save; never
look up the newly selected row. New-session actions explicitly request creation,
while ordinary default attachment may reuse. Probe launcher availability once
per configuration/environment, then repeat preflight before session creation.
Use observed session metadata for activity and age; do not infer agent identity
from titles. See ADR 194.

Diagnostic file views read bounded local tails outside the event loop, without
runtime preparation. Fence completions by both navigation generation and
request serial, cancel on pause or navigation, and keep record identity across
wrapped screen rows. Text inputs own paste and numbers before global keys.
Strip terminal controls before drawing logs, and invoke external tools with
literal argument vectors while the TUI is suspended (ADR 157).

When a terminal has not negotiated reliable grapheme widths, treat a complex
cluster's reserved cells conservatively: clear its span and explicitly
position subsequent output. Do not rely on the host cursor advancing by the
same width as the model, or replace the grapheme with separated code points
(ADR 158).

Dialogs, selectors and menus occupy centered overlays and scroll the focused
control into view. `Tab`/`Up`/`Down` move between dialog fields, text inputs
receive `Left` and `Right` for cursor movement, and `Right` chooses only when
the field exposes an activation callback. Selector labels may carry decision
context while values remain stable identifiers. Long mutations run outside
the event loop with resource keys that reject conflicting work while
navigation, refresh and other terminal sessions remain responsive.

Keep an input's implicit default separate from its editable content. Render the
default as a muted prompt only while content is empty, return it as the field's
submitted value, and remove the prompt as soon as the operator types. This
preserves first-keystroke replacement semantics without duplicating defaults in
validation or submission callbacks.

## Host clipboard delivery

Deliver private clipboard effects only in the owning frontend. The TUI passes
OSC 52 to the outer terminal for both local and remote sessions; availability
of a native clipboard command must not override this abstraction. Do not claim
confirmed delivery for an unacknowledged terminal clipboard request.

The TUI's input lease belongs to its request connection; its event subscription
uses a separate connection with the same frontend instance ID. Register the
newest successfully synchronized event connection for each instance and fence
clipboard queue admission against both that recipient and the physical input
lease. Retain the recipient ID until the last connection for the instance
closes, so a disconnected successor cannot reactivate a stale event stream.
Reconnect snapshots never replay clipboard effects. See ADR 148.

A mouse release without a native selection is an empty successful interaction;
skip clipboard delivery for empty selection results. Normalize only the native
no-value result for this expected gesture and preserve other errors (ADR 149).

## Shared template drafts and command boundaries

Use one domain editor for create, clone and update entry points. Parent overlays
retain their drafts; child completion changes the parent only after explicit
apply. Load definitions and usage asynchronously and address completions to the
exact editor. Save through the service, preserving drafts after rejection.

Resolve preview order through the same pure environment/template resolver used
by node creation. A multiline command is one execution block: retain its newlines
inside one buffer, and make adding/removing/reordering blocks explicit. Persist
empty environment and command arrays explicitly so creation cannot accidentally
inherit defaults. See ADR 175.

Recover bracketed-paste control events before handing text to an editor, and
normalize CRLF after the complete paste. Script buffers opt into the vendored
text input's `PreserveTabs`; generic text widgets otherwise expand literal tabs
into spaces. Exercise parser-shaped LF/CR/tab events as well as whole-string
paste fixtures, then verify a real terminal paste.

## Global recipe inspection

Derive recipe usage from global stored nodes and configurations, independently
of the scoped fleet. Share frozen/current comparisons with Node Spec and read
execution outcomes only from recorded bootstrap steps. A clone's inherited
steps belong to their original execution node. Include deleted records for
history without counting them as current template users. Keep identity separate
from row indices through refreshes, sorting and responsive panel changes.

An external command editor operates on one command block in private owned
scratch, then returns to an unsaved draft. Parse executable arguments without
shell evaluation, bound the returned file and clean up on every outcome. Reuse
the pager's foreground suspend/resume path. See ADR 176.

## Disposable operation cleanup

Admit a server-generated frozen plan and durable cleanup intent before creating
a disposable resource. Keep execution cancellation separate from cleanup
cancellation. Recover teardown only, never commands whose partial effects are
unknown. Preserve the original outcome and persist successful cleanup with the
terminal receipt in one write. Verify exact resource ownership before deletion;
a previously deleted runtime name may have been reused. Pending cleanup must
not expire with ordinary history retention. Resume output from the retained
tail and line offsets so recovery diagnostics cannot discard earlier command
evidence. See ADR 177.

Expose retained operation output by receipt identity rather than requiring a
live node. Fence async output replies by the visible viewer instance. An
ambiguous admission acknowledgment retains its original request ID; a typed
definitive rejection can release it for a new request.

## Static recipe hints

Analyze shell syntax in the asynchronous metadata snapshot, without execution,
environment expansion or runtime probes. Distinguish source evidence from guest
state, and report unavailable analysis explicitly. Bound parsing and merge
control-flow paths conservatively; background or conditional work cannot
silently establish a prerequisite. Derive provider hints from current saved
definitions rather than built-in names.

Keep suggested edits as exact-source byte insertions with a stale-source check.
Open a shared unsaved draft, preserving normal apply/save/discard controls.
Do not reformat unrelated shell text or save a suggestion implicitly. See ADR 178.

## Global diagnostic snapshots

Keep global findings independent of view-local data. Bound asynchronous checks,
suppress overlap, fence completions and retain the last successful snapshot
with its observation time when refresh fails. Unknown or stale diagnostics
must not render as a clean zero. Separate completed repair messages from open
findings by re-checking; retain repair evidence in Activity. Only explicit user
actions may request repair. See ADR 179.


## Cached daemon overviews

Keep routine status RPCs separate from evidence collection. Return typed cached
observations and explicit unavailable support; never render terminal grids or
probe guests just to draw a global panel. Count logical subscriptions rather
than transport connections, and label observed units precisely. Bound and
coalesce visible-view polling, cancel it on navigation, fence reconnect
completions and preserve prior evidence with an explicit stale timestamp.
See ADR 180.

## Explicit daemon actions

Share lifecycle helpers between CLI and TUI, but make fallback policy explicit.
An action promising live-session continuity must refuse a restart fallback.
Guard explicit shutdown with the observed daemon epoch, wait for ownership
release, then start its successor. Treat a lost mutation reply as an unknown
outcome and never automatically resubmit it. Only one lifecycle worker runs
per frontend; diagnostic and log workers remain independent. Post results onto
the event loop, fence shutdown completions, and retain saved evidence paths
without replacing a different foreground view. Closing an evidence view does
not cancel its capture; quitting the frontend does (ADR 181).

## Framed terminal viewport and session-strip identity

Keep terminal decoration outside the child window handed to the renderer.
Share initial and resize dimensions (`tuiEmbeddedTerminalSize`) and derive the
mouse rectangle from that child's actual origin and size. Focus changes must
not change dimensions or create another terminal owner. Keep the active session
visible by identity, with metadata and bell acknowledgement shared across tab
presentations. Capture node/session/terminal IDs in mouse targets and revalidate
them on click; an ended target must never select its replacement (ADR 195).

## Destructive review identity and evidence

Keep a confirmation's canonical node ID and original displayed name through
submission. Validate action availability before mutation and again after an
asynchronous prerequisite such as a settings save. Exact-name confirmation uses
the original input string, without trimming. Discarding this disposable input
needs no second confirmation. Bind optional inspection to a specific terminal
ID; an ended target must not create or select a replacement. Use known terminal
kind and fresh activity evidence for impact, and preserve unaffected host tabs
when a guest operation completes. Share the generic input and submission model
inside a dedicated domain review instead of adding lifecycle behavior to every
dialog. Revalidate the current root terminal size before activation: the global
minimum-size screen may skip the overlay's Draw method entirely (ADR 196).

## Clone before optional child startup (ADR 197)

A clone's deferred source restoration must finish before the child boots. Keep
clone execution separate, bind the committed child under the existing daemon
receipt, and reuse normal startup phases. Explicit cancellation tracks a child
startup UUID separately from the receipt's original source UUID; do not infer
cleanup from optional-start intent. Bound list summaries retain intent and
current phase, while startup projections recognize the child only after its
first startup phase. Return the committed node on a later startup failure.

Reuse captured-node reviews for clone inputs. Load all names off the UI loop,
apply only to the same live review, preserve edits and let the service arbitrate
final uniqueness. The checkbox belongs to this request, not global settings.

## Use explicit singular and plural forms for visible counts

Use `countLabel` for a number with its noun and `pluralForm` for a phrase whose
verb or pronoun also changes. Supply both forms explicitly, including irregular
words. Exactly one selects the singular; zero selects the plural. Fleet, review,
Activity and resource summaries share these helpers so a one-member batch reads
as one node throughout its review and retained display label.

## Finish deleted-target session cleanup before releasing operation ownership (ADR 199)

Use the committed node tombstone as the authority for terminal teardown, even
when a subsequent event write fails. Invoke cleanup inside the daemon operation
owner before receipt finalization; requester cancellation must not skip an owed
cleanup. Retain sessions when deletion did not commit. Remove the complete target
set under the terminal mutex, then share ordinary close persistence, publication
and bounded tracked runtime teardown under the update barrier.

Resolve reusable slugs to canonical identity before launching a terminal. Recheck
that identity under the same mutex just before publication: deletion either sees
the published entry or the late publisher sees deletion and closes its runtime.
Cold restoration must pass the same admission gate. Do not hold metadata locks
across runtime launch or acquire the update gate while holding the terminal mutex.

## Admit a complete batch before running its members (ADR 198)

Persist an ordered root receipt containing captured node IDs/names, action kinds
and preallocated child receipt IDs. Validate and reserve the entire set before
acknowledging admission. Persist each ordinary child receipt before executing it;
reuse its phase, output, cancellation and failure behavior. A failed member does
not prevent later members from running. Bound per-batch and global concurrency.

The root owns its context independently of the client; child contexts inherit
root cancellation but can also be canceled separately. Keep queued targets
reserved. On recovery, project completed child receipts into the root and mark
uncertain/unstarted work interrupted instead of replaying destructive actions.
Keep bounded member identities in active summaries for queued fleet evidence;
completed history summaries expose counts, with full outcomes loaded on demand.

An unfinished deletion is retryable only after a fresh, revision-matched
observation confirms that its claim is unowned and no batch still reserves it.
Offer a new explicit deletion review; let the service recover durable lifecycle
markers under its existing locks. Keep form validation visible across redraws
and non-editing input events, clearing it only when the field value changes.

When a provider has shared first-use initialization, gate only that initialization
under its resolved home. Let the provider create its own files, release the gate
once complete validated evidence exists, and allow long VM boots to overlap.
Use a persistent cross-process lock inode and context-aware waiting; never hold
a global metadata lock across the provider command or overwrite provider keys.
