# Lease the PTY descriptor to each read pump

## Context and Problem Statement

Terminal handoff and teardown close an `*os.File` that owns the PTY master.
The read pumps previously looked up `t.pty` and called `File.Fd()` on every
would-block retry, so a handoff could close the same Go file wrapper while the
pump was reading its descriptor. The race detector observed this in both the
isolated daemon terminal and the embedded Ghostty handoff paths.

## Decision Drivers

* Handoff must never access an `*os.File` concurrently with its destruction.
* A rolled-back terminal must start exactly one new pump on the returned PTY.
* Boundary output already read before quiescence must remain in renderer replay.
* Read-pump shutdown must stay bounded on every supported Unix host.
* Terminal input accepted before quiescence must drain; new input must not enter
  the outgoing writer after handoff starts.

## Considered Options

* Keep looking up the live terminal fields and add a mutex around every
  `Read`, `Fd`, and `Close` operation.
* Give each read-pump generation an immutable descriptor lease and stop that
  generation before the handoff closes the PTY.
* Duplicate a separate PTY descriptor exclusively for the read pump.

## Decision Outcome

Chosen option: **give each read-pump generation an immutable descriptor
lease**, because it makes generation ownership explicit without adding another
descriptor or holding a terminal mutex across blocking system calls.

At launch, a pump captures its PTY file, numeric descriptor, quit channel, and
completion channel under the terminal mutex. It uses only those captured values
for its lifetime. The numeric descriptor is obtained through `SyscallConn`
rather than `File.Fd()`: Go documents that `Fd()` may switch a pollable file
back to blocking mode, which would defeat the pump's bounded readiness loop.
The pump reads with `unix.Read` so `EAGAIN` remains observable and every idle
wait stays bounded. Rollback installs a new PTY, quit channel, and completion
channel before starting the next generation, so an old pump can never switch
to the replacement generation's resources.

Handoff changes the terminal state to quiescing, which stops isolated-terminal
input admission, drains the already accepted writer queue, closes the captured
quit channel, and waits for the read pump's completion acknowledgement before
closing the writer/PTY. The embedded actor consumes any output the pump had
already read while it waits, preserving the replay boundary. The wait remains
bounded because PTY reads are nonblocking and their readiness poll is bounded;
the immutable lease also keeps a tardy fallback exit from racing `File.Fd()`.

### Positive Consequences

* `File.Fd()` and `File.Close()` no longer race during handoff or teardown.
* Descriptor observation no longer silently clears `O_NONBLOCK`.
* Old and rollback read-pump generations cannot follow mutable terminal fields
  into each other's PTYs or quit channels.
* Output read at the handoff boundary is ingested before replay is captured.
* Input cannot refill the isolated writer after its quiescing drain.

### Negative Consequences

* Read-pump setup duplicates a small amount of immutable generation state.
* Embedded handoff must drain the actor's buffered read channel while waiting
  for the pump acknowledgement.
* A bounded fallback remains for a platform that fails to acknowledge despite
  nonblocking reads; the descriptor lease makes that fallback memory-safe, but
  its final unread kernel bytes are left for the adopted PTY generation.

## Pros and Cons of the Options

### Lock every PTY operation

* Good, because ownership remains represented by the terminal fields.
* Bad, because a mutex held across `Read` or readiness polling can block input,
  teardown, handoff, and unrelated actor work.
* Bad, because rollback still needs generation-specific quit semantics.

### Immutable descriptor lease per pump generation

* Good, because the pump never dereferences a file wrapper being replaced under
  it.
* Good, because quit and completion acknowledgements name one exact generation.
* Good, because it adds no file descriptors and keeps system calls outside the
  terminal mutex.
* Bad, because handoff ordering and buffered boundary draining become explicit
  lifecycle responsibilities.

### Duplicate a read-only pump descriptor

* Good, because the pump could close its own wrapper independently.
* Bad, because PTY master duplicates are not directionally read-only and add
  another descriptor whose hangup and transfer lifecycle must be coordinated.
* Bad, because it does not by itself solve rollback generation or replay-boundary
  ordering.

## Links

* Refines [ADR 30](use_nonblocking_queued_pty_writes_for_embedded_ghostty_terminal_30.md).
* Refines [ADR 63](terminal_runtime_actor_model_63.md).
* Resolves `TODO.md` item 36.
