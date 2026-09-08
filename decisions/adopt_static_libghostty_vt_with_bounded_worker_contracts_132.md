# Adopt static libghostty-vt through bounded worker contracts

## Context and Problem Statement

The September 7 review identified duplicated terminal semantics, dynamic native
library selection, inconsistent publication, and recovery that could only
replay a short raw byte tail. The supported September libghostty-vt API provides
bulk rendering, formatting, selection/search, effects, snapshots, compression
and graphics, but does not own CodeLima's shell, frontend or recovery policy.

## Decision Drivers

- Preserve daemon-owned shells and isolate a non-returning native call to one
  replaceable renderer worker.
- Make native behavior reproducible and bounded at every process boundary.
- Adopt supported native semantics without forwarding untrusted guest control
  strings directly to the host terminal.
- Distinguish automated implementation evidence from native release qualification.

## Considered Options

- Keep the May shared-library bridge and duplicate missing semantics in Go.
- Adopt the exact September API in the existing fault-isolated worker.
- Move native rendering into the daemon or replace the entire frontend.

## Decision Outcome

Chosen option: adopt the exact September API in the existing worker. Ghostty
`82232ecde55405559dec29c5466cb9e39938cb41` and Zig `0.16.0` are pinned. The
source, compiler, feature policy, owned patch digests, target, CPU, optimization
and width policy form a native build identity. Header, static archive, Go
binary and worker handshake must agree. Installers serialize cache publication
with kernel locks and publish only validated builds. Release archives contain
`bin/codelima` and `bin/codelima-renderer-worker`, not a shared Ghostty library.

Only `internal/ghostty` and the worker command import the native adapter.
`internal/terminalstate`, `internal/terminalgraphics`, `internal/terminalio` and
`internal/rendererbuild` hold shared values, portable PTY ownership and build
identity. The main CLI builds with `CGO_ENABLED=0`; there is no implicit renderer
fallback or arbitrary shared-library override. Vaxis remains the frontend,
upgraded to v0.17.1 with an attributed local additive bounded Kitty-image API.

### Terminal semantics and effects

The actor owns all C handles. It copies native bulk frames, owned hyperlink
strings and native formatter output before freeing native allocations. Cells,
cursor, metadata, graphics scene and visible text are published as one immutable
cut. Native errors are explicit and latched; unavailable encoders do not trigger
hand-written escape-sequence fallbacks. Recent reads deliberately mean the
latest 2,000 history rows plus the visible screen, bounded to 4 MiB output.

Paste is one semantic operation of at most 64 KiB valid UTF-8. Native safe-paste
encoding runs once and its complete bytes enter the PTY queue atomically.
Oversize/invalid/full-queue requests fail without a prefix or split bracket.
The client input FIFO is bounded by both bytes and item count. Ordinary shared
typing remains available from attached clients; shared inspection/selection,
theme policy, focus and geometry use the seat owner.

Native callbacks supply clipboard writes, title, working directory, bell,
notifications and progress. Clipboard reads are disabled. Clipboard writes are
bounded text delivered only to the current subscribed physical seat connection,
without consuming a shared state revision or replaying on reconnect. Kitty
acknowledged writes are explicitly unsupported before queue admission: CodeLima
does not claim the host clipboard completed a write it cannot acknowledge.
Titles and notices become bounded, sanitized local UI labels/messages, never
automatic host commands, path changes or desktop notifications. Native logs
drain through bounded owned records; no process-wide stderr redirection remains.

Host foreground/background/palette discovery runs outside the UI with one
cancellable 500 ms query. Unknown defaults stay unspecified, including unknown
versus actual RGB black. Theme changes reach all managed terminals, including
hidden ones, and survive raw recovery as application policy. Resize includes
actual cell-pixel geometry when available. Existing PTY SIGWINCH behavior stays
until native shell reflow qualification justifies removing it.

Selection gestures and F7 search use native handles on the actor. The frontend
has one bounded inspection worker, query versions, explicit cancellation and
incremental search ticks; it never builds a second scrollback/search index.

### Recovery and memory

Worker protocol v3 verifies version and exact native identity before installing
a link. Installation, publication, response and graphics caches are generation
fenced. A read timeout detaches its waiter without declaring a healthy native
operation dead; a separate actor-reaching hard deadline still replaces hangs.
Hard budgets follow actual worker dispatch order: queued short calls cannot
expire an executing long read, and later long admissions cannot extend an
already executing short command. Caller deadlines only detach their waiters.
All effects, not just PTY responses, are suppressed during reconstruction.

Checkpoints are native FINISH streams in a versioned SHA-256-protected envelope
containing terminal/build/policy identity, contiguous applied event watermark,
dimensions, viewport, focus, metadata, pixels, host color policy and recovery
provenance. Decoding happens into a temporary terminal and commits only after
strict completion and validation. Payloads are capped at 16 MiB, retained
checkpoint data at 64 MiB across the daemon, and native history at 64 MiB/10,000
rows. A native allocator-backed decode policy also bounds decoded allocation;
the encoded size is not treated as a bound on decompressed pages.

Periodic single-flight capture runs only for dirty eligible terminals. An
independent bounded raw journal remains available for incompatible builds,
quota exhaustion, corrupt checkpoints and native state that snapshots exclude.
Recovery uses a compatible checkpoint plus a gap-free ordered tail, never a
checkpoint with a missing tail. Handoff v5 streams bounded recovery bundles
under an aggregate 64 MiB cap and keeps v4/v3/v2 import compatibility. Graphics
in any screen, unplaced assets or in-flight image loading disable checkpoints;
there is no promise of whole-worker image or selection/search restoration.

Idle compression begins after 500 ms and performs one incremental step per
actor turn, yielding between steps and stopping when complete. It is enabled
by default after paired Linux measurements and can be disabled with
`CODELIMA_GHOSTTY_IDLE_COMPRESSION=0`; platform-wide latency claims still require
native qualification.

### Static graphics

The native graphics store accepts bounded in-band static RGB/RGBA/PNG payloads.
Filesystem, temporary-file and shared-memory transport are disabled, animation
is explicitly rejected, and virtual Unicode-placeholder placements are outside
this first path. Immutable scene metadata uses renderer and asset revisions;
RGBA transfers use validated chunks of at most 64 KiB, with a 16 MiB asset budget.
One frontend worker loads and rasterizes only visible, clipped source regions,
retaining bounded assets and an aggregate 16 MiB prepared scene. Actual cell
pixels preserve placement geometry. A narrow Vaxis extension retains native
z-order, uploads at most 64 KiB per draw, and deletes only owned host images.
Unsupported outer terminals retain text rather than receiving raw guest Kitty
commands. Tab changes, overlays, deletion and worker replacement invalidate
placements and stale results before installation.

### Positive Consequences

- Native terminal semantics replace parallel parsers/encoders and per-cell cgo.
- A packaged worker can start without developer paths or shared libraries.
- Recovery preserves substantially more compatible state without sacrificing
  shell identity or cross-build raw fallback.
- Explicit bounds, effects policy and generations make failure behavior testable.

### Negative Consequences

- Static native builds and small attributed patches require upstream rebase work.
- Larger snapshots, image staging and checkpoints add bounded memory/CPU costs.
- Strict paste/image limits and intentionally unsupported clipboard/animation
  modes are user-visible compatibility policy.
- The full native macOS/Lima, graphics and two-window QA matrix cannot be
  substituted by headless tests; qualification remains tracked in TODO #41.

## Pros and Cons of the Options

Keeping the old bridge minimizes initial changes but retains unsupported native
selection, duplicated parsing and runtime ABI ambiguity. Moving native work into
the daemon simplifies IPC but reintroduces daemon-wide native failure. The
chosen bounded worker contract preserves isolation while making semantic
adoption incremental and independently testable.

## Links

- [Review and acceptance gates](../plans/sep_7_plan.md)
- [Implementation ledger](../plans/libghostty_vt_adoption_progress.md)
- [Compression measurements](../plans/libghostty_vt_adoption_measurements.md)
- [Per-flow local QA evidence](../plans/libghostty_vt_adoption_qa.md)
- [Qualification follow-up](../TODO.md)
