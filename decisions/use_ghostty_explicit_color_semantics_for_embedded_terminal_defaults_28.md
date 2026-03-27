# Use Ghostty Explicit Color Semantics for Embedded Terminal Defaults

## Context and Problem Statement

The embedded terminal initially approximated Ghostty default-background handling by comparing each resolved cell background RGB against Ghostty's current default background RGB and leaving matching cells transparent in Vaxis. That fixed the obvious black-pane regression, but it could not distinguish "default background" from "explicit background equal to the default color." The same issue existed for default foreground semantics too. The question was how to make default-color rendering correct without waiting for a separate Ghostty API to configure host terminal colors directly.

## Decision Drivers

* Preserve explicit terminal colors even when they equal the terminal default RGB
* Let host-terminal defaults flow through naturally for default-colored cells
* Keep the fix localized to the Ghostty render bridge and renderer
* Avoid reintroducing RGB-equality heuristics for terminal defaults

## Considered Options

* Keep comparing resolved RGB values against Ghostty's default colors
* Switch to Ghostty's explicit-versus-default cell color semantics in the render bridge
* Delay the fix until Ghostty exposes host-default color configuration directly

## Decision Outcome

Chosen option: "Switch to Ghostty's explicit-versus-default cell color semantics in the render bridge", because the newer Ghostty render-state API already provides the distinction the renderer needs and removes the ambiguous RGB-equality heuristic immediately.

### Positive Consequences

* Default foreground and background cells now inherit the outer host terminal colors through Vaxis `ColorDefault`.
* Explicit application colors are preserved even when they equal Ghostty's current default RGB values.
* The render path no longer needs to carry a parallel "default background RGB" heuristic in Go.
* The fix works with the current runtime-loaded Ghostty integration.

### Negative Consequences

* Guest applications that query terminal default colors can still observe Ghostty's own default-color model rather than the outer host terminal theme.
* The bridge now carries a little more per-cell metadata to preserve explicit/default color semantics.
* Manual visual QA is still needed for hyperlink, selection, and background behavior in a real terminal session.

## Pros and Cons of the Options

### Keep comparing resolved RGB values against Ghostty's default colors

Continue treating any cell whose resolved background equals Ghostty's default background RGB as transparent.

* Good, because it is simple and already shipped.
* Good, because it restored host-theme background blending quickly.
* Bad, because it cannot distinguish explicit colors from default-origin colors when the RGB values match.
* Bad, because it keeps the renderer dependent on a heuristic instead of Ghostty's actual cell semantics.

### Switch to Ghostty's explicit-versus-default cell color semantics in the render bridge

Carry explicit/default color flags alongside each resolved cell and only paint fg/bg colors in Vaxis when Ghostty reports them as explicit.

* Good, because it uses Ghostty's actual render semantics instead of an RGB guess.
* Good, because it fixes the explicit-background-equal-default case directly.
* Good, because it lets both foreground and background defaults inherit from the host terminal naturally.
* Bad, because it widens the bridge cell metadata slightly.
* Bad, because it still does not make Ghostty's own internal default-color queries match the host terminal theme.

### Delay the fix until Ghostty exposes host-default color configuration directly

Leave the RGB-equality workaround in place until Ghostty offers a direct way to configure default colors from the host terminal.

* Good, because it would avoid changing the cell metadata shape today.
* Good, because a future upstream API could align both rendering and guest-visible color queries in one step.
* Bad, because it leaves the explicit-color ambiguity unfixed in the current product.
* Bad, because it blocks a correctness improvement on an upstream capability that may not be available yet.

## Links

* Template [ADR_TEMPLATE.md](/Users/brianrackle/Projects/codelima/ADR_TEMPLATE.md)
* Related [match_terminal_background_1.md](/Users/brianrackle/Projects/codelima/decisions/match_terminal_background_1.md)
