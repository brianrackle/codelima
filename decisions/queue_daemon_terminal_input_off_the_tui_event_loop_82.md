# Queue Daemon Terminal Input off the TUI Event Loop

Status: Accepted

## Context and Problem Statement

The daemon-backed TUI sent every ordinary key or mouse event with a synchronous `terminal.send_event` request while handling the Vaxis event. All requests on a daemon client share one serialized connection, including terminal snapshot polling, so a key could stall the whole UI behind an in-flight request. The TUI then redrew immediately even though the daemon had not published a new snapshot, adding work while showing stale terminal state and making typing feel behind the user.

## Decision Drivers

* Keep ordinary typing responsive when snapshot requests or daemon work briefly contend for the request connection.
* Preserve exact key order and semantic Vaxis key data for Ghostty's mode-aware encoder.
* Preserve paste ordering and the semantic paste boundary established by ADR 81.
* Ensure terminal detach and close do not discard already accepted input.
* Avoid stale redraw work on the latency-sensitive input path.

## Considered Options

* Keep synchronous per-event requests and immediate redraws.
* Batch ordinary keys into a new daemon protocol message.
* Queue semantic input per terminal on an ordered worker and redraw only after a fresh snapshot arrives.

## Decision Outcome

Chosen option: "Queue semantic input per terminal on an ordered worker and redraw only after a fresh snapshot arrives", because it removes daemon latency from the Vaxis event loop without weakening Ghostty's ownership of key encoding or expanding the private protocol. Each terminal lazily starts one worker, appends accepted key, mouse, and paste requests to its ordered queue, and lets that worker perform the existing `terminal.send_event` calls. Detach closes admission, drains the queue, and waits for the worker before returning.

Terminal payload keys and paste boundaries no longer cause an immediate TUI draw. The daemon adapter's snapshot loop posts `vaxis.Redraw` when it observes a new terminal generation, so the next paint contains the resulting terminal state. CodeLima shortcuts still draw immediately because they change client-owned UI state.

### Positive Consequences

* Ordinary typing no longer blocks the TUI event loop on daemon round trips or snapshot-request contention.
* Keys, paste chunks, and mouse events retain their original order.
* The UI avoids redundant renders that cannot yet contain the typed character.
* No new daemon protocol version or alternate key encoder is required.
* Accepted input drains before a terminal detaches or closes.

### Negative Consequences

* Input delivery errors remain asynchronous and cannot be returned to the originating Vaxis event.
* A terminal owns one additional lazily started goroutine and an in-memory queue.
* If the daemon remains unavailable, accepted input can accumulate until each request times out or the terminal is detached.
* Visual echo still depends on the daemon producing output and the client observing a fresh snapshot.

## Pros and Cons of the Options

### Keep synchronous requests and immediate redraws

* Good, because call completion is known before the event handler returns.
* Good, because no client-side queue lifecycle is needed.
* Bad, because every key can block behind unrelated requests on the shared daemon connection.
* Bad, because the immediate redraw can only repaint the previous snapshot.

### Batch ordinary keys in the daemon protocol

* Good, because it can reduce the number of daemon calls under sustained typing.
* Good, because one message could amortize JSON framing overhead.
* Bad, because batching adds timing policy and another private protocol shape.
* Bad, because aggressive coalescing risks changing boundaries for modified keys and mode-sensitive terminal input.

### Queue semantic input and wait for fresh snapshots

* Good, because it decouples UI responsiveness from daemon latency while preserving every semantic event.
* Good, because a single worker provides a simple FIFO delivery boundary.
* Good, because snapshot-driven redraws already describe when terminal state is fresh.
* Bad, because shutdown must explicitly drain and join the worker.

## Links

* Refines [Move terminal runtime ownership into a per-home daemon](daemon_owned_terminal_runtimes_64.md)
* Preserves [Preserve bracketed paste across daemon terminals](preserve_bracketed_paste_across_daemon_terminals_81.md)
* Extends [Use nonblocking queued PTY writes for embedded Ghostty terminal](use_nonblocking_queued_pty_writes_for_embedded_ghostty_terminal_30.md)
