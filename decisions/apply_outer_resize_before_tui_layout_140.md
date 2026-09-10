# Apply outer resize events before TUI layout

## Context and Problem Statement

The Vaxis v0.17.1 dependency adopted with expanded libghostty-vt support requires
applications to call `Vaxis.Resize` when receiving a resize event. CodeLima
updated its embedded terminal dimensions without resizing the outer drawing
buffers. The following draw used the old window dimensions and reasserted
them to the daemon, producing clipped text and stale screen layout. Overlay
routing also consumed resize events before the global handler could see them.

## Decision Drivers

* Keep the drawing surface, pane layout and shell geometry consistent.
* Handle resizing during dialogs and before terminal sessions exist.
* Preserve asynchronous daemon resizing and terminal-owned text reflow.

## Considered Options

* Apply resize events globally before layout and drawing.
* Patch Vaxis to resize buffers automatically.
* Force additional redraws or terminal resets.

## Decision Outcome

Chosen option: "Apply resize events globally before layout and drawing",
because it follows the dependency's public API and fixes geometry at its
source. The global event handler processes resize before overlay input routing.
The resize handler updates Vaxis before computing or forwarding pane sizes,
including when no session exists. Existing invalid-dimension fallback remains.

Regression tests drive the actual event/draw path with Vaxis buffers and a
recording terminal, covering growth, shrinkage, minimum-size recovery,
pixel-only changes, tree focus, terminal focus and overlays. They assert the
dimensions after drawing, when the old implementation reverted the resize.

### Positive Consequences

* The outer surface and daemon receive dimensions from the same resize.
* Dialogs and minimum-size notices recover as the window changes.
* Native terminal reflow and shell lifetime remain under their existing owners.

### Negative Consequences

* The frontend must continue honoring Vaxis's explicit resize contract during
  future dependency upgrades.

## Pros and Cons of the Options

### Apply resize globally

* Good, because one event establishes the geometry before any drawing.
* Good, because public Vaxis behavior is covered in application regressions.
* Bad, because resize must remain ahead of future modal input handlers.

### Patch Vaxis

* Good, because it could restore implicit behavior.
* Bad, because it widens the dependency fork and bypasses its public contract.

### Additional redraws or terminal resets

* Good, because they can refresh stale cells.
* Bad, because they retain the incorrect geometry and may lose terminal state.

## Links

* Refines [Static libghostty-vt adoption](adopt_static_libghostty_vt_with_bounded_worker_contracts_132.md).
* Verification: [Manual QA](../QA.md), `make test-tui-resize`.
