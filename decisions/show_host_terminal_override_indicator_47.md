# Show Host Terminal Override Indicator

Status: Refined by [Use TUI Header For Host Terminal Override Indicator](use_tui_header_for_host_terminal_override_indicator_50.md).

## Context and Problem Statement

The TUI can temporarily switch a focused node terminal to the owning project's host-local shell with `Option+Shift+Backtick`. That is useful, but the selected tree context remains the node, so operators need a clear visual signal that input is currently going to the host machine instead of the VM node.

## Decision Drivers

* Make host-versus-VM terminal context visible while preserving node selection.
* Keep the indicator close to the terminal surface without adding another modal or pane.
* Avoid changing the host-terminal toggle state model.

## Considered Options

* Rely on the header project name alone.
* Show a status-line message after switching to the host terminal.
* Render a red top indicator while the host override is active.

## Decision Outcome

Chosen option: "Render a red top indicator while the host override is active", because it stays visible for the whole override session and makes the host-machine context harder to miss.

### Positive Consequences

* Operators can see when fullscreen input is going to the host-local project shell.
* The selected node remains available for node-scoped actions after returning to the tree.
* The indicator disappears automatically when the node terminal is restored.

### Negative Consequences

* Fullscreen terminal focus has one less row while the host override is active.
* The TUI now owns another conditional chrome element.

## Pros and Cons of the Options

### Rely on the header project name alone

Keep the existing header and active-terminal state unchanged.

* Good, because it adds no new chrome.
* Bad, because project names are also present for node terminals, so the host context remains ambiguous.

### Show a status-line message after switching to the host terminal

Write a short status message when the shortcut is pressed.

* Good, because it is simple to implement.
* Bad, because later status updates can overwrite it while the host override is still active.

### Render a red top indicator while the host override is active

Draw a red `HOST TERMINAL` line below the header whenever the active terminal target is the host-local project shell reached from a node.

* Good, because it persists for the duration of the host override.
* Good, because it keeps the existing node selection and return-target behavior.
* Bad, because it consumes a terminal row.

## Links

* Refines [Add Host Terminal Toggle for Node Sessions](add_host_terminal_toggle_for_node_sessions_43.md)
* Refined by [Use TUI Header For Host Terminal Override Indicator](use_tui_header_for_host_terminal_override_indicator_50.md)
