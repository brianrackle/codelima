# Replace Idle Terminal Polling and Broken Event-Stream Spins

Status: Accepted

## Context and Problem Statement

Every daemon-backed TUI tab requested a complete terminal cell-grid snapshot every 50 milliseconds, including hidden and unchanged tabs. Each request serialized through the client, made the daemon ask the Ghostty actor to assemble a full grid, encoded that grid as JSON, decoded it in the TUI, and rebuilt plain text; the cost multiplied by tab count and open TUI count. Separately, when daemon update or restart permanently closed an event connection, the TUI treated immediate `EOF` like a read timeout and retried it without delay, pinning a CPU core.

## Decision Drivers

* An idle tab must cause no recurring client or daemon work.
* Hidden tabs must not pull full cell grids merely because their child produced output.
* Visible terminal output must retain the established 20-frames-per-second snapshot ceiling and render only fresh daemon state.
* The lockless terminal runtime registry must remain owned by the TUI event loop.
* Normal idle event-stream timeouts are recoverable; permanent socket closure is not a retryable read.
* Until automatic daemon reconnection is implemented, a disconnected TUI must become idle and tell the user to reopen it.

## Considered Options

* Increase the fixed polling interval and add sleep after every event error.
* Keep polling only the active terminal and exponentially back off event errors.
* Pull coalesced snapshots from daemon dirty events, defer hidden tabs until visible, and terminate an event reader after one permanent failure.

## Decision Outcome

Chosen option: "pull coalesced snapshots from daemon dirty events, defer hidden tabs until visible, and terminate an event reader after one permanent failure", because it removes idle work instead of merely reducing its frequency and distinguishes expected read deadlines from a connection that can never recover.

Daemon dirty and resize notifications cross into the Vaxis event queue before looking up a terminal runtime. On the single-owner UI loop, the matching adapter advances a dirty version. The active adapter wakes its snapshot worker immediately; a hidden adapter retains only the dirty version and wakes when its first visible `Draw` requests the pending state. A capacity-one wake channel coalesces bursts, and the worker retains the previous 50-millisecond minimum interval while output is active.

The event reader retries only errors implementing `net.Error` with `Timeout() == true`. Context cancellation exits silently; `daemon.shutdown` and `daemon.update_committed` are terminal events; any other permanent read failure is reported once and ends the goroutine. This decision does not add automatic request/event reconnection or replay ambiguous terminal input.

### Positive Consequences

* Idle tabs make zero recurring terminal snapshot RPCs regardless of tab or TUI count.
* Hidden output avoids full-grid assembly, JSON encoding/decoding, and text reconstruction until the user displays that tab.
* Active output still coalesces and remains capped at the established snapshot cadence.
* A daemon update cannot turn `EOF` into a tight client-side retry loop.
* Terminal registry access remains on its documented single-owner goroutine.

### Negative Consequences

* A hidden tab's client-side snapshot is intentionally stale until that tab becomes visible; the daemon remains the authoritative up-to-date emulator.
* Snapshot delivery now depends on the subscribed dirty-event stream while a tab is visible.
* An already-open TUI still requires quit and reopen after daemon update; it now reports that recovery and consumes no core while waiting.
* Each daemon terminal keeps one sleeping snapshot worker and a capacity-one wake channel.

## Pros and Cons of the Options

### Increase the fixed polling interval and sleep after every event error

* Good, because it is a small change.
* Bad, because every idle and hidden tab still creates recurring full-grid work.
* Bad, because sleeping and retrying `EOF` cannot restore a dead socket and delays the inevitable diagnostic.

### Keep polling only the active terminal and exponentially back off event errors

* Good, because it bounds idle work independently of tab count.
* Good, because transient errors can recover without user action.
* Bad, because the active terminal still performs full-grid work when nothing changes.
* Bad, because this event connection has no reconnect operation; backoff only retries the same dead descriptor.

### Pull coalesced snapshots from dirty events and terminate permanent read failures

* Good, because work follows terminal state changes rather than elapsed time.
* Good, because the daemon's current snapshot is sufficient to absorb any number of coalesced dirty notifications.
* Good, because timeout and permanent-error behavior match the actual socket lifecycle.
* Bad, because active/hidden routing and dirty-version bookkeeping add client-side state.

## Links

* Refines [Move terminal runtime ownership into a per-home daemon](daemon_owned_terminal_runtimes_64.md)
* Refines [Queue daemon terminal input off the TUI event loop](queue_daemon_terminal_input_off_the_tui_event_loop_82.md)
* Preserves the geometry idempotence from [Make daemon terminal geometry updates edge-triggered and idempotent](edge_trigger_daemon_terminal_geometry_68.md)
* Leaves automatic reconnection from [Use the caller binary for cross-protocol daemon update](use_the_caller_binary_for_cross_protocol_daemon_update_84.md) as follow-up work
