# Animate visible progress cells in the frontend event loop

## Context and Problem Statement

The Kit specifies a ten-frame braille spinner at 10 fps. Loading and active
operation indicators were static. Raising the telemetry rate or repeatedly
rebuilding terminal surfaces would couple presentation to unrelated work.

## Considered Options

* Repaint the whole manager from a periodic worker.
* Register visible spinner cells while drawing and advance them in the UI loop.

## Decision Outcome

Use one 100 ms timer owned by the existing UI event loop. A full draw replaces
the registered cells; the foremost overlay replaces underlying registrations.
Ticks advance the specified glyph sequence from monotonic elapsed time and
repaint only those cells. Clipped or hidden cells do not keep a timer alive.
The timer stops on idle, an undersized display, and event-loop shutdown.

Node-status glyphs and historical request icons retain their static meaning.
Active Activity rows/badges, lifecycle/bootstrap steps, host checks, daemon work,
create submission and local-operation summaries use the spinner. Polling and
the existing startup-logo effect retain their independent timing. The event
loop cannot paint while suspended for a raw shell or an external editor.

### Positive Consequences

* No animation goroutine, RPC or full-screen rebuild is needed per frame.
* Selection, cursor, terminal images and surrounding text remain intact.
* Late frames skip ahead; an older timestamp cannot rewind the animation.

### Negative Consequences

* Rendering code explicitly registers animated cells and their clip windows.
* Covered background indicators remain static while an overlay owns the screen.

## Pros and Cons of the Options

A periodic whole-screen worker would be short but adds idle work and risks
painting during terminal suspension. Cell registration fits the existing
single-owner UI loop and preserves graphics while adding a small presentation
state with deterministic tests.
