# Input Ownership Plan: narrow the lease to the job that needs it

# Implementation status

Implemented on August 10, 2026 — steps 1, 2, 3, and 5 (ADR 130, README, QA
Flow 5b, TODO 39). Verification items (a)–(c) passed: no client-side event-ID
namespace exists, the terminal actor and PTY mutex serialize concurrent
senders, and per-chunk paste bracketing bounds interleaving. Step 4
(input-follows-seat) remains deliberately deferred pending the native QA 5b
pass; see TODO 39. The native two-window interactive verification also
remains (TODO 39).

Fix the intermittent `send terminal input: client is observe-only; request
input.takeover first` failures by removing their cause rather than routing
around it: the input-ownership lease currently gates every mutation, but the
only daemon state that actually requires single-writer arbitration is
replaceable per-seat state (terminal geometry and focus). Everything else the
lease "protects" is either already serialized daemon-side or already bypassed
by design. Narrowing the lease deletes the failure mode instead of adding
recovery machinery for it.

## Problem statement

The daemon holds a single input lease keyed to one client connection
(`daemon/server.go:1044`). Every non-query method from a non-owner is
rejected with the observe-only error (`server.go:1089-1090`,
`MutatingInputMethod` = everything but `ClassQuery`, `dispatch.go:65`). The
lease moves via unconditional `input.takeover` (`server.go:1080`), a
`want_input` hello against an empty lease (`server.go:691`), and clears to
nobody when the owning connection closes (`server.go:1160`).

Verified failure chains behind the reported symptom (two TUIs, one over SSH):

- The event-stream supervisor re-takes input on **every** resynchronization —
  read timeout, sequence gap, epoch change — with no regard for window focus
  (`tui_sessions.go:246`). An idle background TUI whose event stream hiccups
  silently steals the lease from the window being typed in; heavy terminal
  output raises event pressure, so it correlates with busy terminals.
- When the owning connection dies (SSH drop, sleep, CLI exit) the lease
  clears to nobody with no broadcast; every remaining client is observe-only.
- CLI `terminal open/close/send-text/send-keys` dial with `wantInput=true`
  and steal the lease (`cli_command.go:651-662`), then exit, leaving it empty.
- The only steady-state reclaim trigger is `vaxis.FocusIn` (`tui_app.go:431`,
  ADR 80) — which never fires if the user never left the window, and cannot
  fire in terminals that do not report focus (Terminal.app, tmux without
  `focus-events`).
- After the first rejection, `deliverInput` latches (`inputFailed`,
  `tui_terminal_daemon.go:246-271`) and silently drops subsequent keystrokes
  until one succeeds.

## The design question: what does the lease actually protect?

Three unrelated jobs hide behind the one gate.

1. **Keystroke exclusivity — not needed.** Input is already serialized per
   terminal by the daemon's delivery lanes (`ClassInput`, `dispatch.go:20-23`);
   concurrent senders interleave at frame granularity exactly as in a shared
   tmux session. No daemon-side correctness depends on a single input writer
   (verification items below). Rejecting the second writer is policy, and it
   is the policy producing the bug.
2. **Replaceable-state arbitration — genuinely required.** Two attached TUIs
   rarely share a window size, and each one's draw loop continuously
   reasserts its own geometry through a retrying background loop
   (`tui_terminal_daemon.go:93-116`). Without an arbiter, two different-sized
   windows fight indefinitely: a SIGWINCH storm into the shell, constant
   renderer reflow, constant dirty traffic. Someone's geometry must win.
   "Seat holder wins; observers render cropped" (`Draw` already crops via
   `min(window, snapshot)`) is that decision. This job cannot be deleted,
   only assigned differently.
3. **Control/lifecycle race-guard — already vacuous.** `terminal.open/close/
   move/restart_renderer` and `node.start/stop` are keyed, idempotent, and
   serialized by handler state locks (`ClassControl`/`ClassLifecycle`
   contracts, `dispatch.go:33-41`), and every CLI client auto-takeovers
   through the gate anyway (`cli_command.go:657-662`). A guard that every
   non-TUI client bypasses by design protects nothing; in practice it only
   ever blocks the user's own second window.

Conclusion: keep a lease, but only for job 2. Rename the concept to what it
is — the **seat**: the client whose replaceable per-view state (size, focus)
the terminal adopts.

## Goals

- **Reliability.** Typing, scrolling, pasting, and tab control succeed from
  any attached client, always — independent of focus reporting, owner death,
  CLI activity, or resyncs. The observe-only rejection becomes structurally
  impossible for those methods rather than recovered-from.
- **Performance.** Strictly less work: no takeover round-trips on input
  paths, no doomed-call/error/redraw cycles, fewer takeovers during
  reconnect storms, no seat perturbation from CLI scripts.
- **Maintainability.** Net deletion: the CLI's forced takeover, the
  unconditional resync takeover, and the error-latch recovery path all
  shrink; no new client-side belief machinery, retry wrappers, or error
  markers are added.
- **Architecture.** The daemon remains sole arbiter of one clearly named
  thing (the seat); intent enters only through explicit user signals. The
  lease's meaning becomes honest instead of accidental.

## Non-goals (considered and excluded)

- **Claim-on-input recovery machinery** (the previous revision of this
  plan): a client-side ownership belief, a `CallOwned` ensure/retry wrapper,
  and an `observe_only` error marker. Strictly dominated by ungating input:
  it builds apparatus to recover from rejections the daemon no longer needs
  to issue. Retained in git history as the fallback design if verification
  finds a hard single-writer dependency (see Risks).
- **Per-terminal seats.** The seat could be per-terminal (different windows
  driving different terminals' geometry). Deferred: multiplies state and
  handoff transfer for a refinement nobody has asked for; the narrowed
  global seat no longer damages input, so the pressure is gone.
- **tmux-style aggregate geometry** (min/max of attached clients). A policy
  change with real UX consequences (a small forgotten SSH window would
  shrink the local view). Seat-based geometry is already the shipped,
  understood behavior.
- **Reclaiming the seat in reaction to `input.revoked`.** Reactive
  reclamation is the ping-pong trap; seat transfer must always originate
  from a local user signal.
- **Replaying uncertain mutations.** Unchanged invariant (ADR 107). Nothing
  here retries anything.

## Design

### 1. Daemon: only replaceable state consults the lease

Replace the `MutatingInputMethod` gate (`server.go:1089`) with a
seat-arbitrated predicate: methods classified `ClassReplaceable`
(`terminal.resize`, `terminal.focus`) are rejected from non-seat clients —
silently ignorable state, see change 3 — while `ClassInput`, `ClassControl`,
`ClassOwnership`, and `ClassLifecycle` dispatch for any authenticated
client. `ClassifyMethod` stays the single shared vocabulary
(`dispatch.go:44-62`); the change is confined to the predicate so server and
client cannot drift.

`input.takeover`, the lease record, hello's `want_input` grant, and
clear-on-disconnect all remain wire- and semantics-identical — the lease
simply arbitrates less. Old clients that still takeover before mutating
remain correct (takeover now just moves the seat). No protocol version
change.

### 2. Clients: stop demanding the seat for non-seat work

- CLI: `withDaemonClient` callers for `terminal open/close/send-text/
  send-keys` drop `wantInput` (`cli_command.go`), so scripts and agents stop
  perturbing the seat entirely. The explicit `terminal takeover` command
  remains for operators.
- TUI: connection-time claim (ADR 78) and FocusIn claim (ADR 80) remain —
  they now transfer only the seat. The `deliverInput` error latch stays for
  genuine transport errors but can no longer engage for ownership causes.

### 3. Seat behavior for non-seat windows

- A non-seat TUI renders the seat's geometry cropped/padded, which `Draw`
  already does. Its resize reassert loop must treat the seat rejection as a
  clean "not my seat" outcome — stop retrying until seat acquisition or the
  next local size change, rather than hammering retries (audit
  `reassertResize` backoff and error surfacing so no footer noise appears).
- Conditional resync reclaim: replace the unconditional takeover in
  `prepareDaemonSynchronization` (`tui_sessions.go:246`) with
  `reclaim iff (held the seat entering the sync) OR (window focused)`.
  Focus is a `tuiApp`-owned flag (default true; set by `vaxis.FocusIn` and a
  new `vaxis.FocusOut` case; published via atomic for the off-loop sync
  path). A background window's event-stream hiccup then cannot move the
  seat, so the foreground's geometry never flaps. Terminals that never
  report focus keep the flag true and degrade to exactly today's behavior.
- Optional (separate commit, after the above proves out): piggyback a seat
  claim on locally originated input, so in focus-eventless terminals the
  geometry also follows typing. Pure QoL — with input ungated it carries no
  correctness weight — and it is the only acceptable form of
  "claim-on-input": fire-and-forget, ordered ahead of the input by
  `ClassOwnership`'s read-order guarantee (`dispatch.go:29-32`).

## Behavior matrix

| Trigger | Today | After |
| --- | --- | --- |
| Background TUI resyncs (gap/timeout/update) | Steals lease; foreground typing errors, then keys silently dropped until refocus | Cannot reject input at all; seat also stays put (conditional reclaim) |
| Second TUI launched | Steals; first window stranded until refocus | Both windows type freely; seat follows launch/focus as before |
| Seat holder dies (SSH drop, sleep, CLI exit) | Everyone observe-only until a refocus | Typing unaffected; seat re-established by next hello/focus/explicit claim |
| CLI `terminal *` invocation | Steals lease, strands TUI | No seat interaction at all |
| Terminal without focus reporting | Permanent strand once revoked | Typing always works; geometry follows seat (optional input-claim closes even that) |
| Two humans typing simultaneously | Second window hard-rejected | Interleaved per-terminal, tmux semantics — accepted and documented |
| Resize churn from a background window | Never steals (ADR 80 driver) | Unchanged: non-seat resize is rejected/ignored, never adopted |

## Risks and verification items

- **Hidden single-writer assumptions on the input path.** Before step 1
  lands, verify: (a) input event-ID scoping — renderer-response
  deduplication and "fire-and-forget inputs keep distinct event IDs" must
  hold with two sending connections (IDs must be daemon- or
  connection-scoped, not global-client-assumed); (b) bracketed-paste
  chunking (ADR 81) — a multi-frame paste from one client can interleave
  with another client's keys; accepted for v1 (single-human probability ~0,
  tmux-equivalent), documented in the ADR, with per-terminal paste
  coalescing noted as the follow-up if it ever bites; (c) the "accepted
  input drain" on terminal close with two senders. If (a) or (c) reveal a
  real dependency, fall back to the previous revision's claim-on-input
  design (git history) rather than weakening the drain/dedup contracts.
- **Tests encoding the old gate.** Suites assert observe-only rejections and
  reconnect-implies-owner (handoff, two-TUI integration, ADR 78/80
  coverage). The audit is part of step 1/3, and each assertion is updated
  deliberately to the seat semantics, not deleted.
- **Version skew during live update.** Old client + new daemon: takeovers
  still succeed, nothing rejected that used to succeed. New client + old
  daemon (brief handoff window): a CLI without `wantInput` could hit
  observe-only on an old daemon — keep the CLI change behind the same
  release as the daemon change and document the one-release ordering; the
  TUI sides are compatible in both directions.
- **Seat starvation UX.** A user typing in a focus-eventless window sees
  cropped geometry until the optional input-claim ships; called out in the
  README update so it is a known tradeoff, not a surprise.

## Implementation steps

1. **Daemon predicate + contract tests.** Narrow the gate to
   `ClassReplaceable`; add tests that (a) input/control methods dispatch
   from a non-seat client, (b) resize/focus from a non-seat client are
   rejected and the handler is never invoked, (c) the seat lease lifecycle
   (takeover, hello grant, clear-on-disconnect) is unchanged. Run the
   event-ID and input-drain verification items alongside.
2. **CLI de-escalation.** Drop `wantInput` from the terminal subcommands;
   integration test that a `terminal send-text` during active TUI typing
   moves neither input nor seat.
3. **TUI seat behavior.** Conditional resync reclaim + FocusOut tracking +
   non-seat resize quiescence; audit and update the suites that assumed the
   old semantics. Integration regressions: forced background resync moves
   nothing; typing continues across seat-holder death; two-client
   interleaving preserves per-client order.
4. **Optional input-follows-seat commit** (fire-and-forget takeover ahead of
   input when not seat holder), only after 1–3 soak.
5. **Docs.** New ADR "Narrow the input lease to seat arbitration" amending
   ADRs 78 and 80 (blockquote amendments, house style): the three-jobs
   analysis, tmux precedent for shared input, ping-pong rule (seat moves
   only on local user signals), rejected alternatives (claim-on-input
   machinery, per-terminal seats, aggregate geometry). Update README's
   input/reconnect paragraphs and SSH guidance; extend QA Flow 5/7 with a
   two-window sub-flow (background resync, CLI interference, seat-holder
   kill, focus-eventless variant); TODO entry for the native two-machine
   manual pass this environment cannot run.

## Acceptance criteria

- With two attached TUIs (one focus-eventless, one over SSH), background
  resyncs, CLI `terminal` commands, and seat-holder kills: zero
  observe-only errors, zero silently dropped keystrokes, geometry stable in
  the focused window.
- `codelima terminal send-text` during live typing is interleaved, not
  rejected, and does not move the seat.
- The words "observe-only" appear daemon-side only on `ClassReplaceable`
  rejections; grep confirms no input-path caller handles that error anymore.
- All suites green; `make fmt-check lint test` clean; ADR and QA updates
  landed with the code.
