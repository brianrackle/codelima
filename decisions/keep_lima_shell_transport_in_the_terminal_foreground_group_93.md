# Keep the Lima shell transport in the terminal foreground process group

## Context and Problem Statement

The Lima return applied the non-interactive command cancellation helper to the
interactive `sh -lc "limactl shell ..."` child. That helper starts a new process
group, while the outer `codelima shell` process remains the PTY session's
foreground group. No code transfers foreground ownership to the new group, so
the runtime transport cannot read or switch the host terminal to raw mode:
escape sequences and bracketed-paste markers echo literally, and `Ctrl+C`
signals the terminal wrapper instead of the guest job.

## Decision Drivers

* Interactive Lima shells must preserve ordinary terminal input semantics.
* Closing a daemon terminal tab must still terminate the complete launch chain.
* Non-interactive runtime commands must retain bounded whole-group cancellation.
* The solution must work with the existing PTY session ownership on macOS and Linux.

## Considered Options

* Keep a separate process group without a foreground-terminal handoff.
* Move the runtime child to a separate group and implement `tcsetpgrp` handoff and restoration.
* Keep only the interactive runtime transport in the terminal wrapper's foreground process group.

## Decision Outcome

Chosen option: "Keep only the interactive runtime transport in the terminal
wrapper's foreground process group", because the wrapper is already the PTY
session leader and terminal teardown already signals that complete group. This
restores the proven pre-Microsandbox Lima behavior without changing
non-interactive cancellation.

`runInteractiveCommand` uses `exec.CommandContext` with direct terminal streams
and does not apply `configureCommandCancellation`. The `sh`, `limactl`, and SSH
chain therefore inherits the wrapper's foreground group, allowing Lima to put
the terminal in raw mode and pass control bytes to the guest. Context
cancellation still terminates the direct command; closing a managed tab closes
the PTY and uses the terminal session's group-termination ladder from ADR 56.

### Positive Consequences

* Arrow keys, Readline controls, bracketed paste, and guest `Ctrl+C` work normally.
* The interactive and non-interactive process policies are explicit and independently tested.
* Tab close continues to reclaim the runtime transport and its descendants.

### Negative Consequences

* Callers that run an interactive shell outside a managed terminal depend on
  their own terminal/session lifecycle to reap descendants after cancellation.
* Interactive command cancellation cannot reuse the generic new-process-group
  helper without also implementing foreground ownership transfer.

## Pros and Cons of the Options

### Keep a separate group without foreground handoff

* Good, because generic cancellation can signal the child group directly.
* Bad, because the child is a background terminal reader and ordinary input is unusable.

### Implement foreground handoff and restoration

* Good, because the runtime child would own an independently cancellable group.
* Bad, because it adds platform-sensitive terminal ownership, signal, stop, and restoration races around an already nested PTY session.

### Inherit the terminal wrapper's foreground group

* Good, because it matches normal shell/SSH terminal semantics and the previously working Lima implementation.
* Good, because the outer terminal session is already the correct lifetime boundary.
* Bad, because interactive and non-interactive commands intentionally use different cancellation setup.

## Links

* Refines [Return to Lima as the sole runtime with schema v4](return_to_lima_as_the_sole_runtime_92.md).
* Relies on [Kill terminal process groups instead of direct child PIDs](kill_terminal_process_groups_not_pids_56.md) for managed-tab teardown.
