# Latch TUI daemon disconnects before focus takeover

Status: Accepted

## Context and Problem Statement

An open TUI correctly reported that a stopped, updated, or permanently
disconnected daemon required the window to be reopened. The next host-window
focus event nevertheless sent `input.takeover` through the dead request socket
and replaced that actionable message with `write: broken pipe`; late terminal
worker failures could overwrite it again. How should the TUI behave after its
daemon session has ended but before automatic reconnection exists?

## Decision Drivers

* A known-dead connection must not receive recurring focus-driven writes.
* The footer must retain one actionable recovery instruction.
* Routine focus takeover on a healthy idle connection must remain unchanged.
* A failed RPC must not be replayed because its completion may be ambiguous.
* Automatic live-update reconnection remains separate lifecycle work.

## Considered Options

* Keep attempting takeover and show each transport error.
* Redial and retry all failed daemon requests automatically.
* Latch daemon disconnection in the TUI event loop and require reopening.

## Decision Outcome

Chosen option: "latch daemon disconnection in the TUI event loop and require
reopening", because it preserves the existing safe recovery boundary while
removing repeated writes and misleading low-level errors.

Daemon shutdown, update commit, and permanent event-stream failure cross into
the UI loop as a dedicated daemon-disconnected event. The app latches that
state, retains the reconnect guidance, stops sending ownership takeovers on
focus, and logs terminal worker errors that arrive after the latch instead of
replacing the guidance. A takeover failure that arrives before the event-stream
notification also sets the latch and reports the same reopen instruction.

### Positive Consequences

* Focusing a TUI after daemon update no longer displays `broken pipe`.
* Repeated focus events perform no work against a known-dead connection.
* Late asynchronous errors cannot hide the required recovery action.
* Healthy multi-window ownership handoff and idle connections are unaffected.

### Negative Consequences

* The TUI still must be quit and reopened after daemon replacement or an
  unexpected permanent disconnect.
* A transient takeover failure makes that TUI session conservatively
  unavailable instead of trying to reuse a potentially desynchronized stream.

## Pros and Cons of the Options

### Keep attempting takeover and show each transport error

* Good, because it requires no additional lifecycle state.
* Bad, because a dead Unix socket cannot recover through another write.
* Bad, because raw transport errors overwrite the user action that can recover.

### Redial and retry all failed daemon requests automatically

* Good, because a successful reconnect could avoid reopening the TUI.
* Bad, because replaying input or other mutations after an ambiguous failure
  can apply an operation twice.
* Bad, because request, event, snapshot, terminal identity, and ownership state
  require coordinated replacement rather than a focus-handler retry.

### Latch daemon disconnection in the TUI event loop and require reopening

* Good, because the UI loop remains the single owner of visible lifecycle
  state.
* Good, because it eliminates recurring dead-socket work without replay.
* Bad, because reopening remains a manual recovery step.

## Links

* Refines [Replace idle terminal polling and broken event-stream spins](replace_idle_terminal_polling_and_broken_stream_spins_90.md).
* Preserves [Keep authenticated daemon connections alive while idle](persistent_authenticated_daemon_connections_75.md).
* Leaves automatic reconnection in TODO item 32.
