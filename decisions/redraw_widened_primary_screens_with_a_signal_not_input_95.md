# Redraw widened primary screens with a signal, not input

## Context and Problem Statement

ADR 33 repaired duplicated wrapped bash prompts by injecting `Ctrl-L` after a
shell-like primary screen grew wider. Expanding the split pane to terminal
focus exercises that path. If the nested Lima/SSH transport has not entered raw
mode yet, the PTY echoes the control byte literally as `^L`; once the shell is
ready, `Ctrl-L` can clear terminal history the operator expected to keep.

## Decision Drivers

* Width growth must retain clean wrapped prompt rendering.
* Geometry changes must never create application input.
* Focus switching must preserve visible terminal history.
* The behavior must work through the existing local and daemon-owned PTY paths.
* Alternate-screen applications and scrollback must retain the ADR 33 exclusions.

## Considered Options

* Keep injecting `Ctrl-L`, but delay it until the guest shell appears ready.
* Remove the redraw request and accept duplicated prompt fragments.
* Send a supplemental `SIGWINCH` to the PTY child process group after resizing.

## Decision Outcome

Chosen option: "Send a supplemental `SIGWINCH` to the PTY child process group
after resizing", because window-change notification is terminal lifecycle, not
application input, and the shell can redraw against the already-applied
geometry without losing history.

`ghosttyTUITerminal.resizeLocked` still updates the Ghostty emulator first and
then calls `pty.Setsize`. For a width increase on a primary screen that is at
the bottom and not tracking the mouse, it sends one additional `SIGWINCH` to
the negative PTY child PID. `pty.Start` creates that child as a new session
leader, so the negative PID addresses the terminal process group, including the
foreground Lima/SSH transport established by ADR 93. The signal carries no
bytes through the PTY input queue.

The existing interactive-bash test continues to require a clean wrapped prompt
after repeated growth. A canonical-mode `cat` regression now additionally
requires that widening never renders `^L`, directly covering the reported leak.

### Positive Consequences

* Option+Backtick/F6 focus changes do not type form feeds into starting shells.
* Ready shells retain earlier terminal history while still redrawing cleanly.
* Local and daemon terminals share the fix because geometry is owned by the same terminal actor.
* The input queue contains user events only.

### Negative Consequences

* Shell-like primary screens receive two closely spaced window-change notifications for a qualifying growth.
* The solution relies on the PTY child's session/process-group ownership established by `pty.Start`.
* The supplemental signal remains a compatibility workaround around emulator and readline resize timing.

## Pros and Cons of the Options

### Delay `Ctrl-L`

* Good, because it retains ADR 33's known prompt cleanup.
* Bad, because generic terminal output cannot prove that a nested transport is ready for input.
* Bad, because even a correctly timed form feed clears shell history by design.

### Remove the redraw request

* Good, because no synthetic event reaches the child.
* Bad, because the existing wrapped-prompt regression returns.

### Send supplemental `SIGWINCH`

* Good, because it expresses a geometry change through the operating-system terminal contract.
* Good, because it preserves both clean prompts and input purity in automated PTY tests.
* Bad, because applications may perform duplicate resize work.

## Links

* Supersedes the input mechanism in [Request primary-screen redraw after embedded terminal width growth](request_primary_screen_redraw_after_embedded_terminal_width_growth_33.md).
* Relies on [Keep the Lima shell transport in the terminal foreground process group](keep_lima_shell_transport_in_the_terminal_foreground_group_93.md).
* Shares terminal ownership with [Terminal runtime actor model](terminal_runtime_actor_model_63.md).
