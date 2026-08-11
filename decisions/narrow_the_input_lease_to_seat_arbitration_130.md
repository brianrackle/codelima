# Narrow the input lease to seat arbitration

## Context and Problem Statement

Users working in a codelima terminal hit intermittent `send terminal input:
client is observe-only; request input.takeover first` failures, and after the
first one the input drain's error latch silently dropped further keystrokes
until one succeeded. The trigger was never the user's own action: the daemon's
single input lease gated every non-query method, and it moved underneath the
working window whenever any other client touched it — a second TUI's
event-stream resynchronization re-taking input unconditionally on every read
timeout, sequence gap, or epoch change; a CLI `terminal` command dialing with
`want_input`, stealing the lease, and leaving it empty on exit; or the owning
connection dying and clearing the lease to nobody. The only steady-state
recovery was the `vaxis.FocusIn` takeover (ADR 80), which never fires when the
user never left the window, and cannot fire in a terminal that does not report
focus.

ADR 78 made a connecting interactive TUI claim the lease and ADR 80 made
window focus move it. Both treated the lease as "the right to interact". This
decision asks what the lease actually protects.

## Decision Drivers

* Typing in an attached TUI must always work — independent of focus
  reporting, other windows' connection health, CLI activity, or lease death.
* Two attached windows rarely share a geometry, and each continuously
  reasserts its own; exactly one client's replaceable per-view state may win
  or the guest shell lives in a SIGWINCH storm.
* Background activity (resync, redraw, resize reassert, polling) must never
  move any arbitration the user experiences — ADR 80's driver, kept.
* Recovery paths that replay mutations with ambiguous outcomes remain
  forbidden (ADR 107).
* No wire or protocol version change; live-update skew must degrade to
  today's behavior, never worse.

## Considered Options

* Keep the global gate and add client-side recovery: an ownership belief, an
  ensure-ownership wrapper on user-intent mutations, and a retry-once on the
  observe-only rejection.
* Narrow the lease to replaceable per-view state (geometry, focus) and stop
  gating input, control, and lifecycle methods.
* Per-terminal leases.
* tmux-style aggregate geometry (min/max of attached windows) instead of any
  lease.

## Decision Outcome

Chosen option: **narrow the lease to replaceable per-view state**, because
the gate hid three unrelated jobs and only one of them needs arbitration:

1. **Keystroke exclusivity — dropped.** Input is serialized per terminal by
   its delivery lane, and the terminal actor and PTY writer serialize
   concurrent callers; two clients typing interleave at frame granularity
   exactly as in a shared tmux session. Rejecting the second writer was
   policy, and it was the policy producing the failures.
2. **Replaceable-state arbitration — kept, and named.** The lease becomes the
   **seat**: the client whose window geometry and focus state the terminal
   adopts. `SeatArbitratedMethod` (exactly `ClassReplaceable`:
   `terminal.resize`, `terminal.focus`) is the only gate consulting it.
3. **Control/lifecycle race-guard — dropped as vacuous.** Those methods are
   keyed, idempotent, and serialized by handler locks, and every CLI client
   walked through the gate via automatic takeover anyway; the gate only ever
   blocked the user's own second window.

The lease record, `input.takeover`, hello's `want_input` grant, and
clear-on-disconnect are wire-identical; the lease simply arbitrates less. An
old client that still takes over before mutating remains correct — takeover
now just moves the seat.

### Seat movement stays a user signal

* Connection-time claim (ADR 78) and FocusIn claim (ADR 80) remain, now
  transferring only the seat.
* The resynchronization takeover is no longer unconditional. The supervision
  ping already returns the current seat holder, so the client decides from
  authoritative daemon state: reclaim only when it does not hold the seat
  **and** its window is focused. A terminal that never reports focus leaves
  the flag at its initial `true` and keeps the old behavior; a window that
  already holds the seat skips the ownership RPC entirely.
* Nothing reclaims in reaction to `input.revoked` — reactive reclamation is
  the ping-pong shape. Every seat movement originates from a local user
  signal, so ownership cannot oscillate without fresh user action.

### Non-seat windows and the parked resize

A non-seat window renders the seat holder's geometry cropped, as `Draw`
always has. Its resize reassert loop now treats the seat rejection as a clean
"not my seat" outcome: it parks instead of retrying on the interval
(previously an observe-only background window re-sent its rejected geometry
every 250ms, forever). Seat acquisition — focus takeover or a focused
resynchronization — pokes every parked loop so the winning window's geometry
is re-presented immediately.

### CLI de-escalation

`terminal open/close/send-text/send-keys` no longer dial with `want_input`:
scripts and agents interleave with a TUI instead of stranding it, and never
perturb the seat. The explicit `codelima terminal takeover` command remains
the operator's manual seat transfer.

### Accepted interleavings

Two clients typing into one terminal interleave per-frame (tmux semantics). A
multi-chunk bracketed paste (ADR 81) is self-bracketed per chunk daemon-side,
so another client's keys can land between chunks but never inside one;
single-operator probability is negligible and per-terminal paste coalescing
is the recorded follow-up if it ever bites.

### Positive Consequences

* The observe-only failure is structurally impossible for typing, scrolling,
  pasting, and tab control — not recovered from, removed. The input drain's
  silent-drop latch can no longer engage for ownership causes.
* Terminal.app and tmux-without-focus-events windows stop being strandable:
  focus events now only tune seat placement, they no longer gate typing.
* Strictly less traffic: no ownership RPC on already-seated resyncs, no
  250ms rejected-resize retry loop from background windows, no seat churn
  from CLI scripts.
* The lease's name finally matches its behavior, and the gate predicate
  lives beside the delivery classes both sides already share.

### Negative Consequences

* Concurrent typists into one terminal are interleaved, not rejected. That
  is a human-coordination situation, and last-typer-wins is the correct
  arbitration for it — but the old gate did announce the conflict.
* A user typing in a focus-eventless window keeps the seat holder's cropped
  geometry until focus or an explicit takeover moves the seat; input works
  throughout. A fire-and-forget seat claim piggybacked on input (safe under
  `ClassOwnership`'s read-order guarantee) is the recorded follow-up if this
  bites in practice.
* One release-ordering constraint: the CLI's `want_input` removal must not
  ship ahead of the daemon predicate change, or a new CLI against an old
  daemon hits the old gate during the update window.

## Pros and Cons of the Options

### Keep the gate, add client-side recovery machinery

* Good, because the daemon's semantics stay untouched.
* Bad, because it builds belief tracking, an ensure/retry wrapper, and an
  error marker to recover from rejections the daemon does not need to issue.
* Bad, because recovery apparatus is rarely exercised and rots; removal is
  exercised on every keystroke.

### Narrow the lease to seat arbitration

* Good, because it deletes the failure mode and net-deletes code.
* Good, because the seat — the one genuine conflict — keeps exactly the
  arbitration it always had.
* Bad, because cross-client input interleaving becomes possible (accepted,
  tmux precedent).

### Per-terminal seats

* Good, because different windows could drive different terminals' geometry.
* Bad, because it multiplies lease state, revocation semantics, and handoff
  transfer for a refinement nothing demands once input is ungated. The seat
  mechanism is unchanged by granularity, so this remains open as a later
  refinement.

### Aggregate geometry (tmux min/max)

* Good, because no client ever holds a seat at all.
* Bad, because a small forgotten SSH window silently shrinks the local view —
  a policy change with real UX cost, replacing a shipped, understood behavior.

## Links

* Amends [ADR 78](claim_daemon_input_when_an_interactive_tui_connects_78.md) —
  connection-time claim now transfers only the seat.
* Amends [ADR 80](reclaim_daemon_input_when_a_tui_window_gains_focus_80.md) —
  focus still moves the seat; typing no longer depends on it.
* Preserves [ADR 107]'s non-replay invariant: nothing here retries any
  mutation.
* Relates to [ADR 81](preserve_bracketed_paste_across_daemon_terminals_81.md) —
  the per-chunk bracketing that bounds paste interleaving.
