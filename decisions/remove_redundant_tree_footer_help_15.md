# Remove redundant footer help from the TUI tree pane

## Context and Problem Statement

The tree pane was still reserving its bottom rows for instructional help text such as mouse and arrow-key hints. Those instructions duplicated information already available in the footer and reduced the visible room for projects and nodes. We need the tree pane to dedicate that space to tree content instead of repeating controls.

## Decision Drivers

* Reduce redundant chrome in the TUI
* Give more vertical space to project and node entries
* Keep interaction guidance in one place instead of repeating it

## Considered Options

* Keep the tree footer help text as-is
* Remove the tree footer help text and return the rows to tree content
* Replace the footer help text with a shorter one-line hint

## Decision Outcome

Chosen option: "Remove the tree footer help text and return the rows to tree content", because the footer already carries the active interaction hints and the tree benefits more from the extra visible rows.

### Positive Consequences

* More project and node rows are visible without scrolling
* The tree pane is visually cleaner
* Control hints stay centralized in the main footer

### Negative Consequences

* The tree pane no longer provides local inline onboarding for mouse and arrow-key controls
* Operators now rely more on the footer for discoverability

## Pros and Cons of the Options

### Keep the tree footer help text as-is

Leave the bottom tree rows reserved for local control hints.

* Good, because the pane is self-describing
* Good, because new users can discover controls without looking elsewhere
* Bad, because it duplicates footer guidance
* Bad, because it wastes vertical space in the tree

### Remove the tree footer help text and return the rows to tree content

Use the full inner tree pane for the title and tree entries only.

* Good, because it maximizes the visible project and node list
* Good, because it removes redundant visual noise
* Bad, because discoverability shifts to the footer and docs
* Bad, because the pane itself becomes less self-explanatory

### Replace the footer help text with a shorter one-line hint

Keep a smaller local hint area in the tree pane.

* Good, because it preserves some onboarding context
* Good, because it reduces duplication compared with three full lines
* Bad, because it still spends tree space on redundant chrome
* Bad, because it requires another judgment call about which hints deserve space

## Links

* Refines [streamline_tui_chrome_12.md](streamline_tui_chrome_12.md)
