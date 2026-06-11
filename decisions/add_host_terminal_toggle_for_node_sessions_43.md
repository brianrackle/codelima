# Add Host Terminal Toggle for Node Sessions

## Context and Problem Statement

The TUI already preserved both host-local project terminals and guest-backed node terminals, but moving from a focused node terminal to its host project shell required changing tree selection or pane mode. Operators need a direct shortcut to inspect the host workspace shell and return to the node terminal without losing node context. The requested binding is Option+Shift+Backtick.

## Decision Drivers

* Preserve the selected node while temporarily viewing its host project terminal
* Avoid overloading the existing tree-versus-terminal focus shortcut
* Keep project and node terminal sessions reusable
* Make the shortcut deterministic despite shifted graphic-key matching behavior

## Considered Options

* Reuse the existing `Alt-\`` or `F6` focus toggle
* Add a separate Option+Shift+Backtick host-terminal toggle
* Switch tree selection to the parent project when entering the host terminal

## Decision Outcome

Chosen option: "Add a separate Option+Shift+Backtick host-terminal toggle", because it gives the host terminal its own command while preserving the existing fullscreen focus behavior and the selected node context.

### Positive Consequences

* A focused node terminal can switch to the owning project's host-local terminal and back.
* The selected node remains the tree context for later node actions.
* The shortcut is matched with an exact Option+Shift modifier check, so it does not collide with `Alt-\``.

### Negative Consequences

* The TUI state now tracks an active fullscreen terminal target separately from the selected tree entry.
* Terminal emulators must send Option as Meta/Alt for the shortcut to reach CodeLima.

## Pros and Cons of the Options

### Reuse the existing `Alt-\`` or `F6` focus toggle

Make the existing fullscreen shortcut also switch host and node terminals.

* Good, because it avoids another key binding.
* Bad, because it would blur two separate concepts: tree focus and host/node terminal choice.

### Add a separate Option+Shift+Backtick host-terminal toggle

Keep `Alt-\``/`F6` for tree focus, and use Option+Shift+Backtick for host/node terminal switching.

* Good, because each shortcut has one responsibility.
* Good, because node selection and node session state remain intact.
* Bad, because it adds another global TUI shortcut before the broader configurable keybinding system exists.

### Switch tree selection to the parent project when entering the host terminal

Move selection to the project entry so the existing project terminal path is reused directly.

* Good, because it fits the previous selected-entry rendering model.
* Bad, because returning to the node requires extra state anyway.
* Bad, because node-scoped footer actions and context are lost while inspecting the host shell.

## Links

* Template [ADR_TEMPLATE.md](/Users/brianrackle/projects/codelima/ADR_TEMPLATE.md)
