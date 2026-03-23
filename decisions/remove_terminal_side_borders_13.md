# Remove terminal pane side borders while keeping top and bottom framing

## Context and Problem Statement

The terminal pane was still using a full box border after the broader TUI chrome cleanup. That left an extra left and right gutter around the embedded shell, which wasted horizontal space and made the terminal feel more padded than the tree pane needed it to.

## Decision Drivers

* Maximize usable horizontal space for terminal content
* Keep enough framing to visually separate the terminal from the rest of the layout
* Avoid reintroducing extra pane-local chrome after the recent TUI simplification
* Preserve mouse hit testing and selection coordinates correctly

## Considered Options

* Keep the existing full border around the terminal pane
* Remove the border entirely
* Keep only top and bottom terminal borders and remove the side borders and horizontal padding

## Decision Outcome

Chosen option: "Keep only top and bottom terminal borders and remove the side borders and horizontal padding", because it preserves pane separation without wasting terminal width on side gutters.

### Positive Consequences

* The shell gets the full usable width of the terminal pane
* Terminal content aligns directly with the pane edge instead of starting one column in
* The pane still has visible framing at the top and bottom

### Negative Consequences

* The terminal pane now uses a different border treatment than the tree pane
* Border drawing is no longer handled by the shared `border.All` helper for this pane
* Rendering tests need to account for the custom top/bottom-only frame

## Pros and Cons of the Options

### Keep the existing full border around the terminal pane

Continue using the same full border widget for the terminal pane that the tree and overlays use.

* Good, because it keeps border rendering uniform across panes
* Good, because it relies on a shared widget instead of custom drawing
* Bad, because it wastes terminal width on left and right gutters
* Bad, because it adds visual padding that is less useful for shell content than for menus or forms

### Remove the border entirely

Let the terminal content run directly against the outer window with no framing.

* Good, because it maximizes both horizontal and vertical terminal space
* Good, because it fully removes pane-local chrome
* Bad, because the terminal becomes visually less separated from adjacent UI
* Bad, because the top and bottom edge lose the framing that still helps orient the layout

### Keep only top and bottom terminal borders and remove the side borders and horizontal padding

Draw custom horizontal rules for the terminal pane and place the terminal body directly below the top rule.

* Good, because it gives the shell more horizontal room without losing all framing
* Good, because it keeps the terminal visually separated from the header and footer lines
* Bad, because it introduces pane-specific border rendering logic
* Bad, because it makes the terminal frame behavior different from the tree and overlay boxes

## Links

* Refines [streamline_tui_chrome_12.md](streamline_tui_chrome_12.md)
