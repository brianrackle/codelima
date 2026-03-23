# Streamline redundant TUI chrome around the embedded terminal

## Context and Problem Statement

The TUI was showing several layers of repeated status information: a branded app header, layout labels, runtime capability labels, and a terminal-pane header that repeated the selected node, status, workspace, and focus state. That extra chrome reduced usable terminal space and duplicated information already visible in the tree or details pane.

## Decision Drivers

* Maximize usable space for the embedded terminal
* Reduce repeated information across the header, details pane, and terminal pane
* Keep keyboard and mouse affordances visible without crowding the shell output
* Preserve transient status visibility without reintroducing terminal-pane clutter

## Considered Options

* Keep the existing multi-line header and terminal-pane metadata
* Remove only the branded header labels but keep the terminal-pane title and subtitle
* Strip redundant chrome from both the app header and the terminal pane, and move transient status to the footer

## Decision Outcome

Chosen option: "Strip redundant chrome from both the app header and the terminal pane, and move transient status to the footer", because it removes duplicated information while keeping the selection context and interaction hints that still help operators orient themselves.

### Positive Consequences

* The embedded terminal gets more vertical space
* The visible shell output is less cluttered by repeated node and workspace metadata
* Transient statuses still remain visible without taking over the terminal pane

### Negative Consequences

* The terminal pane no longer self-identifies the visible node independent of the tree and top header
* Footer copy now carries more responsibility for communicating focus and transient status
* Future additions to the header need more discipline so the redundant chrome does not grow back

## Pros and Cons of the Options

### Keep the existing multi-line header and terminal-pane metadata

Leave the branded header, layout line, terminal title, terminal subtitle, and focus hint rows in place.

* Good, because the current UI already exists and needs no rendering changes
* Good, because each pane self-describes its state in isolation
* Bad, because it duplicates information that is already visible elsewhere
* Bad, because it reduces the amount of space available for actual terminal output

### Remove only the branded header labels but keep the terminal-pane title and subtitle

Reduce the top-level chrome while leaving the terminal pane mostly unchanged.

* Good, because it is a smaller code change
* Good, because the terminal pane still explicitly names the selected session
* Bad, because the terminal pane would still repeat node slug, status, workspace, and focus copy
* Bad, because it does not recover as much terminal space as a full chrome cleanup

### Strip redundant chrome from both the app header and the terminal pane, and move transient status to the footer

Use a single compact header line for overall context, render the terminal flush inside its bordered pane, and reserve the footer for interaction hints or transient status.

* Good, because it maximizes the visible terminal area
* Good, because it removes repeated metadata while keeping context in the tree and top line
* Bad, because users relying on pane-local labels must now look to the tree or header for that context
* Bad, because footer copy and status handling become more central to usability

## Links

* Refines [decouple_terminal_focus_from_expansion_11.md](decouple_terminal_focus_from_expansion_11.md)
