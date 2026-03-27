# Use Ghostty Viewport State for Embedded Terminal Scrollback

## Context and Problem Statement

The embedded terminal already delegated VT parsing, render state, keyboard encoding, and mouse encoding to Ghostty, but viewport position and visible scrollback were still tracked by a local Go `scrollOffset`. That duplicated state Ghostty already owns through its viewport and scrollbar APIs, and it left CodeLima further away from the Ghostling pattern of letting Ghostty own terminal semantics. The question was how to move scrollback ownership into Ghostty without changing the runtime-loaded integration model.

## Decision Drivers

* Remove duplicated viewport state from Go
* Keep embedded-terminal wheel scrolling aligned with Ghostty's own viewport behavior
* Preserve the runtime-loaded `libghostty-vt` packaging model
* Keep CodeLima-specific focus, copy, and gesture routing above the terminal boundary

## Considered Options

* Keep the existing Go-owned `scrollOffset` model
* Switch the embedded terminal to Ghostty-owned viewport scrolling and scrollbar state
* Replace runtime loading with direct linking to a larger Ghostty C API

## Decision Outcome

Chosen option: "Switch the embedded terminal to Ghostty-owned viewport scrolling and scrollbar state", because it adopts the same terminal-ownership direction Ghostling uses while keeping packaging stable and removing duplicated scroll math from Go.

### Positive Consequences

* Wheel scrolling now moves Ghostty's viewport instead of a local Go offset.
* Rendering reads Ghostty's current viewport directly instead of stitching together local scrollback slices.
* Cursor visibility now follows Ghostty-owned viewport state rather than a parallel Go scroll position.
* The bridge keeps runtime loading intact while enabling a bounded Ghostty scrollback history for the embedded terminal.

### Negative Consequences

* The bridge now chooses a concrete embedded-terminal scrollback limit instead of relying on Ghostty's zero-history default.
* Hyperlink, selection, and render regressions around scrolled history still need manual QA coverage in a real terminal session.
* The Go layer still owns response polling and PTY writes for now, so viewport ownership is not yet the full end-state Ghostling boundary.

## Pros and Cons of the Options

### Keep the existing Go-owned `scrollOffset` model

Continue tracking scrollback position in Go and stitch the visible rows together from local math plus Ghostty history reads.

* Good, because it avoids widening the bridge further.
* Good, because it leaves the existing rendering path mostly unchanged.
* Bad, because it duplicates viewport state Ghostty already owns.
* Bad, because it keeps wheel scrolling, cursor visibility, and visible-history calculations more fragile than the other Ghostty-owned terminal paths.

### Switch the embedded terminal to Ghostty-owned viewport scrolling and scrollbar state

Use Ghostty's scrollbar state and viewport-scroll APIs as the source of truth for embedded-terminal scrollback position and visible history.

* Good, because it follows the same ownership direction Ghostling uses for viewport state.
* Good, because it removes duplicated scrollback math from Go.
* Good, because it lets rendering consume Ghostty's current viewport directly.
* Bad, because it requires enabling and sizing Ghostty scrollback explicitly in the bridge.
* Bad, because it still leaves terminal callbacks and PTY writes for later migration.

### Replace runtime loading with direct linking to a larger Ghostty C API

Drop `dlopen` and link directly against Ghostty's C API.

* Good, because it would make the full compile-time Ghostty API surface directly available.
* Good, because it simplifies some symbol-loading code.
* Bad, because it changes the current packaging and runtime-library discovery model.
* Bad, because it is broader in scope than the scrollback-ownership change required here.

## Links

* Template [ADR_TEMPLATE.md](/Users/brianrackle/Projects/codelima/ADR_TEMPLATE.md)
* Related [use_ghostty_mouse_encoder_for_embedded_terminal_input_26.md](/Users/brianrackle/Projects/codelima/decisions/use_ghostty_mouse_encoder_for_embedded_terminal_input_26.md)
