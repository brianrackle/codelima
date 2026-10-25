# 205. Share header theme selection with startup animation

## Context and Problem Statement

The full manager header honored the saved color mode, but its bounded startup
animation chose colors directly from terminal capabilities. On a truecolor
terminal configured for ANSI, the eight logo cells changed back to RGB until
the next full repaint. A live tmux capture and a failing regression reproduced
the difference; the changing letters themselves are the existing logo effect.

## Considered Options

* Use the same header style resolver for full and animated paints.
* Repaint the entire manager on every logo tick.

## Decision Outcome

Use the same header style resolver, based on `managerTheme`, for both paths.
Saved ANSI/truecolor overrides and automatic capability selection stay
consistent, as does the existing host-shell accent. The bounded animation and
event-loop ownership are unchanged; no protocol or storage change is needed.

### Positive Consequences

* Startup preserves the selected color mode without additional full redraws.
* The regression compares every rendered cell's style before and after both
  an animation tick and completion, and verifies text outside the wordmark
  remains unchanged.

### Negative Consequences

* This checks the existing theme mapping, not each native terminal's palette.

## Pros and Cons of the Options

### Shared style resolver

* Good: removes duplicated style selection and keeps the partial repaint.
* Bad: future header layers must continue to use the same resolver.

### Full repaint on every tick

* Good: automatically recomposes the whole view.
* Bad: needlessly redraws the manager and terminal surface for eight cells.
