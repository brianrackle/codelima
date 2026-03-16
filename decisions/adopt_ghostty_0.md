# Adopt Ghostty VT for the Embedded Terminal Backend

## Context and Problem Statement

The original embedded terminal used the Vaxis terminal widget directly for PTY handling, VT parsing, state management, and drawing. That path was simpler, but real workloads such as `apt` and `dpkg` progress rendering exposed correctness and responsiveness problems severe enough to freeze the app. The question was how to improve terminal correctness without rewriting the entire TUI.

## Decision Drivers

* Terminal correctness under real interactive workloads
* Preserve the existing Vaxis-based TUI shell, tree, dialogs, and layout
* Keep hyperlink, selection, and scrollback behavior under application control
* Limit the scope of the migration to the terminal pane

## Considered Options

* Keep the Vaxis terminal widget as the only backend
* Use Ghostty VT as the terminal backend while keeping Vaxis for the outer TUI
* Replace the full TUI stack instead of only the terminal backend

## Decision Outcome

Chosen option: "Use Ghostty VT as the terminal backend while keeping Vaxis for the outer TUI", because it raises terminal-emulation quality where the failures were happening while preserving the rest of the TUI implementation and interaction model.

### Positive Consequences

* The terminal pane uses a more battle-tested VT implementation for PTY output parsing and screen state.
* The existing Vaxis tree, overlay, dialog, and event-loop code remains reusable.
* The terminal backend can evolve independently from the rest of the TUI surface.

### Negative Consequences

* The project now depends on Cgo and the Ghostty VT build/install path.
* Integration complexity increased because Vaxis and Ghostty now meet at a custom rendering boundary.
* Backend-specific quirks such as parser warnings and background rendering must be handled explicitly in the bridge layer.

## Pros and Cons of the Options

### Keep the Vaxis terminal widget as the only backend

Continue using the existing Go-native terminal widget for emulation and rendering.

* Good, because it keeps the stack simpler and avoids a new native dependency.
* Good, because it minimizes build and packaging complexity.
* Bad, because the failures that motivated the change were already happening in this path.
* Bad, because improving correctness there would likely require substantial terminal-emulation work in-house.

### Use Ghostty VT as the terminal backend while keeping Vaxis for the outer TUI

Use Ghostty for terminal parsing/state and Vaxis for the shell-first TUI chrome.

* Good, because it directly targets the failure domain without replacing the whole UI stack.
* Good, because it preserves existing Vaxis-based TUI behaviors and layouts.
* Good, because it allows a backend abstraction for future terminal work.
* Bad, because it introduces native-library integration and maintenance cost.
* Bad, because the bridge layer must translate state, input, links, and rendering semantics.

### Replace the full TUI stack instead of only the terminal backend

Move away from Vaxis entirely and rebuild the full interface on another stack.

* Good, because it could provide a more uniform rendering model if done completely.
* Good, because it removes the split between UI framework and terminal backend.
* Bad, because it is much larger in scope than the problem required.
* Bad, because it would discard stable, working TUI code unrelated to terminal emulation.

## Links

* Template [ADR_TEMPLATE.md](/Users/brianrackle/Projects/codelima/ADR_TEMPLATE.md)
