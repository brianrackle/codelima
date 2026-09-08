# CodeLima architecture and implementation review — September 7, 2026

Status: review complete; recommendations proposed, not implemented.

## Scope and evidence

This review covers simplicity, stability, maintainability, speed, responsiveness,
and extensibility across the CLI, TUI, application services, metadata, Lima,
forwarding, daemon, terminal lifecycle, renderer, native bridge, and engineering
workflow. It also compares CodeLima's libghostty-vt integration with current
upstream source, including capabilities added after the existing August draft.

- CodeLima baseline: `44a3122f1343b07474d74b9610dd84db708e5fc2`.
- Current Ghostty pin: `ae52f97dcac558735cfa916ea3965f247e5c6e9e`, May 25, 2026,
  with the local 208-line patch in `scripts/patches/ghostty-vt-codelima.patch`.
- Upstream examined: [`82232ecde55405559dec29c5466cb9e39938cb41`](https://github.com/ghostty-org/ghostty/commit/82232ecde55405559dec29c5466cb9e39938cb41),
  September 7, 2026, 16:45:25 UTC. This is an audit snapshot, not a recommendation
  to track `main` automatically.
- The existing untracked `libghostty_plan.md` is an August 15 proposal against
  `cecf81678e47f967b0354acada67e69d229f436b`. It was read and preserved. The
  September findings and corrections below take precedence for this review.
- Evidence includes production code, representative unit/race/integration and
  fault-injection tests, public upstream headers and implementations, build
  scripts, QA, TODO, roadmap, patterns, and relevant ADRs. File line numbers
  below refer to this baseline; symbols are the durable lookup keys.

Findings labeled **defect** follow from a concrete code path or interleaving.
They are source-level findings unless a reproduction is explicitly reported;
passing existing tests does not demonstrate that an untested interleaving is
safe. **Risk** identifies an inadequately bounded or specified failure mode.
**Improvement** is a proposed simplification or optimization, not a claim of
measured production slowness. P1 means address in the next stabilization work;
P2 means the next architectural/performance work; P3 means optional expansion.
Effort estimates are relative: S is localized, M crosses a few components, L
requires a staged contract change. They are not delivery commitments.

## Recommended direction

Keep the daemon-owned shell and PTY, one replaceable Ghostty worker per
terminal, and Vaxis as the outer UI. Make ownership and cancellation consistent
at those boundaries, then remove duplicated terminal semantics using the
upstream C API. Separate application services and portable protocol values
from presentation and Ghostty dependencies. Do this through small, tested
changes; a wholesale rewrite would discard valuable fault-containment work.

```text
CLI / Vaxis TUI
    | typed commands, immutable views, ordered events
    v
Daemon / application services
    |-- metadata records and lifecycle operation ownership
    |-- Lima adapter / runtime inventory
    |-- forwarding peers / discovery / telemetry
    `-- terminal session: shell + PTY + bounded input/output + recovery journal
            |
            | bounded, versioned worker protocol
            v
        renderer worker (one per terminal)
            | thin Go/C adapter, exact native build
            v
        libghostty-vt: terminal state, encoding, formatting, search, effects
```

Preserve the delivered foundations: opaque terminal identities, ordered tabs,
geometry seat arbitration with shared input (ADR 130), reconnect and
epoch/sequence synchronization, bounded per-client output queues, independent
control traffic, dirty-driven visible snapshots, lazy read variants, encoded
snapshot caching, renderer restart containment, chunked handoff, scoped
metadata locks and lifecycle tokens, frozen configuration values, atomic file
writes, inode-based metadata caching, persistent SSH peers, and Lima watch
reconciliation. The read-pump descriptor-lease race in TODO #36 is already
resolved by ADR 131. Recommending these again as new work would obscure the
remaining problems.

## Prioritized CodeLima recommendations

### R01 — Give state events and heartbeat watermarks one ordering contract

**P1 · defect · M.** In [daemon/server.go](../internal/codelima/daemon/server.go),
`Broadcast` assigns a sequence under `s.mu` at line 450, then unlocks before
encoding/enqueueing. Its publication lock is shared, so publisher N+1 can
enqueue before N. `heartbeatLoop` also samples the revision independently.
A heartbeat carrying N can overtake the state event N.
`runDaemonConnectionSupervisor` in
[tui_daemon_connection.go](../internal/codelima/tui_daemon_connection.go):141
advances its sequence from either event type: a forward gap causes reconnect,
while an overtaking heartbeat can make the actual state event look duplicated
and be discarded.

Serialize revision assignment and publication through one owner; encode payloads
before admission and keep each client's enqueue nonblocking. Define heartbeat
watermarks so they cannot mark unseen state as applied. Preserve the sync
snapshot barrier and guarantee that rejected payloads consume no revision.
The tradeoff is one short publication serialization point. Test deterministic
barriers for two publishers and a heartbeat overtaking a publisher, then stress
many terminals and clients; assert exact order, no missed state, and no spurious
resync. This is a stronger contract than merely sorting received events.

### R02 — Use fresh runtime observations for every mutation precondition

**P1 · defect · S–M.** `NodeStart` uses the fresh path introduced in ADR 126, but
[service.go](../internal/codelima/service.go) still uses cached observations for
`NodeStop` (1535), `NodeClone` (1645), explicit-port preflight (1461), and
incomplete cleanup (1184). A stale stopped observation can make stop report
success while the VM runs. Stale clone state can select the wrong source
restoration policy; a stale negative cleanup observation can orphan a VM.

Make freshness explicit in the inventory interface and require it for decisions
that authorize runtime changes. Keep cached observations for display. For a
validated incomplete record, consider invoking the runtime's idempotent delete
on its recorded identity without a cached existence gate. Extend
`TestNodeStartIgnoresStaleRuntimeCacheWhenDecidingToBoot` to every listed path,
with both stale-positive and stale-negative results. Extra runtime queries on
mutations are an acceptable tradeoff; measure and avoid duplicate queries
inside a single operation.

Freshness alone does not reserve a shared host port: simultaneous starts of
different nodes hold different lifecycle tokens and can both pass the check.
Add explicit per-port check-and-reserve ownership through the start outcome,
with release/reconciliation on failure. Test simultaneous starts with a barrier;
the existing sequential conflict test is insufficient. This does not replace
handling an unrelated host process winning the actual bind race.

### R03 — Revalidate cleanup candidates after acquiring lifecycle ownership

**P1 · defect · S.** `NodeCleanupIncomplete` in
[service.go](../internal/codelima/service.go):1142 enumerates incomplete records,
releases the enumeration lock, later acquires the candidate's operation token,
and deletes the runtime before checking `nodeMetadataExists` at 1223. A create
can finish between enumeration and token acquisition. Cleanup then deletes a
valid VM and leaves its completed node record intact.

After acquiring the operation token, reread and validate the candidate under
its metadata lock **before** any runtime deletion. Retain operation ownership
through teardown and metadata cleanup; keep slow runtime calls outside global
locks. Test with barriers that finish creation after cleanup enumerates it,
then resume cleanup and assert zero deletes. The additional short validation
is simpler than repairing a valid node after a mistaken destructive action.

### R04 — Bound PTY input by bytes and report admission truthfully

**P1 · defect/risk · M.** `ghosttyPTYWriter.Enqueue` in
[tui_terminal_ghostty_cgo.go](../internal/codelima/tui_terminal_ghostty_cgo.go):467
appends to an unlimited `bytes.Buffer`; `nextChunk` copies the entire pending
buffer. A shell that stops reading can therefore grow daemon memory despite
bounded RPC and renderer lanes. `daemonTerminal.SendInput`/`Update` return no
error, while [daemon_host.go](../internal/codelima/daemon_host.go):375–446 can
report success even when the isolated terminal rejects input during quiescence
or an unavailable writer/queue prevents admission.

Use per-terminal and daemon-wide byte budgets, bounded chunks, and explicit
admission results. An acknowledged command means accepted into the owned queue,
not executed by the shell. Preserve accepted-byte order and never automatically
retry an ambiguous acceptance. Treat a paste as an owned semantic transaction
so shared clients cannot mix its framing. Reserve bounded capacity for terminal
query responses or define their failure policy. Test a non-reading PTY, many
concurrent senders, quiescence, close, partial writes, and large Unicode pastes;
memory must plateau and rejected input must be visible. Backpressure introduces
an overload outcome that callers must handle, but removes silent loss and OOM.

### R05 — Make SSH deadlines include opening the session

**P1 · defect · M.** `sshForwardingPeer.run` in
[dynamic_forwarding.go](../internal/codelima/dynamic_forwarding.go):169 calls
`NewSession` before entering its context select. In the pinned SSH module,
channel opening can wait for the peer indefinitely. A peer can continue
answering keepalives while withholding that reply. `dynamicForwarder.Close`
waits for workers before closing their peers (1683), completing a shutdown hang.

Give each peer an owner and a cancellation path covering channel opening,
command execution, output collection, and teardown. Close a wedged owned
transport before joining workers; bound command output as well as elapsed
time. Replace the custom `DialContext` wrapper with the pinned SSH client's
existing method, while retaining transport shutdown for truly stuck channels.
Test a real loopback SSH fixture that answers keepalives but never acknowledges
session-open. Scan and shutdown must finish and release goroutines. Closing a
peer can interrupt its other routes, so record the reason and reconnect only
that node with the existing backoff.

### R06 — Preserve the replay timeline and suppress all replay side effects

**P1 · defect · M; checkpoint extension L.**
[daemon_terminal_isolated.go](../internal/codelima/daemon_terminal_isolated.go):763
exports `rendererJournalReplay` as concatenated output, losing ordered resize
events. Import at 846 reconstructs one final resize followed by that output.
Even an untrimmed journal can therefore reconstruct a different screen after
geometry changes while retaining a false `ReplayPartial` value.
[renderer_worker_server.go](../internal/codelima/renderer_worker_server.go):312
suppresses replay PTY replies, but its clipboard event path at 333 has no
equivalent replay guard.

Transfer a bounded, versioned ordered event stream including resizes and all
state-changing terminal configuration. Make recovery quality explicit: complete
core checkpoint, complete replay from a known origin, or partial reconstruction.
Give every external effect a replay mode and deduplication identity; clipboard,
notifications, and future image-file requests must not fire again during state
reconstruction. Keep ADR 131's descriptor lease and ADR 113's chunk framing.
Test multiple width changes, incomplete VT sequences, journal eviction,
handoff rollback, and OSC 52 across restart. Ghostty checkpoints can later
reduce replay cost, subject to the exclusions in G11 below.

### R07 — Fence asynchronous TUI work and make completion delivery cancelable

**P1/P2 · defect/risk · M.** `startDataRefresh` in
[tui_app.go](../internal/codelima/tui_app.go):672 allows overlapping refreshes
after a stall or when a preferred selection is requested. Completion carries
no request generation, and `finishDataRefresh` always clears the latch and
applies the result. An older reply can overwrite newer membership or selection.
The code already orders usage samples; membership needs equivalent fencing.

Attach a generation and cancellation scope to refreshes and overlay loads;
apply only current results and retain the intended selection. Replace calls
to Vaxis's unconditional `PostEventBlocking` with an application-owned,
context-aware completion channel selected by the UI loop. Preserve reliable
one-shot completion while allowing producers to exit after UI shutdown.
Selectors in [tui_dialogs.go](../internal/codelima/tui_dialogs.go):44,172,229,495,569
still load service data synchronously from event handlers; load those off-loop
with a visible loading state. Test reverse-order replies, canceled overlays,
a full event queue during quit, and slow metadata. This adds a small task owner
but replaces several independent latches and prevents stale UI state.

### R08 — Close credential-copy failure windows

**P1 · defect · M.** `copyHostAuthToGuest` in
[auth_import.go](../internal/codelima/auth_import.go):741 returns on copy failure;
the cleanup trap is installed only by the later placement script. Earlier
copied secrets remain in guest staging. Sequential placement into two homes
and subsequent host completion persistence also have partial-failure windows.

Register cleanup immediately after staging creation and execute it with a fresh,
bounded context on failure. Use a private staging root; report cleanup failure
without exposing payloads and allow later recovery when the guest is unreachable.
Document partial placement accurately. Consider a guest completion receipt to
reconcile a completed import whose host metadata write failed, avoiding blind
credential overwrite on retry. Test failure of the second copy, cancellation
before placement, partial placement, and failed completion persistence. A
receipt adds a cross-boundary state, so introduce it only with a defined retry
contract. Preserve the explicit login-user/root execution boundary in ADR 129.

### R09 — Distinguish corrupt inventory from confirmed absence

**P1 · defect · M.** [store.go](../internal/codelima/store.go):965 intentionally
skips unreadable node records in `ListNodes`. The successful partial list reaches
`dynamicForwarder.syncMembership` at
[dynamic_forwarding.go](../internal/codelima/dynamic_forwarding.go):873, which
treats absence as deletion and retires otherwise working peers/routes.

Return an internal inventory containing valid records, unreadable identities,
and completeness/provenance. Presentation may show the valid subset, while
destructive consumers retain last-known state for an unreadable record and
surface degradation. Explicit deletion must remain authoritative. Test live
HTTP/WebSocket traffic while a node record becomes corrupt and is repaired,
then test real deletion. Retaining uncertain state requires visible diagnostics
and a bounded recovery policy, but prevents an unrelated parse error from
withdrawing healthy service.

### R10 — Put resource budgets at the allocation boundary

**P1/P2 · risk · M.** Existing queues bound individual connections and frames,
but that is not a daemon-wide budget. Audit terminal dimensions and their
product before native allocation, terminal/client counts before spawning,
request dispatch before goroutine creation, snapshot size before materializing
or encoding, and total replay/checkpoint/image memory. The snapshot body cache
in [daemon/server.go](../internal/codelima/daemon/server.go):181 even preserves
an oversized sole entry by design; admission must prevent an unbounded one.
Also bound client pending calls and aggregate per-connection input lanes;
count journal events and slice overhead as well as output payload bytes in
[renderer_journal.go](../internal/codelima/renderer_journal.go). Many tiny events
can exceed a payload-only memory estimate.

Define configurable conservative limits and explicit overload errors; use
separate control reserves so status, close, and health remain available.
Validate integer conversion and multiplication before converting to native
width/height types. Include max dimensions, many terminals, many clients,
oversized results, and sustained producers in tests. Limits must reflect
measured supported workloads; an arbitrary tiny cap would degrade normal use.
Do not mistake same-user socket authentication for resource containment.

### R11 — Make worker request deadlines agree with health supervision

**P2 · defect · S.** `rendererSupervisor.Read` in
[renderer_supervisor.go](../internal/codelima/renderer_supervisor.go):434 allows
four times the command timeout, but the health check at 622 applies the base
timeout to all pending requests through `HealthError` at 1210. A legitimate
read taking between two and eight seconds can restart the worker early.

Store a deadline and operation class with each pending request. Distinguish a
responsive worker with expensive read work from an unresponsive native actor,
and keep a hard upper bound for both. Worker dispatch is serial: a probe queued
behind an allowed read must not restart it at the probe's shorter timeout.
Define scheduling/progress semantics for that case. Test a four-to-six-second
read inside its own deadline, then a truly stuck read. This is
a small correction; do not loosen all health deadlines to the slowest case.

### R12 — Separate port discovery, listener publication, and telemetry cadence

**P2 · defect/performance · M.** In
[dynamic_forwarding.go](../internal/codelima/dynamic_forwarding.go):791,924–999,
listener reconciliation waits for a batch whose workers scan ports and then
sample telemetry. Slow telemetry therefore delays new listeners across nodes,
despite comments describing independent routing and telemetry behavior.

Publish successful discovery promptly. Give telemetry its own concurrency
budget/cadence and immutable latest-value cache, reusing the node's peer without
making listener creation await metrics. Test a blocked telemetry sample and
more nodes than the scan concurrency limit; a healthy newly discovered service
must become reachable within the discovery budget. Separate scheduling adds
ownership bookkeeping, so share the peer lifecycle rather than creating a
second connection per metric.

### R13 — Validate and reserve clone targets before stopping the source

**P2 · defect · S–M.** `NodeClone` in
[service.go](../internal/codelima/service.go):1648 stops a running source before
validating the target slug at 1682 or uniqueness at 1707. Invalid requests can
unnecessarily interrupt a working VM and encounter source restart failure.

Perform pure validation and target reservation first, then mutate the source.
Keep cancellation-safe restoration and release reservations on every exit.
Test missing, malformed, duplicate, and unreservable names with zero stop/start/
clone calls. Retain the existing cancellation restart regression. The cost is
a reservation spanning the operation, already a familiar lifecycle pattern.

### R14 — Validate record identity and treat indexes as rebuildable hints

**P2 · defect/risk · M.** `readNodeRecord` in
[store.go](../internal/codelima/store.go):828 checks nonempty ID, but not agreement
with its directory. `NodeByIDOrSlug` at 860 and analogous configuration and
environment lookup paths trust indexed targets without fully checking the
requested identity. A stale or damaged index can resolve a command incorrectly.

Validate safe identifier components, directory/record ID agreement, requested
slug or runtime identity, and deletion status at the store boundary. On stale
derived indexes, read lookups may fall back to a scan without writes; rebuild
only in `doctor --repair` or an authorized mutation under the existing lock.
Reject ambiguity.
Isolate configuration/environment corruption as deliberately as node corruption,
including configuration-label hydration. Test wrong-target references,
mismatched IDs, interrupted rename/index updates, and missing records. Strict
validation may expose damaged existing homes, so provide a precise doctor
report rather than silently choosing a different target.

### R15 — Extract packages along ownership boundaries, not file size

**P2 · improvement · L.** `internal/codelima` contains the application service,
TUI, renderer supervisor, native bridge, runtime adapter, and storage. The
2,473-line `Service` and 2,761-line Ghostty file are symptoms of mixed
responsibility. Both executable entry points import the same package, so a
separate worker executable has not yet enforced a separate Ghostty dependency.

First extract portable terminal/daemon DTOs and consumer-sized interfaces.
Then isolate metadata and node lifecycle, the Lima adapter, forwarding, TUI,
and finally the Ghostty worker adapter. Keep composition at entry points.
Separate PTY/process lifecycle from the cgo terminal model so only the worker
imports Ghostty. Darwin platform capability detection may still require cgo;
the criterion is absence of Ghostty from the application dependency path.
Use package dependency checks and existing behavior tests after each move.
Avoid a generalized runtime plugin system while Lima is the only provider,
and avoid duplicating DTO definitions just to break import cycles.

### R16 — Complete the public lifecycle/view split and index observations once

**P2 · improvement · M.** Durable lifecycle and observations already have
separate storage concepts, but `reconcileNodeWithObservations` still overwrites
the returned `Node.Status` with runtime-derived values. Extend TODO #17 with
explicit `NodeRecord`, `RuntimeObservation` including freshness/error provenance,
and `NodeView`. Keep read surfaces free of durable lifecycle writes.

`reconcileNodes` in [service.go](../internal/codelima/service.go):2402 also calls
linear `findObservation` for each node, making reconciliation O(nodes × runtime
instances), despite PATTERNS claiming an index. Index one observation batch by
runtime identity, define duplicate handling, and reuse the snapshot. Benchmark
10/100/1,000 nodes with allocation and subprocess counts before adding more
caches. Preserve the existing inode parse cache and metadata-only writes that
already bypass runtime validation. The view split involves API plumbing and
may require a versioned public response change; the batch index is localized.

### R17 — Remove implicit terminal-backend divergence and finish process cleanup

**P2 · risk/improvement · M–L.** `newTUITerminal` in
[tui_terminal_vaxis.go](../internal/codelima/tui_terminal_vaxis.go):20 silently
falls back to a different terminal emulator when Ghostty initialization fails.
Its `CapturesMouse` reflects unsynchronized private Vaxis fields at 129 (known
TODO #25). This doubles semantics and failure paths and weakens reproducibility.

Daemon-managed terminals already require the isolated Ghostty worker. Remove
the residual local paths' implicit fallback and give them an actionable
initialization error. Keep a fallback only if it is an explicit,
tested product mode with supported public accessors. Separately finish TODO
#24: shell job-control groups can outlive a kill of the initial process group.
Model platform-specific session cleanup behind one process owner, guard against
PID reuse, and test foreground/background jobs, close, and handoff. Preserve
live shell ownership during worker replacement. Removing fallback narrows
support for developer builds missing Ghostty; document that deliberate tradeoff.

### R18 — Make input bindings declarative and restore guest word navigation

**P1 product priority / P2 extensibility · M.** ROADMAP priority 1 correctly
identifies that Option+Left/Right are intercepted as tab navigation. The central
`tuiKeyBindings` table in [tui_input.go](../internal/codelima/tui_input.go):32 is
already delivered, but uses opaque match functions while footer/help labels
remain separate constants.

Extend it into binding descriptors with action ID, scope, default chord, and
display label. Validate collisions and generate help/footer text from the same
registry; add configuration using the existing settings conventions. Choose
non-conflicting navigation defaults and migrate documentation together. Test
that Option+Left/Right reach the guest, paste never triggers shortcuts, shifted
tab movement stays consistent, and unsupported terminal key encodings have
documented alternatives. Keep this independently shippable; it should not wait
for the Ghostty rebase or package decomposition.

### R19 — Optimize end-to-end screen work after measuring each stage

**P2 · improvement · M–L.** `draw` in
[tui_render.go](../internal/codelima/tui_render.go):557 clears and rebuilds the
entire logical window. Vaxis performs output diffing, so this is not evidence
that the full screen is transmitted each time. Existing redraw coalescing,
lazy read formatting, cheap journal stats, and encoded snapshot caching already
remove major amplification paths; preserve their regression tests.

Fix coherent publication and simultaneous cache misses as described in G02:
one actor capture for cells/text, one expensive computation per immutable
revision/variant, and a revision check before installing its result.

Measure PTY read → worker apply → snapshot publish → client receipt → Vaxis
render, including queue wait, allocations, encoded bytes, dirty rows, and
visible versus hidden tabs. First use upstream dirty rows and bulk C reads
(G02), then retain unchanged UI regions and immutable rows. Introduce row-delta
transport only if full-frame copying/JSON remains material; require a full
snapshot fallback, generation/base sequence checks, and bounded recovery.
Keep on-demand text/ANSI variants. One publication scheduler should own cadence
across adjacent stages where possible; do not stack independent 50 ms delays.
Changing transport without evidence would add more state than it removes.

### R20 — Make native/tool installs atomic, platform-specific, and verifiable

**P1/P2 · risk/improvement · M.** Extend TODO #22. Installer scripts use
remove-and-replace installation and `rm`/`ln` updates without a cross-process
lock. Ghostty also rewrites the shared `.tooling/ghostty-vt/current` include
link while installations are platform-scoped. Concurrent host/guest or separate
make invocations can publish mismatched or missing headers/libraries.

Use an install lock per platform/build identity, private project-rooted staging,
validated downloads, atomic final rename, and platform-specific include/link
paths. Include source revision, patch hash, Zig version, target, optimization,
and feature set in the build identity. Go/Zig archive downloads currently lack
checksum verification; the Ghostty installer replaces a hashed Zig dependency
with a downloaded local `.path`, so verify that dependency explicitly or remove
the workaround. Test concurrent installs, interrupted downloads, corrupt
archives, and clean offline reuse. Route tools through make. Static worker
linking (G01) simplifies runtime packaging but does not fix install races itself.

### R21 — Strengthen qualification and keep architectural contracts current

**P2 · improvement · M.** CI already runs verification and race tests on Linux
and macOS, but the daemon integration job is Linux-only. Descriptor passing,
PTYs, native worker loading, sleep/wake, and process cleanup justify a macOS
integration lane and native release qualification. Add bounded fuzz/property
coverage for frame decoding, replay/checkpoint decoding, dimensions, split VT
input, and metadata identity. Native Zig/C faults require native sanitizers or
upstream safety builds; the Go race detector is not sufficient.

Create make recipes for reproducible performance and visual qualification,
using project-rooted scratch and cleanup traps even on failure. Keep local
daemon diagnostics payload-free by default and collect queue ages/bytes,
publication lag, operation deadlines, and restart reasons; do not resume
per-call success logging. Extend TODO #2's visual harness and TODO #28/#35's
native matrix. Keep signing/notarization (TODO #4) and clean archive execution
in release qualification, with both executables tested outside the checkout.

Reconcile documented contracts in the implementation work: BUILD still calls
the dispatcher a symlink, says `verify` rewrites formatting, and claims all
tests run serially; Makefile uses a script, `fmt-check`, and parallelism four.
PATTERNS still describes one-second node-list polling, pressure-triggered
VirtioFS reclaim, and indexed runtime observations; current behavior differs.
QA Flow 4 retains an SDK heading and Flow 7 mentions the retired runtime.
Maintain one current contract per area, label historical proposals clearly,
and update README/BUILD/PATTERNS/QA with each behavioral change. This review
does not accept new ADRs for changes that have not been implemented.

### R22 — Own request lifetimes through cancellation and daemon shutdown

**P1 · risk/defect · M.** [daemon/dispatch.go](../internal/codelima/daemon/dispatch.go):152
starts handler goroutines outside the server's wait group; input lane workers
also have separate lifetimes. `Server.Run` can reach `Handler.Close` while an
admitted handler is still mutating the host. `daemonHost.update` lacks a context,
`enterTerminalMutation` blocks on a non-cancelable mutex, renderer reads start
from `context.Background`, and
[daemonclient/client.go](../internal/codelima/daemonclient/client.go):153 registers
pending work and waits for the writer mutex before checking cancellation.

Introduce a server-owned operation group and admission gate. Stop new work,
cancel pre-commit operations, unblock transports, await owned work within
declared deadlines, then tear down host state. Define which committed lifecycle
steps must finish under a recovery context. Check cancellation before queue
admission and before writing requests; do not equate a disconnected caller with
permission to replay an uncertain mutation. Test shutdown/disconnect during
open, queued input, read, and update; no rejected command may execute later and
no handler may mutate after teardown. This replaces implicit goroutine lifetime
assumptions with an explicit shutdown contract.

### R23 — Fence renderer state at installation, after a verified handshake

**P1 · defect/risk · M.** `rendererSupervisor.handleFrame` in
[renderer_supervisor.go](../internal/codelima/renderer_supervisor.go):530 checks
generation, unlocks, then decodes and calls outward. A replacement can occur
between those steps. `installRendererSnapshot` in
[daemon_terminal_isolated.go](../internal/codelima/daemon_terminal_isolated.go):588
ignores the supplied generation. `startRenderer` also accepts state frames
before verifying initialization, while the worker emits its first snapshot
before its init reply.

Model starting/verified/ready/replacing states explicitly. Buffer or reject
state until the protocol/build handshake succeeds and check renderer identity
plus generation atomically with state installation. Use the same owner for
stale marking and replacement. If early frames are rejected, explicitly request
a fresh snapshot after readiness so a cold terminal receives initial state.
Test delayed old-generation decoding, replacement
between callback admission and installation, and a mismatched worker sending
state before its reply. More precise fencing removes the need to trust timing
between supervisor and snapshot cache.

### R24 — Centralize typed RPC validation and delivery semantics

**P2 · risk/improvement · M.** `daemonHost.Handle` in
[daemon_host.go](../internal/codelima/daemon_host.go):226 combines decoding,
dispatch, Vaxis conversion, and lifecycle work. Several branches ignore
`json.Unmarshal` errors or default invalid read enums. Resize accepts positive
Go integers that later narrow to `uint16`. The useful `ClassifyMethod` registry
does not fully express semantics: opening a fresh terminal or moving a tab by
a delta is not idempotent merely because it is classed as control traffic.

Extend the method registry with typed parameters/results, validation, limits,
execution class, and explicit retry/idempotency rules. Keep codecs at transport
boundaries and UI events inside the frontend. Validate malformed JSON, unknown
enums, dimension overflow, invalid IDs, and unknown-delivery retries. Add
idempotency tokens only where a product workflow needs them. This can simplify
both daemon and CLI call sites, but a generated framework is unnecessary for
the present method count.

### R25 — Make rollback failure observable and recoverable

**P2 · defect/risk · M.** The update rollback closure in
[daemon_host.go](../internal/codelima/daemon_host.go):1526 ignores failures from
`resumeReplacement` and individual `RollbackHandoff` calls. In
[daemon_terminal_isolated.go](../internal/codelima/daemon_terminal_isolated.go):785,
a failed renderer restart can return before restarting the PTY read pump. The
reported update error then conceals that an original terminal stopped advancing.

Represent handoff phases and owned descriptors explicitly; aggregate rollback
errors and expose recovery status per terminal. Preserve a managed retry/recovery
path even when recreating the renderer fails, with PTY ownership unambiguous.
Inject failure before/after transfer, import, commit, and during rollback itself.
Assert original terminal continuity or an explicit actionable degraded state.
Keep the existing tab-set barrier and commit-time persistence. Recovery logic
is necessarily more explicit, but it should be one state machine rather than
several best-effort closures.

### R26 — Validate resolved filesystem containment at the workspace boundary

**P1 · defect/risk · S–M.** `canonicalPath` in
[fsutil.go](../internal/codelima/fsutil.go):38 performs lexical absolute/clean
normalization. `resolveNodeDirectoryPath` in
[service.go](../internal/codelima/service.go):2097 checks containment in
`CODELIMA_HOME` using that path. Later,
[lima.go](../internal/codelima/lima.go):624 resolves symlinks and mounts the result
writable. An outside symlink into the metadata home can therefore bypass the
service's explicit exclusion and expose the metadata tree to the guest.

Validate both resolved metadata-root and resolved workspace containment before
accepting the directory and again at the mount/seed boundary. Keep logical paths
for display if desired. Define behavior for symlink changes between validation
and use; a single early `EvalSymlinks` is not a race-proof filesystem capability.
Test symlinked metadata roots, outside aliases into them, harmless workspace
aliases, and retargeting before start. Reuse one containment helper for mounted
and copy modes. The tradeoff is additional filesystem resolution and explicit
handling of broken/inaccessible aliases, which should fail without mutation.

## libghostty-vt: current capabilities and responsibility transfer

The following recommendations are part of this plan, with stable IDs G01–G14.
Detailed upstream evidence and upgrade constraints follow.

### Availability and adoption matrix

“At pin” means the May API already provides the capability even if CodeLima
does not use it. “New” means added or materially extended by the audited
September revision. These are public C APIs unless a limitation says otherwise.

| ID / priority | Upstream capability and availability | Recommendation and retained CodeLima responsibility |
|---|---|---|
| G01 · P2 | Exact native build; public C API remains unstable | Link the exact static archive only into the renderer's Ghostty package. Remove dynamic symbol loading and arbitrary production library candidates. Keep process supervision and a versioned worker handshake. |
| G02 · P2 | Render state and multi-get at pin; two-phase updates, dirty iteration, UTF-8 reads, structured cursor, bulk raw rows new | Use one C batching call per frame/changed rows, then owned Go values. Ghostty owns terminal damage and cell semantics; CodeLima still resolves its Vaxis representation, publishes frames, and draws. |
| G03 · P2 | Plain/VT/HTML formatter at pin; streaming writer new August 17 | Replace local terminal text/ANSI formatting. Preserve the public read contract, output limits, on-demand caching, and transport backpressure. HTML is optional, not a new required feature. |
| G04 · P2 | Grid-reference hyperlinks at pin | Delete the custom hyperlink exports and use supported grid references. Keep viewport coordinate translation, click policy, and host link opening. |
| G05 · P2 | Key/mouse/focus and low-level paste encoders at pin; terminal-level semantic paste new August 22–23 | Require the encoders and remove handwritten fallbacks. Use one semantic paste operation. Keep Vaxis translation, shortcuts, input ordering/admission, and paste policy. |
| G06 · P2 | Clipboard write since pin; clipboard reads, OSC 5522 MIME/permissions/grants new August 21–24 | Remove the OSC 52 scanner and receive decoded requests. Keep host permission, size limits, destination ownership, and callback-lifetime mediation. Read support is a separate opt-in design. |
| G07 · P2 | System log hook, title/bell/PWD state at pin; richer PWD/notification/progress effects new | Replace process-wide stderr capture and extra escape scanners with native hooks. Keep bounded logs, badges, host notification policy, and replay-effect suppression. |
| G08 · P2 | Terminal default colors and basic size reports at pin; color-scheme encoder, prompt resize fixes, automatic mode-2048 reports, prompt query new | Supply host colors, delegate report encoding and terminal reflow, and use positive prompt evidence. Keep host discovery/theme propagation and shell redraw qualification. |
| G09 · P3 | Selection/formatting at pin; gesture machine new | Use native word/line/output selection and gesture calculations. CodeLima retains gesture routing, host-selection bypass, highlighting, copy UX, and stale-reference handling. |
| G10 · P3 | Whole-terminal incremental literal search new August 31 | Use `ghostty_search_feed/tick/get` for terminal search and suitable future literal waits. Avoid another scrollback index. Scheduling, cancellation, UI, and application completion semantics stay local. |
| G11 · P2 | Core terminal snapshots and continuation since pin; decoder retain-continuation option new August 17 | Add bounded same-build core checkpoints plus ordered journal tails. Restore presentation/policy separately and report unsupported state honestly. |
| G12 · P2 | Caller-driven scrollback compression and resource limits new | Use bounded idle compression and native history limits. CodeLima owns idle scheduling and total worker/daemon budgets. |
| G13 · P3 | Basic Kitty images/placements/resource controls at pin; newer geometry, generations, relative placement, current-frame data | Let Ghostty parse/store protocol state; build a complete bounded asset/placement transport and Vaxis drawing path. Start with static in-band images. Animation remains a public-C API gap to resolve. |
| G14 · P2 | Sized structs, typed errors, ABI/type manifest, selected-feature builds | Keep a small lifetime-safe, error-reporting adapter. Use manifest validation for ABI checks, not handwritten Go decoding of unstable native bitfields. |

### G01/G14 — Rebase as a compile-time dependency change

The audited [public header](https://github.com/ghostty-org/ghostty/blob/82232ecde55405559dec29c5466cb9e39938cb41/include/ghostty/vt.h)
warns that API signatures are unstable, and the
[README](https://github.com/ghostty-org/ghostty/blob/82232ecde55405559dec29c5466cb9e39938cb41/README.md#cross-platform-libghostty-for-embeddable-terminals)
still describes libghostty as untagged. Established terminal behavior does not
imply ABI compatibility. Updating the revision while retaining the existing
`dlsym` signatures is unsafe.

Verified breaking changes include removal of `GhosttyTerminalOptions` in favor
of the allocator/out/columns/rows constructor; removal of
`ghostty_terminal_mode_get/set` in favor of typed terminal data/options; and
replacement of `ghostty_render_state_colors_get` with the render-state color
getter. Clipboard request structures and lifetimes have also evolved. The
[dependency manifest](https://github.com/ghostty-org/ghostty/blob/82232ecde55405559dec29c5466cb9e39938cb41/build.zig.zon)
requires **Zig 0.16.0**, while CodeLima pins 0.15.2.

The standalone installed archive on Linux/macOS is **`libghostty-vt.a`**.
`ghostty-vt-static` is the Zig dependency artifact name; the dedicated
`libghostty-vt-static.pc` selects the installed static archive. Merely adding
`--static` to pkg-config for the shared package does not choose it. See
[build.zig](https://github.com/ghostty-org/ghostty/blob/82232ecde55405559dec29c5466cb9e39938cb41/build.zig#L161)
and [GhosttyLibVt.zig](https://github.com/ghostty-org/ghostty/blob/82232ecde55405559dec29c5466cb9e39938cb41/src/build/GhosttyLibVt.zig#L507).

Use an explicit feature set. A starting candidate is
`-Dvt-features=-all,+formatter,+selection,+render-state,+input-encode,+color,+grid-introspection`;
enable snapshot/search when adopted and graphics only with its transport.
Validate the actual selected build rather than assuming disabled features keep
their symbols. Compare ReleaseSmall with ReleaseFast and a safety build using
end-to-end measurements. The full source revision, compiler, features, target,
and patch hash belong in the worker handshake; `ghostty_build_info` alone is
not a guaranteed full revision/feature attestation.

Follow upstream [packaging guidance](https://github.com/ghostty-org/ghostty/blob/82232ecde55405559dec29c5466cb9e39938cb41/PACKAGING.md):
official source assets include preparation missing from automatic GitHub source
archives. A mutable tip URL is not a reproducible pin. Archive verified inputs
or build a reproducible exact-checkout/dependency-cache flow. Expose upstream
`test-lib-vt`, `test-lib-vt-build`, and `test-lib-vt-schema` plus CodeLima adapter
conformance through make. Native builds and static packaging on all release
platforms are required before this migration can ship.

Make native failures visible. Current bridge resize ignores the upstream result,
render-update failures can look like a clean frame, and callback allocation
failure can silently drop a response. Return typed errors, validate sized
structs, cap callback buffers, and distinguish unsupported capability, allocation
failure, clean state, and stale state. Do not preserve these silent fallbacks
when deleting the dynamic loader.

### G02/G03 — Transfer rendering semantics while reducing copies

The [render API](https://github.com/ghostty-org/ghostty/blob/82232ecde55405559dec29c5466cb9e39938cb41/include/ghostty/vt/render.h)
supports global and row-level dirty state, `next_dirty`, `clean`, a structured
cursor, UTF-8 grapheme access, and batched retrieval. Consume all required frame
state before cleaning both dirty layers. `begin_update` needs exclusive access
to the terminal; `end_update` finishes on render-state-owned data. A serial
worker gains no concurrency simply by calling the two functions consecutively.
The raw row view contains packed cells, **not** fully resolved styled viewport
cells; their layout is not a stable Go ABI. Prefer C getters within the bulk
adapter unless measurements justify generated layout decoding.

Capture cells, cursor, metadata, and visible text as one immutable publication
with one generation and revision. Today worker publication takes separate actor
reads for snapshot and text; daemon/host paths also clone whole grids, while
simultaneous first cache misses can duplicate JSON encoding or variant reads.
Use per-revision single-flight work and verify the returned revision before
memoizing. Share owned immutable rows internally; copy at true ownership or
mutation boundaries. This strengthens consistency as well as reducing cost.

The [formatter](https://github.com/ghostty-org/ghostty/blob/82232ecde55405559dec29c5466cb9e39938cb41/include/ghostty/vt/formatter.h)
can replace `visibleTextLockedRaw`, `recentTextLockedRaw`, local row formatting,
and `ghosttyANSISGRForCell`. Its trim, soft-wrap, selection, and output-format
options need characterization. The current `ReadRecent(ReadANSI)` includes
plain-text history followed by ANSI-visible content; its limit is **2,000
history rows plus the visible rows**, not 2,000 total. Decide and document
whether to preserve or correct this inconsistent ANSI contract. Never stream
the synchronous formatter callback directly into a slow client while holding
terminal access; use bounded owned output/chunks and the existing lazy reads.

Acceptance includes wide/combining graphemes, blank and soft-wrapped rows,
explicit versus default colors, cursor states, hyperlinks while scrolled,
partial/full dirty updates, dropped publications, and identical cells/text
revision. Track cgo crossings, allocations, CPU, and wire bytes per frame.

### G04 — Retire the patch by concern, with honest compatibility decisions

The [grid-reference API](https://github.com/ghostty-org/ghostty/blob/82232ecde55405559dec29c5466cb9e39938cb41/include/ghostty/vt/grid_ref.h)
already exports hyperlink URI access at CodeLima's current pin. Delete the
custom active-screen/scrollback hyperlink exports and their loader checks;
choose viewport/history coordinates deliberately.

The other patch concerns are not upstream equivalents:

1. `CSI ?4m` / XTQMODKEYS is still unsupported in the audited
   [stream parser](https://github.com/ghostty-org/ghostty/blob/82232ecde55405559dec29c5466cb9e39938cb41/src/terminal/stream.zig).
   Characterize the required behavior. If a real dependency requires it, carry
   only that small temporary patch with an owner, upstream issue/PR, and removal
   condition. The unknown-sequence callback cannot implement this CSI query.
2. Explicit icon-title push/pop is intentionally distinct from window-title
   behavior upstream; the local patch aliases them. Remove that alias after a
   compatibility fixture and explicit decision, or seek an upstream option.

The complete existing patch fails applicability checks against the audited
revision. Do not rebase it blindly or claim zero patches while silently losing
required behavior. Zero unnecessary downstream patches is the target; remaining
compatibility needs must be visible and separately tracked.

### G05/G06 — Delegate input and clipboard protocol, retain host policy

Use upstream [key](https://github.com/ghostty-org/ghostty/blob/82232ecde55405559dec29c5466cb9e39938cb41/include/ghostty/vt/key/encoder.h),
[mouse](https://github.com/ghostty-org/ghostty/blob/82232ecde55405559dec29c5466cb9e39938cb41/include/ghostty/vt/mouse/encoder.h),
and [focus](https://github.com/ghostty-org/ghostty/blob/82232ecde55405559dec29c5466cb9e39938cb41/include/ghostty/vt/focus.h)
encoding as required capabilities. The preferred latest paste path is
[`ghostty_terminal_paste`](https://github.com/ghostty-org/ghostty/blob/82232ecde55405559dec29c5466cb9e39938cb41/include/ghostty/vt/paste.h),
which handles terminal modes, sanitization, newline/bracket encoding, and Kitty
paste-event behavior. `GHOSTTY_REJECTED` requires a deliberate paste decision;
`allow_unsafe` must not become a silent compatibility default. The native reader
may buffer text for safety, so preserve input byte limits even with streaming
interfaces. Shared-client ordering and exactly one semantic paste remain R04.

The [terminal clipboard hooks](https://github.com/ghostty-org/ghostty/blob/82232ecde55405559dec29c5466cb9e39938cb41/include/ghostty/vt/terminal.h#L425)
replace CodeLima's OSC 52 byte scanner and now support MIME representations,
destinations, and grant/permission responses. Requests, borrowed data, and
reply functions are valid only during the synchronous callback. Copying the
data does not extend the reply handle's lifetime. Begin with a bounded write
policy and keep reads disabled until a worker/daemon/frontend consent path is
designed. Interactive permission requires a bounded rendezvous or an upstream
asynchronous facility; it cannot queue the borrowed callback for later use.
Define no-client, no-seat, timeout, denied, unsupported, and replay behavior.

`GHOSTTY_TERMINAL_OPT_CLIPBOARD_WRITE_MAX_BYTES` defaults to 64 MiB and applies
to **OSC 5522 only**, not OSC 52. It does not replace host/IPC budgets. Test
fragmented/malformed/oversized payloads, disconnect during a request, replay,
atomic MIME writes, unsafe paste rejection, bracketed multiline paste, and
concurrent senders. Never let a native callback block indefinitely on host UI.

### G07/G08 — Use effects, logging, colors, and semantic evidence directly

The existing [system log hook](https://github.com/ghostty-org/ghostty/blob/82232ecde55405559dec29c5466cb9e39938cb41/include/ghostty/vt/sys.h)
can remove `acquireGhosttyStderrCapture`, `releaseGhosttyStderrCapture`, their
pipe-draining goroutine, and process-wide FD mutation. Install it once at worker
startup, copy borrowed bytes before queuing, and keep a bounded sink. Native
messages compiled out of a release build cannot be recovered by this callback.

Use title/PWD/bell/notification/progress effects for metadata and badges, with
CodeLima deciding presentation and host actions. The unknown-sequence hook
currently reports **APC only**. The upstream VT-processing-error datum is a
permanent informational boolean latch, not a universal callback for malformed
or unsupported sequences (nor a read-and-clear error). Neither
feature justifies a second general-purpose VT parser.
If unknown-sequence diagnostics are enabled, set a bounded nonzero
`UNKNOWN_MAX_BYTES`; installing the callback alone does not capture content.

Terminal default foreground/background/cursor/palette options already exist at
the current pin; TODO #1's upstream prerequisite is stale. Feed outer-terminal
colors through the semantic worker protocol, update live terminals on host
theme changes, and use the new
[color-scheme encoder](https://github.com/ghostty-org/ghostty/blob/82232ecde55405559dec29c5466cb9e39938cb41/include/ghostty/vt/color_scheme.h)
instead of local `CSI ?997` formatting. Preserve explicit/default color
distinctions when mapping to Vaxis.

Test newer prompt reflow and mode-2048 size-report behavior against the current
extra `SIGWINCH` width-growth workaround before removing it. Correct internal
reflow does not prove a particular shell will repaint without the signal.
`CURSOR_AT_PROMPT` is positive semantic evidence only: false also covers absent
shell markers and alternate screen. A reliable unknown/idle/running model needs
marker availability and application policy; it is not a general command-complete
signal. Upstream GUI display-link fixes do not fix CodeLima's Vaxis renderer.

### G09/G10 — Build selection and search on the terminal's own model

The [selection API](https://github.com/ghostty-org/ghostty/blob/82232ecde55405559dec29c5466cb9e39938cb41/include/ghostty/vt/selection.h)
supports word/line/output selection, formatting, and gesture calculations.
Use these when adding embedded selection; preserve host bypass and guest mouse
capture. Selection/grid snapshots have mutation-sensitive lifetimes, so copy
rendering values or reacquire them after terminal updates.

The new [search API](https://github.com/ghostty-org/ghostty/blob/82232ecde55405559dec29c5466cb9e39938cb41/include/ghostty/vt/search.h)
searches live primary/alternate screens and scrollback across resize, reflow,
and pruning. `feed` accesses the terminal; `tick` makes bounded progress on
copied data. Serialize a search object's operations and schedule work within
the worker budget. Completion means caught up to the last feed, not permanently
finished. Matching is literal bytes with ASCII case folding; no public regex
or full Unicode case-folding option was found. Match selection references must
be consumed before relevant mutation. Test all these cases and cancellation
during a large history search. Do not build a parallel text index or claim this
alone solves agent-state detection.

### G11/G12 — Checkpoint core state and compress idle history with explicit bounds

The [snapshot API](https://github.com/ghostty-org/ghostty/blob/82232ecde55405559dec29c5466cb9e39938cb41/include/ghostty/vt/snapshot.h)
can capture core terminal/history state and restore unfinished VT continuation.
Format version 1 has no compatibility guarantee. Restrict initial use to the
exact same build, features, and width policy; maintain a fallback for a changed
build during live update.

The codec's [field classification](https://github.com/ghostty-org/ghostty/blob/82232ecde55405559dec29c5466cb9e39938cb41/src/terminal/snapshot/terminal.zig#L216)
explicitly excludes selection, scrolled viewport position, hover/search/dirty
state, compression scheduling, Kitty images/placements, and glyph registrations.
Focus comes from the destination. Callbacks, search objects, clipboard request
lifetimes, queued input, and CodeLima policy are outside the snapshot.

Use a CodeLima checkpoint envelope carrying build identity, terminal identity,
generation, applied event watermark, integrity data, size limits, and separately
restorable viewport/focus/policy. Capture the checkpoint and watermark at one
terminal-owner boundary; never truncate the tail ahead of that point. Reinstall
callbacks and replay only the ordered tail with external effects suppressed.
Enable bounded continuation tracking before the first write and set decoder
`RETAIN_CONTINUATION` when future checkpoints are required. Tracking overflow
invalidates checkpoint eligibility until safe recovery/ground; ground does not
mean that the shell is idle.

The decoder can restore a renderable prefix through `READY` and then prepend
history incrementally. This could reduce recovery time to first usable frame,
but intervening live changes can make pending history unsafe to apply and cause
it to be skipped. Track remaining/skipped history and expose completeness;
never equate READY with a fully validated/retained checkpoint through `FINISH`.
Qualify this mode separately from a complete restore before using it by default.

Test split UTF-8/CSI/OSC/DCS/APC input, immediate second checkpoints after
restore, corruption/truncation, revision mismatch, size exhaustion, repeated
geometry changes, and non-bottom viewport restoration. Treat missing images
and other excluded state as explicit recovery limitations until separately
handled. Same-build core checkpointing is achievable; exact whole-worker
recovery is not established by this API.

Drive [scrollback compression](https://github.com/ghostty-org/ghostty/blob/82232ecde55405559dec29c5466cb9e39938cb41/include/ghostty/vt/terminal.h#L2217)
in small idle steps within each worker, not in the PTY read path. Preserve the
existing 10,000-line policy with an explicit byte limit and account for native
page granularity. The library starts no scheduler for this. Compare RSS and
first-input latency before enabling it by default; compression can trade CPU
and decompression latency for memory, and should be skipped when unsupported
or unprofitable.

### G13 — Treat Kitty graphics as an end-to-end capability

The [public graphics API](https://github.com/ghostty-org/ghostty/blob/82232ecde55405559dec29c5466cb9e39938cb41/include/ghostty/vt/kitty_graphics.h)
provides image and placement state, clipping/geometry, IDs/generations, and
pixel access. Delegate parsing, transmission assembly, storage, and placement
calculations to Ghostty. CodeLima still needs a PNG decode callback, owned asset
copies, bounded caches, a generation-aware asset/placement protocol, client
capability negotiation, Vaxis drawing, deletion and z-order handling, and
fallback behavior when the outer terminal lacks support. Initial support should
allow in-band static images and disable guest-specified host files, temporary
files, and shared memory.
Set native `KITTY_IMAGE_STORAGE_LIMIT`, `APC_MAX_BYTES`, and
`APC_MAX_BYTES_KITTY` alongside IPC/decoded-pixel budgets; include malformed
images and decompression bombs in the bounded-resource fixtures.

Current-frame pixel access does not prove animation scheduling support:
`ImageStorage.animationTick` exists in the Zig implementation, but no equivalent
public C tick/deadline API was found. Resolve that upstream before promising
animated pets. Glyph handling can be enabled, but
[its build feature](https://github.com/ghostty-org/ghostty/blob/82232ecde55405559dec29c5466cb9e39938cb41/src/terminal/build_options.zig#L150)
has no C surface for rendering registered glyphs; leave it disabled. Graphics
are also absent from core snapshots. Test transfer limits, clipping, resize,
scrolling, deletion, multiple clients, restarts, and unsupported clients before
marking ROADMAP priority 4 delivered. libghostty-vt does not itself draw images
into the outer terminal.

### Expected deletions and retained boundaries

The six current files for the Ghostty Go wrapper, C bridge/header, local input
encoding, clipboard adapter, and downstream patch total 4,771 physical lines.
This is an ownership baseline, not removable-code arithmetic. Target deletion
of the runtime loader, unnecessary patch exports, local VT read formatter,
OSC scanner, optional input encoders, and process-global stderr capture. Keep
the small event conversion, callback/lifetime adaptation, bounded bulk reads,
Vaxis styling, and host policy needed at the actual boundary.

Do not adopt the August draft's 1,500/2,500-line savings or a fixed 450-line bridge
limit as acceptance gates. Measure deleted responsibilities, remaining failure
paths, API surface, latency, and memory; count source lines afterward. Avoid
replacing deleted local code with an equally complex generic binding framework.

## Delivery order, measurement, and acceptance

### Proposed delivery slices

| Slice | Work | Dependency / completion gate |
|---|---|---|
| A — Correctness first | R01–R09, R22–R23/R26; R18 in parallel as existing product priority | Add deterministic regression first, then localized fix. Preserve current protocol where possible. Native boundary changes require their matching QA flows. |
| B — Tighten contracts | R10–R14, R24–R25; characterize G02–G08 behavior | Explicit admission/deadlines/recovery results and bounded resource tests. No implicit retries of non-idempotent operations. |
| C — Thin native worker | R15/R17/R20 plus G01–G08/G14 | Extract portable types and PTY owner; upgrade compiler/library together; remove redundant semantics and qualify static packaged worker. Preserve hang containment and shell continuity. |
| D — Measure and reduce remaining cost | R16/R19 plus G02/G11/G12; tune R12 after its correctness fix in B | Compare stage timings and allocations, add coherent publications and same-build checkpoints, then consider deltas/compression. Each optimization must retain correctness and show benefit. |
| E — Extend on upstream state | G09/G10/G13 | Selection/search can be separate increments. Graphics needs a complete client path and an explicit animation/checkpoint decision. |
| Throughout | R21, documentation, TODO/ROADMAP, numbered ADRs | Each internal behavioral change gets an ADR using ADR_TEMPLATE; patterns are documented once and reused. No recommendation is marked implemented based on this review. |

A and B should remain small enough to review independently. R03 cleanup
revalidation and R13 clone preflight should not wait for an abstraction refactor.
G11 depends on reliable generation/event ordering and G13 has state that G11
does not preserve. The existing runtime model and schemas remain the baseline;
remote providers, a new database, and a new frontend are not prerequisites.

### Measurement plan and proposed gates

Record a baseline before choosing an optimization. Use 80×24 and 160×50 screens,
1/10/configured-maximum terminals, 1/multiple attached clients, idle/hidden tabs,
ordinary echo, Unicode/wrapped output, a sustained producer, and deep history.
Include 10/100/1,000-node metadata/inventory workloads. Report hardware, build
features, optimization mode, and warm versus cold cache with every result.

| Goal | Evidence required |
|---|---|
| Stability | Deterministic regression for every defect; bounded blocked-peer/PTY/native-worker behavior; no stale-generation publication; recovery effects never replay to host. |
| Responsiveness | Stage-level input-to-echo and UI event latency p50/p95/p99; proposed local p95 ≤100 ms and p99 ≤250 ms for ordinary typing on the qualified host, without weakening ordering. Treat these as targets to qualify, not measured current results. |
| Containment | Status and unrelated terminals remain responsive while one worker or peer is stuck; memory plateaus at declared limits; all shutdown paths finish or expose bounded degraded recovery. |
| Efficiency | CPU, RSS, allocations, cgo crossings, JSON/IPC bytes, full/dirty rows, subprocess counts, restart time; idle client cost should not scale with hidden-tab grid size. |
| Simplicity | Removed duplicate parser/formatter/encoder/loader responsibilities; independent package contracts; no new general framework without a demonstrated second use. |
| Extensibility | Portable terminal commands/views consumed by CLI, daemon, and TUI; selected upstream capabilities added without introducing a second terminal model. |

Compare old/new Ghostty with recorded byte streams and randomized meaningful
chunk boundaries in isolated instances. Compare owned screen/read/effect values,
not private native memory. Never shadow-feed two effect-emitting emulators into
one live PTY. Keep prior tests for no-idle polling, snapshot costs, handoff,
renderer hangs, reconnect, input, lazy variants, and tab order. Add targeted
fuzzing and upstream conformance/safety builds rather than implementation-mirror
tests. New tooling belongs in make, and verification-only scratch, processes,
VMs, archives, and data homes must be cleaned after every run.

### Validation performed for this review

On the current Linux/aarch64 workspace:

- `make verify` passed: formatting check, golangci-lint, all default Go package
  tests, CLI build, and renderer-worker build. The main package tests took
  19.037 seconds; this is a suite duration, not a performance benchmark.
- `make test-race` passed across all packages; main package 31.385 seconds.
- `make test-integration` passed using the built CLI and real isolated renderer
  workers; integration package 16.186 seconds. Its disposable home was removed
  by the recipe. This suite uses host shells, not Lima guests.
- The built CLI's `--help` ran successfully. `go doc` inspected the terminal
  registry, Vaxis completion delivery, and pinned SSH APIs; `gopls` inspected
  relevant references and reported no diagnostics for the reviewed TUI,
  daemon/server/dispatch, or client files.
- Upstream source/header/build/API comparison was performed at the exact
  revision above. The new upstream library was **not** compiled or integrated;
  no speedup, ABI compatibility, or native release readiness is claimed.
- Automated document checks passed for 45 local links, 24 revision-pinned
  upstream source links, all 40 recommendation IDs, Markdown structure,
  placeholders, and whitespace. Review-generated upstream scratch was removed
  after source checking; the integration scratch directory is also absent.

This change produces recommendations and tracking documentation, not runtime
changes. The source findings' new regression tests are acceptance work for the
future fixes; this review does not claim they already exist or pass.

### QA qualification coverage and limitations

All flows in [QA.md](../QA.md) were reviewed. Full manual execution was not
performed in this review: the session is a Linux guest, `limactl` is not on its
PATH, and it cannot observe native macOS windows, sleep/wake, or VZ behavior.
Passing host-shell integration tests does not fulfill those manual gates.
Outstanding qualification is tracked in TODO #40 and the existing native QA
items, and must be completed for the corresponding implementation slices.

| QA flow | Recommendations it qualifies | Review-time status |
|---|---|---|
| 1 — Schema and seed | R14–R16/R21 | Help ran; full clean-home, doctor, seed, rejection flow not manually run. |
| 2 — Configuration and frozen nodes | R02/R14–R16 | Automated baseline only; manual flow pending. |
| 3 — Directory nodes and clone | R02/R03/R13/R14/R26 | Automated baseline only; real runtime clone pending. |
| 4 — Lifecycle, bootstrap, resources | R02/R03/R08/R13/R16/R26 | Native Lima, credential/bootstrap, workspace and virtualization checks pending. |
| 5 — Daemon/guest/host terminals and handoff | R01/R04/R06/R10/R11/R17/R22–R25, G01–G08/G11 | Built-binary host integration passed; complete native guest/host manual flow pending. |
| 5b — Seat and shared input | R04/R07/R18/R24, G05 | Two real-window manual arbitration and paste checks pending. |
| 6 — Forwarding | R05/R09/R12 | Automated forwarding tests passed; real multi-VM/IPv6/browser flow pending. |
| 7 — TUI | R01/R04/R07/R17–R19/R23, G02–G10/G13 | Automated TUI tests passed; visual, typing, saturation, sleep/update and Activity Monitor checks pending. |
| 8 — VirtioFS | R16/R21 | Documentation/code reviewed; full native macOS cadence/visibility flow pending. |
| 9 — Read-only diagnostics | R10/R21–R23 | Diagnostic script regression passed in default suite; full live native capture flow pending. |
| Cleanup | All manual slices | No manual QA VMs or user data homes created by this review; integration scratch cleaned, upstream review scratch removed before completion. |

### Relationship to existing work

This plan refines the existing improvement/stability/native-adoption work; it
does not reset delivered tracks. ROADMAP priorities 1, 4, 5, 8, and 9 map to
R18, G13, G01–G14, existing tab persistence/TODO #34, and declarative bindings
respectively. Priorities already marked locally complete remain so, with their
native qualification caveats. TODO #0/#28/#35/#39 retain their manual ownership;
#1 maps to G08, #2 to R21, #4 to release qualification, #17 to R16, #22 to R20,
#24/#25 to R17, and #30 to R05/R09/R12. TODO #36 is resolved, not reopened.

Review completion means the codebase and current upstream capabilities have
been examined and the recommendations documented with evidence and limits.
Implementation completion requires the tests, local execution, applicable full
QA flows, cleanup, documentation, and ADRs described above. Keep the proposed
work in TODO and update each roadmap item only as its actual behavior ships.
