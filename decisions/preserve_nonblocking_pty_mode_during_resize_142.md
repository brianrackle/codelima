# Preserve nonblocking PTY mode during resize

## Context and Problem Statement

Live daemon updates could hang after resizing a host terminal. The existing
resize helper called `os.File.Fd`, which restored blocking mode on the pollable
PTY. The raw read loop then blocked instead of observing its quit channel;
closing the original descriptor did not reliably interrupt that read while
handoff duplicates kept the PTY alive. Previous handoff tests never resized
the live PTY, so they did not reproduce the fault.

## Decision Drivers

* Keep PTY read/poll cancellation working after every resize.
* Preserve live shell identity, pixel geometry and output during handoff.
* Avoid descriptor-reuse races when resizing overlaps closure.

## Considered Options

* Restore `O_NONBLOCK` after the existing resize helper returns.
* Add a timeout around handoff's blocked reader.
* Perform the resize ioctl through `SyscallConn.Control`.

## Decision Outcome

Chosen option: "Perform the resize ioctl through `SyscallConn.Control`",
because it preserves descriptor mode throughout the operation and keeps the
descriptor alive until the ioctl completes. Shared `terminalio.Resize` uses
`unix.IoctlSetWinsize` inside `Control`. Both daemon-owned PTYs and the direct
Ghostty terminal use this helper. PTY creation remains with `creack/pty`;
its startup size operation occurs before the nonblocking reader starts.

Tests verify descriptor flags, full cell/pixel geometry, closed-descriptor
errors, direct actor resizing, and idle-shell handoff/rollback after multiple
resizes. The built CLI integration test now resizes a terminal before filling
its journal, waits for the emitted completion marker, and verifies handoff
preserves shell PID, geometry and history.

### Positive Consequences

* The read loop can observe shutdown after resizing without new input.
* Handoff keeps the existing shell and retained history.
* Both terminal owners use the same descriptor-safe operation.

### Negative Consequences

* Future PTY ioctl helpers must preserve this invariant as well; helpers that
  call `File.Fd` are unsuitable once the read loop has started.

## Pros and Cons of the Options

### Restore nonblocking mode afterward

* Good, because the existing helper could remain.
* Bad, because a concurrent read can enter the kernel during the blocking gap.

### Time out the blocked reader

* Good, because it could bound the visible wait.
* Bad, because a live blocked reader still owns the PTY; starting another
  reader or releasing the shell before it exits can lose or duplicate output.

### Ioctl through Control

* Good, because it never changes the descriptor's mode.
* Good, because descriptor lifetime is protected during the ioctl.
* Bad, because the project owns a small Unix-specific helper.

## Links

* Complements [Outer resize before TUI layout](apply_outer_resize_before_tui_layout_140.md).
* Refines [Renderer-isolated terminals](isolate_ghostty_in_per_terminal_renderer_processes_108.md).
