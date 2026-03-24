# Surface contextual TUI footer actions for the selected tree item

## Context and Problem Statement

The TUI footer was still using a generic project-level hint that told operators to "Use action hotkeys in the right pane" instead of showing the concrete keys available for the current tree selection. That forced users to split attention between the footer and the right pane and made the footer less useful than it could be once it became the main place for interaction hints.

## Decision Drivers

* Keep footer hints aligned with the keys the operator can actually press
* Reduce generic or placeholder copy in the TUI chrome
* Preserve the simpler terminal-focused footer that only advertises reachable terminal-focus controls

## Considered Options

* Keep the generic project footer hint and rely on the right pane for action details
* Show a fixed union of all project and node action hotkeys in the footer
* Render the footer from the current selection's available action set

## Decision Outcome

Chosen option: "Render the footer from the current selection's available action set", because it keeps the footer truthful to the active focus and selection state without forcing operators to infer which actions are currently valid.

### Positive Consequences

* Project and node selections now advertise their exact hotkeys directly in the footer
* The footer no longer uses the generic `Use action hotkeys in the right pane` placeholder
* Terminal focus stays uncluttered because it still only shows `Alt-\`` tree focus and quit guidance

### Negative Consequences

* Footer lines can become longer because they now enumerate concrete actions
* Footer wording now depends directly on the action list ordering and labels
* Future action additions must consider footer length and readability

## Pros and Cons of the Options

### Keep the generic project footer hint and rely on the right pane for action details

Leave the footer unchanged and continue using the right pane as the only place that names project-specific actions.

* Good, because it avoids any new footer rendering logic
* Good, because the footer stays shorter
* Bad, because the footer wastes space on a generic instruction instead of actionable keys
* Bad, because users still need to cross-reference the right pane for simple hotkey discovery

### Show a fixed union of all project and node action hotkeys in the footer

Render one static footer string that includes every action the tree might expose.

* Good, because implementation is simple
* Good, because every possible action is always visible somewhere
* Bad, because many advertised actions would be invalid for the current selection
* Bad, because the footer would become noisy and misleading

### Render the footer from the current selection's available action set

Build the footer from the same action list already used for key matching in tree focus.

* Good, because the footer stays consistent with the actual action dispatcher
* Good, because project, node, and empty-tree states each show the right keys
* Bad, because footer rendering is now coupled to action labels and ordering
* Bad, because wide action sets can push the footer closer to truncation on narrow terminals

## Links

* Refines [streamline_tui_chrome_12.md](streamline_tui_chrome_12.md)
* Refines [remove_redundant_tree_footer_help_15.md](remove_redundant_tree_footer_help_15.md)
