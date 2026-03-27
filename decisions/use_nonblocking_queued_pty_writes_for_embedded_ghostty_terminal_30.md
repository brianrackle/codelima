# Use Nonblocking Queued PTY Writes for Embedded Ghostty Terminal

## Context and Problem Statement

The embedded Ghostty terminal had already moved most terminal semantics into Ghostty, but PTY writes still happened synchronously through direct `io.WriteString` and `pty.Write` calls on the UI-side code path. Ghostling handles PTY transport more explicitly, including partial writes and temporary backpressure, so the question here was how to make CodeLima's embedded terminal transport robust without rewriting the rest of the terminal lifecycle.

## Decision Drivers

* Keep UI-side terminal input from blocking on PTY backpressure
* Handle partial writes and `EAGAIN`-style temporary write failures explicitly
* Preserve the existing blocking read loop for PTY output ingestion
* Keep the runtime-loaded Ghostty integration model intact

## Considered Options

* Keep the existing synchronous PTY write calls on the UI path
* Add a dedicated queued PTY writer with a duplicated nonblocking write fd
* Flip the existing PTY fd fully nonblocking for both reads and writes

## Decision Outcome

Chosen option: "Add a dedicated queued PTY writer with a duplicated nonblocking write fd", because it isolates backpressure handling to the write side while preserving the simpler blocking read loop and keeping the transport change narrowly scoped.

### Positive Consequences

* User input and Ghostty-generated PTY responses now enqueue quickly instead of writing directly on the UI path.
* Partial writes and temporary backpressure are retried explicitly in one place.
* The PTY read loop can stay blocking and simple because reads still use the original PTY fd.
* Terminal teardown now closes both the PTY read fd and the dedicated write loop consistently.

### Negative Consequences

* The embedded terminal now owns an additional goroutine, queue, and duplicated PTY file descriptor.
* Write-side failures are now asynchronous relative to the originating UI event.
* The transport path is more stateful, so shutdown ordering matters more than it did with direct writes.

## Pros and Cons of the Options

### Keep the existing synchronous PTY write calls on the UI path

Continue calling `io.WriteString` or `Write` directly from the terminal update path.

* Good, because it keeps the code very small.
* Good, because it avoids any extra goroutine or queue state.
* Bad, because partial writes and temporary blocking are not handled robustly.
* Bad, because UI-driven terminal events can stall behind PTY backpressure.

### Add a dedicated queued PTY writer with a duplicated nonblocking write fd

Duplicate the PTY fd for writes, set the duplicated fd nonblocking, and let a dedicated writer loop own retries and queue draining.

* Good, because it isolates backpressure handling to the write side.
* Good, because it preserves the existing blocking PTY read loop.
* Good, because both terminal input and Ghostty-generated responses can share the same transport path.
* Bad, because it adds queue and lifecycle complexity.
* Bad, because write failures become asynchronous from the caller's perspective.

### Flip the existing PTY fd fully nonblocking for both reads and writes

Set the single PTY fd nonblocking and handle readiness for both read and write operations in CodeLima.

* Good, because it avoids duplicating the PTY fd.
* Good, because one readiness model could cover all PTY traffic.
* Bad, because it would force the output read loop to handle `EAGAIN`, retries, and readiness too.
* Bad, because it is broader and riskier than the write-side fix needed here.

## Links

* Template [ADR_TEMPLATE.md](/Users/brianrackle/Projects/codelima/ADR_TEMPLATE.md)
* Related [use_ghostty_terminal_callbacks_for_embedded_terminal_queries_29.md](/Users/brianrackle/Projects/codelima/decisions/use_ghostty_terminal_callbacks_for_embedded_terminal_queries_29.md)
