# Render TUI Nodes as Multiline Property Blocks

## Context and Problem Statement

The flat TUI node list rendered the node slug, configuration, working directory, and live status on one terminal row. The tree pane is intentionally narrow, so realistic names and paths truncated the status or made the relationship among values difficult to scan.

## Decision Drivers

* Configuration, working directory, and status must remain visible in the narrow tree pane.
* Property values must be visibly associated with their node.
* Keyboard scrolling, selection highlighting, and mouse selection must continue to operate on nodes rather than individual display lines.
* Path-scoped views must retain relative working directories.

## Considered Options

* Keep one row and truncate or horizontally scroll it.
* Show properties only for the selected node.
* Render every node as a fixed-height multiline property block.

## Decision Outcome

Chosen option: "render every node as a fixed-height multiline property block", because it keeps all navigation context visible and makes ownership clear without adding another interaction mode.

Each node occupies four terminal rows: a bullet-prefixed slug followed by indented `Config`, `CWD`, and `Status` lines. The whole block uses the node's selection style. The viewport converts available terminal rows into node capacity using the shared block height, and mouse hit-testing converts a property-line row back to its owning node through that same height. Any unused remainder below the last complete block is non-selectable.

### Positive Consequences

* Long property values no longer compete with one another on a single row.
* Indentation makes each property's node ownership explicit.
* Keyboard and mouse navigation preserve one selectable identity per node.
* Operation statuses continue to replace the live status value in place.

### Negative Consequences

* The tree displays fewer nodes at once.
* Rendering and mouse hit-testing must share the fixed block-height invariant.

## Pros and Cons of the Options

### Keep one row and truncate or horizontally scroll it

* Good, because it preserves maximum node density.
* Bad, because truncation hides the information the tree is intended to expose.
* Bad, because horizontal scrolling adds state and makes nodes harder to compare.

### Show properties only for the selected node

* Good, because unselected nodes remain dense.
* Bad, because operators cannot compare configuration, directory, or status across visible nodes.
* Bad, because selection changes the tree's vertical layout.

### Render every node as a fixed-height multiline property block

* Good, because every visible node has the same readable structure.
* Good, because fixed height supports deterministic scrolling and hit-testing.
* Bad, because each node consumes four rows.

## Links

* Refines [Streamline TUI Chrome](streamline_tui_chrome_12.md).
* Extends [Periodically Refresh TUI Project Tree](periodically_refresh_tui_project_tree_44.md).
