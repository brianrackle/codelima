# Render transient TUI views in the right pane

## Context and Problem Statement

The TUI currently opens dialogs, menus, selectors, and long-running operation output as centered overlays on top of the tree and right pane. That wastes the existing right-pane width, hides surrounding context, and makes form-heavy flows feel cramped even though the split layout already provides a large content area beside the tree.

## Decision Drivers

* use the existing right-pane space instead of opening a smaller centered popup
* keep the project and node tree visible during transient interactions
* preserve the current keyboard behavior for submit, cancel, and selection flows
* avoid adding a second interaction model just to change the rendering layout

## Considered Options

* keep centered modal overlays and only restyle them
* render transient TUI views directly in the right pane
* move all transient views to a separate fullscreen mode

## Decision Outcome

Chosen option: "render transient TUI views directly in the right pane", because it reuses the space already reserved for contextual content, keeps the tree available for orientation, and lets the existing dialog, menu, selector, and operation input flows continue to work without a separate modal rendering stack.

### Positive Consequences

* forms, selectors, menus, and operation output have more horizontal and vertical space
* operators keep the tree visible while completing transient workflows
* the rendering path is simpler because the active right-pane view replaces the normal details or terminal content instead of layering on top of it

### Negative Consequences

* a transient right-pane view now forces the split layout even if terminal focus had been active previously
* the footer and right-pane help text must stay coordinated with the currently active transient view
* older overlay-specific rendering helpers and screenshots become obsolete

## Pros and Cons of the Options

### keep centered modal overlays and only restyle them

Continue to draw transient views over the full TUI, but tweak spacing or borders.

* Good, because it keeps the existing render structure intact
* Good, because it avoids changing how focus interacts with layout
* Bad, because it still wastes the right-pane space that already exists
* Bad, because it keeps obscuring surrounding context for no real benefit

### render transient TUI views directly in the right pane

Use the right pane as the single surface for details, terminal content, dialogs, menus, selectors, and streamed operation output.

* Good, because it makes better use of the widest contextual area in the TUI
* Good, because it keeps tree context visible while a transient workflow is open
* Bad, because it couples transient-view rendering to the split layout
* Bad, because hidden terminal sessions must be blurred while a transient pane owns the right side

### move all transient views to a separate fullscreen mode

Temporarily replace both panes with the active dialog, selector, or progress screen.

* Good, because it offers the maximum possible space for a form or log
* Good, because it avoids any split-layout constraints for tall content
* Bad, because it removes the tree context the operator was using
* Bad, because it is a larger UX change than this rendering problem requires

## Links

* Refines [clear_modal_overlay_regions_8.md](clear_modal_overlay_regions_8.md)
* Refines [couple_tree_visibility_to_tui_focus_14.md](couple_tree_visibility_to_tui_focus_14.md)
