# Interpreting a CodeLima terminal-freeze capture

## Fault-boundary matrix

| Evidence | Interpretation |
|---|---|
| `daemon status` and `terminal list` succeed, but `terminal read` times out or fails | The daemon control plane is alive; investigate the selected terminal actor and process-wide Ghostty bridge serialization. |
| Status/list and terminal read succeed with a current generation, but TUIs do not redraw | Investigate daemon event delivery, client dirty notifications, and client snapshot scheduling. |
| Status and list fail for a live daemon PID | Investigate the daemon server, Go runtime scheduling, socket state, and process-wide blocking. |
| Only one terminal read fails | Investigate that terminal actor, PTY reader/writer, child process, or its emulator state. |
| Host-local tabs and VM tabs freeze together | Exclude Lima as the common dependency; both kinds terminate in the daemon terminal engine. |
| Direct `limactl shell` works while CodeLima tabs freeze | The VM and Lima transport remain healthy. |
| Separate `CODELIMA_HOME` values with distinct daemon PIDs freeze together | A single daemon is not the shared boundary; investigate host resource pressure, the shared Ghostty library build, or correlated client behavior. |

## Primary stack signatures

### Process-wide Ghostty bridge serialization

`withGhosttyStderrSuppressed` protects process-global `dup2` activity with `ghosttyStderr.mu`. All terminal emulators in one daemon pass many bridge operations through this mutex.

Strong evidence:

- many goroutines wait in `sync.(*Mutex).Lock` beneath `withGhosttyStderrSuppressed`
- one goroutine owns that path inside `ghostty_bridge_terminal_write`, `ghostty_bridge_render_state_update`, viewport extraction, key encoding, or another Ghostty C call
- status/list succeed while terminal reads fail

Possible causes include a hung bridge call or a blocked stderr-capture write. Do not claim which cause occurred unless the owning stack supports it.

### Terminal actor blockage

Each terminal owns `ghosttyTUITerminal.runActor`. Synchronous input, resize, focus, read, and snapshot work waits for that actor.

Strong evidence:

- RPC goroutines wait in `requestSnapshot`, `requestRead`, or `sendSync`
- an actor is stopped inside emulator ingestion, snapshot generation, PTY output, or a Ghostty mutex wait
- the terminal client timed out but the daemon-side actor request remains blocked

Client RPC deadlines do not make an uninterruptible actor operation complete. Avoid repeatedly probing every terminal because each probe can add another blocked server goroutine.

### Event fan-out blockage

`daemon.Server.Broadcast` synchronously visits subscribed clients. Each socket write has a deadline, but concurrent broadcasts can queue on a client's `writeMu`.

Strong evidence:

- terminal event callbacks wait in `Server.Broadcast`
- goroutines accumulate on `clientConn.writeMu`
- a subscriber socket shows repeated blocked or timed-out writes

Distinguish a bounded five-second delay from a persistent freeze.

### Client-only snapshot or redraw failure

Each TUI uses a request connection, an event connection, and per-visible-terminal snapshot scheduling.

Strong evidence:

- terminal reads return changing generations
- daemon samples show actors and broadcasts making progress
- only TUI processes are blocked or spinning
- reconnecting a new TUI displays current terminal state

Sample affected TUI processes separately when the daemon sample is healthy.

## Native sample reading

On macOS, search `daemon-sample.txt` for:

```text
withGhosttyStderrSuppressed
ghostty_bridge_
runActor
requestSnapshot
requestRead
sendSync
Server.Broadcast
writeMu
poll
write
```

Identify the one goroutine or thread making progress versus the many waiters. Record exact function names and counts; do not rely only on CPU percentage.

## Recovery after capture

Prefer the least destructive recovery that still responds:

1. Close and reopen only a TUI when daemon reads prove the daemon and terminal actors are healthy.
2. Consider daemon live update only when actor operations respond; handoff waits on each actor.
3. Stop/restart the daemon only after warning that forced shutdown can terminate live PTYs and respawn fresh shells from session intent.

Keep the evidence bundle local and review it for sensitive paths, commands, logs, or metadata before sharing.
