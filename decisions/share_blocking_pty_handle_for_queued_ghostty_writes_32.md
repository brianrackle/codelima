# Share The Blocking PTY Handle For Queued Ghostty Writes

## Context and Problem Statement

The queued Ghostty PTY writer introduced in ADR 30 duplicated the PTY master fd and set the duplicate nonblocking before handing it to the writer goroutine. On real PTYs that flag is shared across the duplicated file description, so the original read-side handle became nonblocking too. The result was that the Ghostty read loop could hit `EAGAIN` before the guest shell printed its first prompt, leaving the embedded terminal pane blank.

## Decision Drivers

* Restore reliable embedded-shell startup and initial prompt rendering
* Keep UI-originated terminal writes off the foreground render path
* Preserve the existing blocking PTY read loop
* Avoid transport changes that depend on PTY semantics that are not actually isolated per duplicated fd

## Considered Options

* Keep the duplicated nonblocking write fd and try to harden the read loop around shared `O_NONBLOCK`
* Keep queued writes but use the original blocking PTY handle as the writer target
* Remove the queued writer and return to fully synchronous PTY writes

## Decision Outcome

Chosen option: "Keep queued writes but use the original blocking PTY handle as the writer target", because it preserves the correctness of the blocking read loop while still moving foreground-originated writes off the UI path.

### Positive Consequences

* The Ghostty read loop continues to behave like a normal blocking PTY reader, so initial shell output reaches the terminal buffer again.
* User key, mouse, and Ghostty-generated response writes still flow through a dedicated writer goroutine instead of blocking the UI path directly.
* The fix is narrowly scoped to the PTY transport setup and does not change higher-level TUI behavior.

### Negative Consequences

* The queued writer no longer owns a truly independent nonblocking write-side file description.
* Explicit `EAGAIN`/poll handling is no longer part of the production PTY path, even though the helper remains covered by unit tests.
* A future attempt to reintroduce a distinct nonblocking write handle will need a PTY strategy that does not share file-status flags with the read side.

## Pros and Cons of the Options

### Keep the duplicated nonblocking write fd and try to harden the read loop around shared `O_NONBLOCK`

Continue using the duplicated fd, but teach the read loop to tolerate nonblocking PTY reads.

* Good, because it preserves the original nonblocking-write design goal.
* Good, because it keeps explicit backpressure handling in production.
* Bad, because the read loop becomes more complex for a regression caused by transport setup rather than by read-side requirements.
* Bad, because the duplicated fd is not actually isolated from the read side on real PTYs.

### Keep queued writes but use the original blocking PTY handle as the writer target

Leave the PTY in blocking mode and let the dedicated writer goroutine own write calls on that shared handle.

* Good, because it restores prompt rendering and shell startup correctness.
* Good, because the UI path still avoids direct PTY writes.
* Good, because it keeps the transport change small and easy to reason about.
* Bad, because it gives up the separate nonblocking write fd.

### Remove the queued writer and return to fully synchronous PTY writes

Discard the queued writer and go back to direct `Write` calls from foreground terminal events.

* Good, because it is the simplest transport model.
* Good, because it avoids writer lifecycle complexity.
* Bad, because it reintroduces UI-path blocking on PTY writes.
* Bad, because it throws away the useful part of ADR 30 rather than correcting the broken fd strategy.

## Links

* Template [ADR_TEMPLATE.md](/Users/brianrackle/Projects/codelima/ADR_TEMPLATE.md)
* Refines [use_nonblocking_queued_pty_writes_for_embedded_ghostty_terminal_30.md](/Users/brianrackle/Projects/codelima/decisions/use_nonblocking_queued_pty_writes_for_embedded_ghostty_terminal_30.md)
