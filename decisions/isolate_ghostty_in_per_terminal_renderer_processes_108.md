# Isolate Ghostty in per-terminal renderer processes

## Context and Problem Statement

Ghostty bridge calls are native cgo calls and process stderr redirection requires a process-global mutex. A native call that never returns can therefore retain the mutex indefinitely and make every in-process terminal appear frozen. Go cancellation cannot preempt a running cgo call.

## Decision Drivers

* One hung native call must affect at most one terminal renderer.
* The affected shell, PTY, and running jobs must survive renderer replacement.
* The daemon status, other terminals, and client connections must remain responsive.
* Normal client reads must never synchronously call Ghostty.
* Output replay must not duplicate renderer-generated terminal responses.
* Existing cooperative daemon live update must keep its PTY continuity.

## Considered Options

* Keep all renderers in the daemon and add timeouts around Go callers.
* Put one complete shell, PTY, and renderer in a process per terminal.
* Keep the pure-Go PTY session actor in the daemon and put one Ghostty renderer in a process per terminal.
* Add separate session and renderer worker processes plus daemon PTY escrow.

## Decision Outcome

Chosen option: "keep the pure-Go PTY session actor in the daemon and put one Ghostty renderer in a process per terminal", because this is the minimum hard boundary that both contains a non-returning cgo call and preserves the shell.

The daemon-side terminal owns the child process, PTY, nonblocking PTY writer, bounded ordered render-event journal, renderer supervisor, response deduplication, and an atomic immutable snapshot cache. It never waits for renderer output while draining the PTY.

Each renderer worker is a separate packaged `codelima-renderer-worker` executable and owns exactly one Ghostty instance. Its control link is length-framed and bounded, has one reader and writer, applies generation fencing, and acknowledges request IDs. Periodic health probes cross the same actor queue as Ghostty operations, so they measure the liveness boundary that matters. The supervisor kills and replaces a renderer whose write, response, health, or operation deadline expires. Restart policy and budget are terminal-local.

Journal output and resize events have stable semantic IDs. Ghostty-generated PTY writes include their source event and ordinal; the session actor deduplicates those IDs across generation races before writing to the PTY. A replacement suppresses terminal responses while replaying old output, avoiding duplicate application input when the original delivery outcome is uncertain. Journal eviction is bounded and produces an explicit partial-recovery state.

The daemon publishes only cached snapshots to clients. A reconnect synchronization frame contains compact session identity; terminals pull their individual cached grids afterward so tab count cannot create one unbounded frame.

PTY escrow and unexpected-daemon worker adoption are deferred. Cooperative live update continues to quiesce the Go session actor, transfer its PTY with SCM_RIGHTS, and rebuild a fresh isolated renderer around the same shell.

### Positive Consequences

* A real C function that never returns is terminated with its renderer process.
* Other terminals and daemon status remain responsive within bounded latency.
* Renderer replacement preserves the shell PID and PTY.
* Native locks and stderr redirection are no longer shared between terminals.
* Normal daemon read RPCs return immutable cache data without entering native code.
* Live update and rollback preserve existing terminal IDs and PTYs.

### Negative Consequences

* Every terminal has an additional renderer process and IPC buffers.
* Renderer reconstruction is partial after the bounded journal evicts old events.
* The user-facing binary still contains the local-TUI Ghostty backend, although the renderer boundary is a separately packaged executable and daemon-owned terminals execute Ghostty only there.
* Unexpected daemon crashes still use persisted respawn rather than adopting surviving workers.

## Pros and Cons of the Options

### In-process timeouts

* Good, because it adds no process overhead.
* Bad, because Go cannot kill a stuck cgo call or release the native mutex.

### Combined shell and renderer worker

* Good, because it contains one terminal completely.
* Bad, because killing a hung renderer also kills the shell and running jobs.

### Go session actor plus renderer worker

* Good, because it isolates native liveness and preserves the shell.
* Good, because it reuses the existing PTY handoff boundary.
* Bad, because renderer/session semantics need fencing, journaling, and deduplication.

### Separate session worker, renderer worker, and PTY escrow

* Good, because it can also contain a future Go session-worker failure.
* Bad, because it introduces descriptor escrow, adoption, split-brain fencing, and another high-volume process boundary without evidence that the pure-Go session actor is the global failure source.

## Links

* Refines [daemon-owned terminal runtimes](daemon_owned_terminal_runtimes_64.md).
* Refines [terminal runtime actor model](terminal_runtime_actor_model_63.md).
* Preserves [authenticated SCM_RIGHTS daemon handoff](authenticated_scm_rights_daemon_handoff_67.md).
