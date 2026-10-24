# Record native route and capacity Activity

## Context and Problem Statement

The design includes forwarding and host capacity events even when no TUI is
open. Frontend notices cannot supply that history. Forwarding reconciliation
must not wait for journal I/O, and a daemon must not write before acquiring
process ownership or after handing it to another process.

## Considered Options

* Infer events from frontend polling.
* Write journal records synchronously inside forwarding callbacks.
* Enqueue observed transitions and persist them through an owned daemon worker.

## Decision Outcome

Use a bounded daemon queue with stable per-transition message IDs. Forwarding
publishes a typed route/localhost-owner projection after releasing its mutex.
The producer compares successive projections and records additions, removals,
degradation, restoration, unavailable host ports and localhost ownership changes.
Unchanged reconciliation produces no messages. A separate five-second global
capacity observer reuses the same observed allocation calculation as the TUI.
It records CPU/memory overcommit and confirmed recovery; missing observations
cannot imply recovery. An already-overcommitted host is reported at startup.

The server invokes its Ready callback only after acquiring the process lock and
binding private listeners. Native workers start at that boundary. They acquire
the host update barrier before sampling or writing, skip a retired predecessor,
and publish committed messages only after the journal acknowledges them. An
importer opens publication after sending its committed response. If rollback
cannot recover process ownership, the predecessor retires, rejects mutations
and shuts down without rewriting shared session state.

The pending queue holds at most 256 messages. Persistence failures retry the
same IDs. Overflow drops new events and records an explicit count when capacity
returns. Pending memory is not guaranteed across a crash; committed records use
the existing seven-day/10,000-record journal. Graceful shutdown stops observers
and attempts to drain the queue while ownership is still held.

Protocol 16 adds `activity.message`, carrying the existing journal acknowledgment
shape. TUIs use epoch/sequence deduplication, refresh history and display native
warnings/errors in the six-second footer without appending a second copy.
Routine informational events do not replace a warning in the footer.

### Positive Consequences

* Route and capacity history no longer depends on an open frontend.
* Disk latency does not hold the forwarding or event-producer mutex.
* Retry, overflow and missing resource observations remain explicit.
* Ordinary event polling does not flood history or frontend notices.

### Negative Consequences

* Pending events can be lost if the process exits before persistence succeeds.
* A full queue preserves earlier pending records and omits later transitions.
* Startup reports current overcommit and newly established routes again.
* Host capacity follows the runtime observer's cached evidence, then the
  five-second sampling interval; periodic runtime reconciliation can add delay.

## Pros and Cons of the Options

Frontend inference is simple but misses unattended changes and duplicates events
across windows. Direct journal writes preserve callback order but put filesystem
latency inside runtime work. A daemon queue separates those responsibilities at
the cost of bounded delivery state and explicit ownership/shutdown handling.

## Links

* [Shared Activity journal](share_bounded_activity_messages_with_receipt_history_165.md)
* [Failed handoff cache recovery](reload_activity_after_failed_daemon_handoff_167.md)
