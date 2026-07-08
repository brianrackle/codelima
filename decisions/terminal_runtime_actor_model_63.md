# Run each live terminal as a single-goroutine runtime actor

## Context and Problem Statement

The Ghostty embedded terminal mutated its PTY and libghostty-vt emulator from several goroutines at once — the TUI event loop (Update/Resize/Focus/Blur/Close), a read loop that ingested PTY output, a queued PTY writer, and redraw timers — all coordinated by one mutex. That shape has no seam for reading terminal content without a TUI attached, which the daemon (Track 3) and agent detection (Track 5) both require, and its production writer was constructed with a nil writable-waiter, so a would-block PTY write busy-spun instead of waiting on POLLOUT. How should terminal I/O be organized so one owner mutates the live state, content is queryable without a UI, and observable behavior does not change?

## Decision Drivers

* One owner for live PTY/emulator mutation, so the daemon can later take that owner out of the TUI process without re-auditing every lock.
* Terminal content must be readable and snapshottable with no TUI attached — the automation and agent-detection seam.
* The `tuiTerminal` interface, all existing Ghostty cgo tests, and the identity characterization suite must pass unmodified: reorganization, not rewrite.
* `Draw` runs on the render loop every frame and needs a consistent emulator view without deadlocking against the new owner.
* The EAGAIN busy-spin in the PTY writer must be fixed with a real POLLOUT wait.
* Track 4 (live update) will need quiesce/handoff states; the command loop should not need reshaping to add them.

## Considered Options

* Keep the mutex-coordinated design and bolt read/snapshot methods onto it.
* Full actor: every interface method, including Draw, becomes an asynchronous command; Draw renders from an actor-published cell snapshot.
* Actor for mutation with synchronous command delivery; Draw and the read-only methods stay synchronous under a narrow lock the actor also honors.

## Decision Outcome

Chosen option: "Actor for mutation with synchronous command delivery; Draw stays synchronous under a narrow lock the actor also honors", because it establishes the single-owner command channel the daemon needs while provably changing no observable behavior — the existing test suite pins `Update`/`Resize`/`Focus` as synchronously applied and pins exact rendering, and a snapshot-published Draw could not satisfy either without rewriting tests.

The shape (`tui_terminal_runtime_actor.go` + `tui_terminal_ghostty_cgo.go`):

* One actor goroutine per terminal (`runActor`), started at construction, is the sole live-path mutator of PTY + emulator. A dumb read pump goroutine forwards PTY bytes to the actor; the queued PTY writer goroutine remains separate so a full-duplex PTY cannot deadlock ingest against write.
* Command set (daemon-shaped, plan §3.3): `cmdInput`, `cmdResize`, `cmdFocus`, `cmdScroll`, `cmdRead{Source, Format, Reply}`, `cmdSnapshot{Reply}`, `cmdClose`, plus the internal `cmdUpdate` (vaxis events needing mode-aware encoding). `cmdBeginHandoff` / `cmdReleaseAfterHandoff` / `cmdRollbackHandoff` and the state enum `Running → Quiescing → Quiesced → Released` are declared but deliberately unimplemented; Track 4 fills in the dispatch arms without reshaping the loop.
* Interface methods are thin: `Update`/`Resize`/`Focus`/`Blur` enqueue and wait for an applied-ack (preserving their pre-actor synchronous semantics); `Close` enqueues `cmdClose`, which routes teardown through the Track 0.1 `shutdownTerminalProcess` helper (group-kill preserved), and falls back to running the same idempotent teardown directly when the actor already exited from child self-exit — so surviving HUP-immune grandchildren are still reaped.
* Draw consistency: `Draw`/`String`/`HyperlinkAt`/`CapturesMouse` take the terminal mutex directly; every actor handler takes the same mutex around its mutation. Neither side ever holds the mutex across a channel operation, and lock order is everywhere `t.mu` → the process-global libghostty stderr mutex, so no cycle exists; Draw and the actor merely contend, never deadlock. A blocked `sendSync` always unblocks: the actor either acks the command or exits and closes `actorDone`, which every send/await selects on.
* `cmdRead` (visible viewport or recent scrollback; plain text or reconstructed-SGR ANSI) and `cmdSnapshot` (full cell grid — grapheme, fg/bg, attribute flags, cursor, and a `Generation` counter that advances per ingested output chunk) are served by the actor using the same bridge queries Draw uses. Exposed as `ReadVisible`/`ReadRecent`/`Snapshot`/`SendInput`/`Scroll`, they are the seam the daemon API and agent detection consume: terminal content is now testable with a real `/bin/sh` and no TUI.
* Busy-spin fix: production `Start` now constructs the writer with the real `waitGhosttyPTYWritable` (poll POLLOUT) instead of nil, so an EAGAIN parks in poll rather than spinning hot. Reachability note: `pty.StartWithAttrs` calls `Setsize`, whose `os.File.Fd()` flips the master to blocking mode, so production writes block in `write(2)` and never surface EAGAIN today — the waiter is latent correctness for any non-blocking-fd context (tests, future daemon fd handoff), and teardown-while-writing behavior is byte-identical to the pre-actor baseline.

### Positive Consequences

* The daemon (Track 3) maps `terminal.*` API methods one-to-one onto existing commands; agent detection (Track 5) gets pure snapshot input.
* Single-owner mutation ends the multi-goroutine write choreography; `-race` clean across the terminal suite, run repeatedly.
* Track 4 adds quiesce/handoff by implementing already-declared commands and states.
* Close semantics are strictly preserved, including the group-kill-after-self-exit edge, now pinned by a regression test.

### Negative Consequences

* Synchronous command delivery means a TUI-loop caller blocks for the duration of the command ahead of it (bounded by teardown's escalation, ~2.75s worst case) — same worst case as the old mutex design, but now serialized through one channel.
* The mutex still exists alongside the actor (for Draw and the direct-drive test seams); the pure "no locks on the hot path" actor is deferred until Draw is fed by daemon snapshots (Track 3) and the direct-ingest test seams migrate.
* `ReadANSI` reconstructs SGR per cell rather than replaying original bytes; it is a faithful rendering, not a byte-exact recording.

## Pros and Cons of the Options

### Keep the mutex-coordinated design and bolt on read/snapshot methods

Add `ReadVisible`/`Snapshot` as more mutex-taking methods on the existing structure.

* Good, because it is the smallest possible diff.
* Good, because behavior trivially cannot change.
* Bad, because the daemon would inherit N goroutines with shared mutation per terminal and no command seam — the Track 3/4 work would start by doing this refactor anyway, later and riskier.
* Bad, because the busy-spin and teardown paths stay entangled with UI-thread call sites.

### Full actor: Draw reads an actor-published cell snapshot

Every method, Draw included, becomes a command; the actor publishes immutable frame snapshots the render loop reads.

* Good, because it reaches the pure lockless actor in one step, closest to the daemon end-state.
* Bad, because Draw's output would lag the actor by one publish, and the existing render tests drive `ingestPTY` then `Draw` synchronously — they would fail without modification, which the no-behavior-change contract forbids.
* Bad, because tests pin `Update`/`Focus` as synchronously applied (e.g. a color-theme update must have queued its VT response when `Update` returns); pure async commands break them.
* Bad, because publishing a full cell grid every dirty tick costs allocations the TUI does not need yet; the daemon can add publish-on-dirty when a client actually subscribes.

### Actor for mutation, synchronous delivery, Draw under a narrow shared lock (chosen)

Single actor owns mutation via commands; interface methods ack-wait; Draw and read-only queries take the same narrow mutex the actor's handlers take.

* Good, because observable behavior is provably unchanged: all pre-existing terminal tests pass unmodified, including the characterization suites.
* Good, because the command channel, read/snapshot replies, and declared handoff states are exactly the daemon/Track 4 shape.
* Good, because the deadlock analysis is simple and checkable: no channel op under the mutex, one global lock order, every await has an `actorDone` escape.
* Bad, because the mutex survives as a transitional structure until Track 3 moves rendering onto snapshots.

## Links

* Builds on [ADR 56](kill_terminal_process_groups_not_pids_56.md) — `cmdClose` routes through `shutdownTerminalProcess`.
* Builds on [ADR 61](terminal_identity_model_and_target_key_61.md) — the registry that owns these runtimes.
* Complements [ADR 62](single_terminal_launch_contract_62.md) — launch specs feed what the actor runs.
* Consumed by Track 3 (daemon `terminal.read`/`terminal.snapshot`) and Track 5 (agent detection) per `plans/IMPROVEMENT_PLAN.md` Part F §2.1.
