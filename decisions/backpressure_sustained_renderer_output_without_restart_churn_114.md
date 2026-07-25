# Backpressure Sustained Renderer Output Without Restart Churn

## Context and Problem Statement

An unthrottled fullscreen application such as `cmatrix -u 0` can produce PTY
chunks faster than a renderer worker consumes them. The terminal previously
treated a full bounded renderer queue as renderer failure, dropped the current
chunk, and requested replacement. Continued output repeated that transition
until the restart budget was exhausted and broadcast thousands of
`terminal.error` events, which could overflow a TUI event connection and cause
reconnect churn.

## Decision Drivers

* Sustained output must not classify a healthy but slower renderer as failed.
* PTY output must remain ordered and must not be silently dropped.
* A truly blocked native renderer must remain terminal-local and replaceable.
* Health and close probes must make progress even while the bulk-output queue
  is full.
* Fire-and-forget renderer mutations must not generate response traffic, but
  input events still need unique IDs for PTY-response deduplication.
* Backpressure and renderer replacement must not block daemon control or other
  terminals.

## Considered Options

* Increase the renderer queue and retain restart-on-full behavior.
* Drop output while overloaded and periodically reconstruct from the journal.
* Backpressure only the terminal-local PTY reader and reserve a control lane for
  health and lifecycle requests.

## Decision Outcome

Chosen option: "backpressure only the terminal-local PTY reader and reserve a
control lane for health and lifecycle requests", because finite queue growth
cannot solve an unbounded producer and dropping terminal bytes corrupts
emulator state.

The renderer link keeps one bounded, ordered mutation lane. When that lane is
full, the affected terminal's PTY reader waits for capacity, allowing the
kernel PTY to backpressure the child process. The daemon control plane, other
terminals, PTY input writer, and renderer supervisor continue independently.
Tracked health and lifecycle calls use a separate control lane, so a native
renderer that stops consuming mutations is still detected and killed within
the existing deadline.

Ordinary renderer mutations are fire-and-forget frames: they retain a unique
request ID but set `no_reply`, so the worker can associate input-generated PTY
responses with a stable event without sending one acknowledgement per output
chunk. On renderer replacement, the supervisor records the last journal event
included in initialization. A PTY reader waiting across that replacement skips
an event already covered by replay and resumes with the first newer event.
Restart requests are edge-triggered while replacement is active.

The worker exits after acknowledging `close`, so control-lane close priority
cannot leave backlogged output targeting a destroyed emulator.

### Positive Consequences

* Unthrottled fullscreen output no longer produces queue-full renderer
  replacements or terminal-error floods.
* Event connections are not forced to reconnect because internal overload
  errors filled their outbound mailboxes.
* Terminal output remains ordered and exact while the renderer is healthy.
* Health probes can still replace a genuinely non-returning Ghostty worker.
* Output and input notifications avoid per-mutation response frames while
  retaining unique input-response identities.

### Negative Consequences

* A child that writes faster than its renderer can consume is throttled by its
  own PTY rather than being allowed to run arbitrarily far ahead.
* The renderer link now has separate mutation and tracked-control queues plus a
  condition variable coordinating replay catch-up.
* Input or resize mutations can briefly wait behind already accepted output;
  this preserves emulator order but makes overload visible as bounded
  terminal-local latency.

## Pros and Cons of the Options

### Increase the renderer queue

* Good, because it changes little code and absorbs short bursts.
* Bad, because `cmatrix -u 0` and similar producers can eventually fill any
  finite queue.
* Bad, because the same restart and event-flood failure returns at a larger
  memory footprint.

### Drop output and reconstruct later

* Good, because the PTY reader never waits.
* Good, because the bounded journal provides a reconstruction source.
* Bad, because later bytes are applied to emulator state missing earlier
  control sequences until a restart completes.
* Bad, because an output source that never becomes idle can force permanent
  reconstruction churn.

### Backpressure the terminal-local PTY reader

* Good, because the PTY is the operating system's natural flow-control
  boundary.
* Good, because exact byte order and renderer state remain intact.
* Good, because separate tracked health traffic still detects a real native
  hang.
* Bad, because an overloaded application's output syscall may block until its
  renderer catches up.

## Links

* Refines [Isolate Ghostty in per-terminal renderer processes](isolate_ghostty_in_per_terminal_renderer_processes_108.md).
* Refines [Keep daemon requests idle-safe and coalesce renderer publications](keep_daemon_requests_idle_safe_and_coalesce_renderer_publications_112.md).
