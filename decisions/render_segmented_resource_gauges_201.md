# Render resource gauges with separate fill and track colors

## Context and Problem Statement

The Kit reference defines a dark empty track and usage-colored filled cells.
Flattening a gauge into surrounding text applied one foreground to both.

## Considered Options

* Recolor block glyphs in all text rendering.
* Keep an explicit gauge value, width, prefix and suffix until drawing.

## Decision Outcome

Use `tuiGaugeLine` at resource call sites (fleet, Overview, Metrics, host capacity
and create fit). Its segments preserve the caller's background and attributes;
only gauge foregrounds change. Filled cells use the existing 70/90 percent
thresholds, empty cells use `#2b3a49` (ANSI gray), and unavailable gauges remain
dim dashes. Fractional cell calculation and clipping remain shared.

### Positive Consequences

* Selected rows, modal backgrounds and ANSI reverse video remain intact.
* Literal block characters in paths and labels keep their authored style.
* Resource values, widths and allocation semantics remain unchanged.

### Negative Consequences

* Callers retain a small structured line instead of a flat string until draw.

## Pros and Cons of the Options

Global glyph recoloring would be shorter but would change arbitrary user text.
Explicit gauge structure identifies only generated metric cells and is testable
at each affected surface.
