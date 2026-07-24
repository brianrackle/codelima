# Animate the TUI wordmark with bounded header-only frames

## Context and Problem Statement

The TUI rendered a static `CodeLima` wordmark as soon as its first full frame appeared. CodeLima needs a distinctive but non-intrusive startup effect in which the wordmark resembles a slot machine: unresolved characters shuffle, then one character settles from left to right every third of a second. How should that effect run without delaying startup, disturbing terminal state, or adding permanent idle work?

## Decision Drivers

* Keep keyboard, mouse, daemon, and terminal startup handling responsive throughout the effect.
* Settle exactly one additional character from left to right at a one-third-second cadence.
* Keep the header layout stable while characters change.
* Make animation frames deterministic and directly testable.
* Stop every animation resource after the brief startup window.

## Considered Options

* Block startup while printing animation frames.
* Redraw the complete TUI from a random background animation goroutine.
* Post elapsed-time animation events to the UI loop and redraw only the fixed-width wordmark.

## Decision Outcome

Chosen option: "Post elapsed-time animation events to the UI loop and redraw only the fixed-width wordmark", because it preserves the TUI's single-owner rendering model, makes delayed events harmless, and limits each frame to the eight cells that actually change.

The animation uses a fixed ASCII glyph wheel with deterministic per-column offsets and rates. Unsettled columns deliberately avoid displaying their final rune. The UI locks one more rune every `time.Second / 3`, completes after eight intervals, discards its animation state, and cancels or naturally stops the short-lived frame ticker. Full redraws that happen for unrelated events render the current wordmark frame, while animation-only events update the header cells and call the normal Vaxis diff renderer.

### Positive Consequences

* Startup remains immediately interactive.
* The wordmark never changes width, so node and configuration header fields do not move.
* No random source or wall-clock sleeps appear in rendering tests.
* Animation work ends after roughly 2.67 seconds and creates no idle ticker.
* Terminal, tree, and overlay surfaces are not rebuilt for animation-only frames.

### Negative Consequences

* Startup creates one short-lived ticker and approximately 18 header frames per second.
* The deterministic shuffle is visually varied but is not random between launches.
* Small terminals that cannot render the normal TUI header do not show the effect.

## Pros and Cons of the Options

### Block startup while printing animation frames

* Good, because the sequence would be straightforward.
* Bad, because input and terminal startup would pause for the entire effect.
* Bad, because rendering would happen outside the established event loop.

### Redraw the complete TUI from a random background animation goroutine

* Good, because every launch could use a different shuffle.
* Bad, because background rendering would race normal UI events.
* Bad, because full terminal snapshots and pane layout would be recomputed for cells that did not change.
* Bad, because random output makes exact progression tests need unnecessary injection seams.

### Post elapsed-time animation events to the UI loop and redraw only the fixed-width wordmark

* Good, because the UI loop remains the sole rendering owner.
* Good, because elapsed time determines the settled prefix even when scheduling is delayed.
* Good, because fixed-width ASCII cells preserve header geometry.
* Bad, because the partial-header renderer is a specialized optimization.
