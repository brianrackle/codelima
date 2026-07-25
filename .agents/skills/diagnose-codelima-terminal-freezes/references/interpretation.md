# Interpreting a CodeLima terminal-freeze capture

## Fault-boundary matrix

| Evidence | Interpretation |
|---|---|
| `daemon status` and `terminal list` succeed, but `terminal read` times out or fails | The daemon control plane is alive; investigate the selected Go session actor and immutable-cache publication. A renderer hang alone should not block this read. |
| Status/list and terminal read succeed, but `terminal_runtimes` reports an old progress time, pending renderer operation, restart churn, or degraded state | Investigate only the selected renderer process and its bounded journal/replay state. |
| Status/list and cached terminal read succeed with a ready renderer, but TUIs do not redraw | Investigate daemon event delivery, connection close records, authoritative synchronization, client dirty notifications, and client snapshot scheduling. |
| Status and list fail for a live daemon PID | Investigate the daemon server, Go runtime scheduling, socket state, and process-wide blocking. |
| Only one terminal read fails | Investigate that terminal actor, PTY reader/writer, child process, or its emulator state. |
| Host-local tabs and VM tabs freeze together | Exclude Lima as the common dependency; both kinds terminate in the daemon terminal engine. |
| Direct `limactl shell` works while CodeLima tabs freeze | The VM and Lima transport remain healthy. |
| Separate `CODELIMA_HOME` values with distinct daemon PIDs freeze together | A single daemon is not the shared boundary; investigate host resource pressure, the shared Ghostty library build, or correlated client behavior. |

## Primary stack signatures

### Per-terminal Ghostty renderer blockage

Daemon-owned terminals execute Ghostty only in one renderer worker per
terminal. The process-global stderr mutex exists inside that worker and is no
longer shared by other terminal renderers or the daemon control plane.

Strong evidence:

- `terminal_runtimes.<id>.renderer_current_operation` remains set while
  `renderer_oldest_pending` increases
- the selected `renderer-sample.txt` owns
  `ghostty_bridge_terminal_write`, render-state update, viewport extraction,
  key encoding, or another Ghostty C call
- renderer generation or restart count advances while shell PID and daemon PID
  remain unchanged
- other terminal generations and cached reads continue to advance

Possible causes include a hung bridge call, renderer stderr backpressure, or
worker-link backpressure. Do not claim which cause occurred unless the owning
stack and runtime diagnostics support it.

### Terminal actor blockage

Each daemon terminal owns a pure-Go PTY/session actor. It must continue draining
PTY output into a bounded journal without waiting for renderer IPC.

Strong evidence:

- only one terminal's shell/session diagnostics stop changing while the daemon
  and its renderer supervisor remain responsive
- the PTY reader is blocked on a session-local mutex or writer rather than
  enqueueing bounded render events
- cached reads fail even though a prior snapshot existed

Normal cached reads must not enter renderer IPC. Avoid repeatedly mutating or
probing every terminal; one selected read plus the daemon snapshot is enough.

### Event fan-out blockage

`daemon.Server.Broadcast` performs nonblocking enqueue into one bounded
outbound mailbox per subscribed client. One writer pump owns each socket.

Strong evidence:

- a connection close record reports `queue-full` or `write-timeout`
- outbound depth/oldest age grows for only one logical client
- another client and daemon status remain responsive

Queue overflow deliberately disconnects only that client; its TUI should then
reconnect and install a full state cut.

### Client-only snapshot or redraw failure

Each TUI uses a request connection, an event connection, and per-visible-terminal snapshot scheduling.

Strong evidence:

- terminal reads return changing generations
- daemon samples show actors and broadcasts making progress
- only TUI processes are blocked or spinning
- reconnecting a new TUI displays current terminal state

Sample affected TUI processes separately when the daemon sample is healthy.

## Native sample reading

On macOS, search `renderer-sample.txt` for:

```text
withGhosttyStderrSuppressed
ghostty_bridge_
rendererWorkerServer
rendererSupervisor
poll
write
```

Search `daemon-sample.txt` separately for server, session, queue, and snapshot
cache progress. Ghostty bridge functions in the daemon sample are unexpected
for daemon-owned terminals. Record exact function names and counts; do not rely
only on CPU percentage.

## Recovery after capture

Prefer the least destructive recovery that still responds:

1. Wait for automatic TUI reconnection when the close record identifies only a
   client transport failure.
2. Let the terminal-local supervisor replace a failed renderer when shell PID
   and daemon status remain healthy.
3. Consider daemon live update only when session actors respond; handoff waits
   on each session actor.
4. Stop/restart the daemon only after warning that forced shutdown can
   terminate live PTYs and respawn fresh shells from session intent.

Keep the evidence bundle local and review it for sensitive paths, commands, logs, or metadata before sharing.
