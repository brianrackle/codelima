# Couple tree visibility to TUI focus

## Context and Problem Statement

The TUI recently split terminal focus from layout expansion so operators could toggle focus and pane width independently. In practice that model added redundant bindings, made the footer harder to understand, and introduced layout states that were not pulling their weight. We need a simpler interaction that still makes terminal focus obvious and preserves per-node shell sessions.

## Decision Drivers

* Reduce keyboard binding complexity
* Make terminal focus visually obvious without extra chrome
* Preserve per-node terminal session reuse across focus changes
* Avoid host-terminal shortcut conflicts

## Considered Options

* Keep focus and layout as separate TUI state
* Couple tree visibility directly to focus
* Remove the tree permanently and use a terminal-first fullscreen TUI

## Decision Outcome

Chosen option: "Couple tree visibility directly to focus", because it removes redundant state and bindings while making focus changes obvious through layout alone.

### Positive Consequences

* `Alt-\`` becomes the single keyboard toggle between tree view and terminal view
* Terminal focus is visually obvious because the tree is hidden whenever the terminal owns focus
* The implementation no longer has to coordinate separate focus and expansion state

### Negative Consequences

* Operators lose the ability to keep the split layout visible while the terminal has keyboard focus
* Documentation and QA flows must be updated to reflect the simpler but more coupled interaction model

## Pros and Cons of the Options

### Keep focus and layout as separate TUI state

Track focus and tree visibility independently.

* Good, because it allows more layout combinations
* Good, because it can support distinct focus and resize bindings
* Bad, because it adds state and user-facing complexity
* Bad, because the extra combinations were not proving useful in practice

### Couple tree visibility directly to focus

Show the split layout when the tree is focused and hide the tree when the terminal is focused.

* Good, because one binding can express the whole transition cleanly
* Good, because focus becomes obvious from the layout without additional indicators
* Bad, because split-view terminal focus is no longer possible
* Bad, because mouse and keyboard flows both need to keep focus and layout synchronized

### Remove the tree permanently and use a terminal-first fullscreen TUI

Always dedicate the full width to the shell and move project selection into overlays.

* Good, because it simplifies the visible layout further
* Good, because it leans into the shell-first workflow
* Bad, because project and node management become less discoverable
* Bad, because it undermines the persistent tree-centric management model of the TUI

## Links

* Supersedes [decouple_terminal_focus_from_expansion_11.md](decouple_terminal_focus_from_expansion_11.md)
* Supersedes [focus_terminal_expands_full_width_7.md](focus_terminal_expands_full_width_7.md)
