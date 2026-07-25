# Treat daemon connections as disposable, resumable sessions

## Context and Problem Statement

A failed Unix-socket connection made every daemon-owned terminal presented by one TUI appear frozen even when the daemon, PTYs, and shells were healthy. The TUI treated a physical connection as permanent, daemon broadcast wrote too close to client delivery, and the protocol had no authoritative reconnect cut.

## Decision Drivers

* A broken or suspended client must not affect terminals or other clients.
* Reconnection must never replay a mutation whose outcome is uncertain.
* A reconnect must install one authoritative daemon epoch and state sequence.
* The original connection failure must remain diagnosable after secondary EOF or broken-pipe errors.
* Slow requests, readers, and event consumers need bounded, independent lanes.

## Considered Options

* Require users to restart the TUI after every disconnect.
* Retry failed calls on the existing client object.
* Supervise disposable physical connections beneath one stable logical client identity.

## Decision Outcome

Chosen option: "supervise disposable physical connections beneath one stable logical client identity", because it contains routine transport failure without changing terminal ownership or duplicating input.

The handshake now carries a stable client instance ID, physical connection generation, daemon epoch, connection ID, and state sequence. An event connection subscribes by receiving a compact authoritative state cut before later sequenced events. Sequence gaps, epoch changes, missed heartbeats, EOF, update commit, and permanent protocol failures cause a new physical connection and full resynchronization.

Each daemon client has one bounded outbound pump. Broadcast only attempts nonblocking enqueue. Queries have bounded concurrent dispatch, mutations—including input takeover—retain an ordered lane, and one writer owns the socket. The request client has one response reader and an ID-keyed pending registry, so a blocked request does not serialize ping or terminal input. The connection supervisor does not publish ready until the TUI actor acknowledges that it installed the authoritative synchronization cut.

Submitted mutations are never replayed automatically. Delivery errors distinguish not-sent operations from unknown outcomes.

### Positive Consequences

* A TUI reconnects without replacing or closing daemon-owned terminals.
* One client that stops reading is disconnected without delaying another client.
* Full state replacement removes terminals that disappeared while disconnected and preserves surviving terminal IDs.
* Daemon and client close records correlate connection ID, logical identity, epoch, first cause, queue state, and byte/frame progress.
* Heartbeats detect a silent link even when no terminal events occur.

### Negative Consequences

* The protocol version changes and old daemons require update or restart.
* Reconnect logic has explicit synchronizing periods during which mutations are rejected.
* A timed-out mutation may require user reconciliation because its outcome remains unknown by design.

## Pros and Cons of the Options

### Require TUI restart

* Good, because it is simple.
* Bad, because a routine socket failure still presents as a global terminal freeze.
* Bad, because live daemon update remains disruptive to attached TUIs.

### Retry failed calls

* Good, because reads can often recover quickly.
* Bad, because retrying terminal input or close/open can duplicate effects.
* Bad, because it does not define authoritative state replacement.

### Disposable, resumable sessions

* Good, because transport lifetime is separated from terminal lifetime.
* Good, because state and delivery semantics are explicit and testable.
* Bad, because it requires a supervisor, sequence fencing, and pending-request demultiplexing.

## Links

* Refines [persistent authenticated daemon connections](persistent_authenticated_daemon_connections_75.md).
* Supersedes the permanent-disconnect behavior in [latch TUI daemon disconnect before focus takeover](latch_tui_daemon_disconnect_before_focus_takeover_102.md).
