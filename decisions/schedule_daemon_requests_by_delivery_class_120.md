# Schedule daemon requests by delivery class

Status: Accepted

## Context and Problem Statement

Every daemon connection carried one serial mutation lane. `terminal.send_event`
shared that lane with `terminal.open`, `terminal.close` (up to five seconds of
SIGHUP/SIGTERM/SIGKILL escalation) and `daemon.update` (minutes). Because the
TUI multiplexes every tab over one client, closing a tab whose shell ignores
SIGTERM froze keystrokes for every other tab, and 64 queued mutations dropped
the whole connection. The same connection also applied one 30-second
`RequestTimeout` to every method, so a daemon-dispatched `node.start` was
cancelled mid-boot, the `limactl` process group was killed, and the node was
persisted as failed.

A connection is a transport boundary, not a serialization boundary. How should
one connection schedule requests so that slow control work cannot delay input,
and so that legitimately long lifecycle work is not cancelled by a deadline
meant for fast control operations?

## Decision Drivers

* A slow control operation must never delay input delivery or another
  terminal's requests on the same connection (stability invariant I3).
* Terminal input is at most once and ordered; it must never be silently
  dropped, and it must never be replayed automatically.
* Resize and focus are latest-value state; a burst of them must not queue.
* Node and daemon lifecycle operations legitimately run for minutes and carry
  their own internal budgets.
* A stuck terminal must not cost a client its connection, and therefore its
  whole session view.
* The client and the daemon must not maintain two hand-copied lists of
  mutating methods that can silently drift.

## Considered Options

* Keep one serial mutation lane and only raise the queue bound and the request
  timeout.
* Dispatch every request concurrently.
* Schedule by delivery class: a per-terminal serial input lane, a coalescing
  latest-value lane, and concurrent dispatch for control and lifecycle work,
  with a per-method timeout class.
* Run node lifecycle as daemon-side background jobs that stream progress
  events.

## Decision Outcome

Chosen option: "schedule by delivery class", because the delivery classes
already described in `plans/STABILITY_PLAN.md` §6 map directly onto the
scheduling each method actually needs, and because it removes the head-of-line
blocking without giving up the ordering guarantees input depends on.

`daemon.ClassifyMethod` is now the single definition of a method's class, and
`daemon.MutatingInputMethod` is derived from it, so the daemon's ownership gate
and the client's delivery-outcome reporting can no longer disagree. `node.start`
and `node.stop` join the mutating set they were missing from.

| Class | Methods | Scheduling |
|---|---|---|
| Input (at most once, ordered) | `terminal.send_text`, `terminal.send_keys`, `terminal.send_input`, `terminal.send_event`, `terminal.scroll` | One serial lane per terminal id |
| Replaceable (latest value wins) | `terminal.resize`, `terminal.focus` | One coalescing lane per terminal id; superseded requests observe the winning result |
| Ownership (read-order fact) | `input.takeover` | Applied inline on the connection reader |
| Control (ordering held by the handler) | `terminal.open`, `terminal.close`, `terminal.move` | Concurrent, bounded by `MaxInFlight` |
| Lifecycle (minutes, own budgets) | `node.start`, `node.stop`, `daemon.update` | Concurrent, bounded by `MaxInFlight`, `LifecycleRequestTimeout` |
| Query (read only) | everything else | Concurrent, bounded by `MaxInFlight` |

`input.takeover` keeps its documented ordering guarantee by being applied on
the connection reader itself rather than on a lane, so an input frame that
follows a takeover on the wire can never be handled before it.

`RequestTimeout` (30 seconds) now bounds every method outside the lifecycle
class. `LifecycleRequestTimeout` defaults to zero, which applies no
daemon-imposed deadline: node boot and live update are bounded by their own
internal budgets and by the client connection's lifetime. The daemon client
applies the same class, so its blanket request timeout no longer abandons a
lifecycle call either.

`terminal.close` now replies as soon as the daemon-owned state transition is
committed and finishes signal escalation in the background. Daemon shutdown
still waits for those teardowns inside its existing bounded deadline.

### Positive Consequences

* Closing a tab with a signal-immune shell no longer freezes input anywhere.
* A live update no longer blocks every other request on its connection, and can
  no longer be cancelled at 30 seconds by the request timeout.
* A daemon-dispatched `node.start` survives a multi-minute VM boot.
* A saturated terminal reports the loss on the offending request instead of
  dropping the connection and forcing a full resynchronization.
* A burst of window-drag resizes collapses to the geometry the client last
  asked for, while every request still receives a response.
* `node.start` and `node.stop` are covered by the input-ownership gate.

### Negative Consequences

* Control operations on one connection now interleave. `terminal.close` and a
  keystroke for the same terminal race, so a keystroke may be answered
  `NotFound` instead of delivered; that is the correct at-most-once outcome but
  it is newly observable.
* A superseded resize is answered with the geometry that actually won, not the
  geometry it asked for.
* `terminal.close` no longer reports a teardown that exceeded its deadline in
  the response; it is logged instead.
* `daemon.update` runs concurrently with `terminal.open` on the same
  connection, widening a pre-existing cross-connection window in which a tab
  created during handoff is not in the manifest.

## Pros and Cons of the Options

### Keep one serial mutation lane with larger bounds

* Good, because it is the smallest change and preserves total ordering.
* Bad, because head-of-line blocking is the defect; a larger queue only delays
  the freeze.
* Bad, because one timeout cannot serve both a keystroke and a VM boot.

### Dispatch every request concurrently

* Good, because nothing can block anything else.
* Bad, because keystroke ordering within a terminal is lost, which corrupts
  typed input.

### Schedule by delivery class

* Good, because each class gets exactly the guarantee it needs: ordering for
  input, coalescing for latest-value state, concurrency for control.
* Good, because ordering for control operations is already enforced by the
  handler's own state lock.
* Good, because the class table also supplies the timeout class and the
  ownership gate from one definition.
* Bad, because it introduces per-connection lane bookkeeping and a lane cap.

### Daemon-side background jobs for node lifecycle

* Good, because progress could be streamed and the operation would survive a
  client reconnect.
* Bad, because it needs a job registry, job identity, progress events, and
  client-side adoption, none of which exist today.
* Bad, because it does not address input head-of-line blocking at all, so the
  lane work would still be required.

## Links

* Implements invariant I3 of [plans/STABILITY_PLAN.md](../plans/STABILITY_PLAN.md)
  and findings 1d and 1e of [plans/ISSUES_PLAN.md](../plans/ISSUES_PLAN.md).
* Refines [ADR 107](treat_daemon_connections_as_disposable_resumable_sessions_107.md).
