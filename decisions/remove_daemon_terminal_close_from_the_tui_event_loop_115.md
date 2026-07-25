# Remove Daemon Terminal Close From the TUI Event Loop

## Context and Problem Statement

Closing a daemon-backed terminal tab removed its client-side session only after
the backend synchronously drained accepted input and completed a
`terminal.close` RPC. Each accepted input call has a two-second deadline, so a
slow, reconnecting, or overloaded daemon could hold the Vaxis event loop for
multiple deadlines. The close shortcut was recognized, but the tab, cursor, and
chrome could not redraw until that remote cleanup returned.

## Decision Drivers

* A close shortcut must update the local tab model without waiting on daemon
  latency.
* New terminal input must be rejected as soon as close begins.
* Input already accepted by the ordered worker must retain its established
  drain-before-close ordering.
* One tab close must produce at most one daemon close request.
* TUI shutdown detach must continue waiting for accepted input because detach
  preserves the daemon-owned terminal rather than destroying it.

## Considered Options

* Keep synchronous drain and close on the Vaxis event loop.
* Add a shorter deadline to the synchronous close path.
* Stop input admission synchronously and finish drain plus daemon close once in
  the background.

## Decision Outcome

Chosen option: "stop input admission synchronously and finish drain plus daemon
close once in the background", because it makes the client-owned tab removal
independent of daemon health while retaining ordered delivery for input already
accepted by the terminal.

`daemonTUITerminal.Close` marks the view closed and closes its input stop signal
before returning. A single-shot background cleanup waits for the existing FIFO
input worker, then issues `terminal.close` with the existing two-second
deadline. Repeated close attempts share the same single-shot boundary.
`Detach` remains synchronous: TUI shutdown must drain accepted input before
disconnecting while leaving the daemon-owned terminal alive.

### Positive Consequences

* The active tab disappears and adjacent-tab focus can redraw immediately even
  when input delivery or the daemon close response is delayed.
* New input cannot race into a tab after the user closes it.
* Accepted input ordering and daemon close deadlines remain unchanged.
* Duplicate close invocations cannot produce duplicate daemon mutations.

### Negative Consequences

* Daemon-side terminal destruction can complete shortly after the tab has
  already disappeared locally.
* The daemon terminal view owns one short-lived cleanup goroutine after an
  explicit close.
* A close-delivery failure remains intentionally silent, matching the prior
  best-effort close interface.

## Pros and Cons of the Options

### Keep synchronous close

* Good, because daemon cleanup is complete before the method returns.
* Bad, because input drain time grows with the accepted queue.
* Bad, because daemon or connection latency freezes the entire TUI.

### Shorten the synchronous deadline

* Good, because one close call would block for less time.
* Bad, because queued input can still multiply the delay.
* Bad, because any network wait on the Vaxis event loop causes visible input
  and cursor stalls.

### Finish close in the background

* Good, because local tab state and redraw are independent of daemon latency.
* Good, because the existing FIFO worker can retain drain-before-close
  ordering.
* Good, because the same single-shot boundary makes repeated key delivery
  harmless.
* Bad, because local and daemon terminal lifetime converge asynchronously.

## Links

* Refines [Queue daemon terminal input off the TUI event loop](queue_daemon_terminal_input_off_the_tui_event_loop_82.md).
* Extends [Treat daemon connections as disposable resumable sessions](treat_daemon_connections_as_disposable_resumable_sessions_107.md).
