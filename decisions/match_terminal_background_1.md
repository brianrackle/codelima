# Match the Embedded Terminal Background to the Host Terminal Theme

## Context and Problem Statement

After switching the embedded terminal backend to Ghostty VT, the terminal pane rendered as solid black even when the surrounding TUI inherited a different host terminal background. The problem was caused by painting every Ghostty cell background explicitly, including cells that were only using the terminal's default background. The question was how to restore visual alignment with the host terminal theme without destabilizing the new terminal backend.

## Decision Drivers

* Match the embedded terminal pane to the surrounding host terminal background
* Keep the fix low risk and localized to the Ghostty rendering bridge
* Preserve explicitly colored application backgrounds when possible
* Avoid adding host-terminal-specific startup dependencies immediately

## Considered Options

* Keep painting every Ghostty cell background explicitly
* Query the host terminal background first and configure Ghostty with that color
* Treat cells that match Ghostty's default background as transparent in Vaxis

## Decision Outcome

Chosen option: "Treat cells that match Ghostty's default background as transparent in Vaxis", because it fixes the visible black fill immediately with a small renderer change and works regardless of which host terminal is running `codelima`.

### Positive Consequences

* The embedded terminal pane now inherits the host terminal background for default-background cells.
* The fix is localized to the Ghostty draw path and does not change higher-level TUI behavior.
* Explicit non-default background colors are still rendered normally.

### Negative Consequences

* The current Ghostty cell API does not tell us whether a color was explicit or default-originated.
* If an application explicitly paints the same RGB value as Ghostty's default background, that color is treated as transparent too.
* A future host-background query may still be needed for perfect fidelity on non-black themes.

## Pros and Cons of the Options

### Keep painting every Ghostty cell background explicitly

Continue writing Ghostty's resolved background RGB into every Vaxis cell.

* Good, because it is the simplest mapping from Ghostty cell data to Vaxis style.
* Good, because it avoids ambiguity between default and explicit colors.
* Bad, because it forces a hard terminal background even when the app is using the default background.
* Bad, because it visibly breaks host-theme integration.

### Query the host terminal background first and configure Ghostty with that color

Use the outer host terminal theme as the Ghostty default background at terminal startup.

* Good, because it can more accurately separate explicit colors from default-background cells.
* Good, because it would improve fidelity for non-black host themes.
* Bad, because it adds asynchronous startup coordination and failure handling around host color queries.
* Bad, because theme changes over time would need additional propagation logic.

### Treat cells that match Ghostty's default background as transparent in Vaxis

Compare each Ghostty cell background to Ghostty's default background and leave the Vaxis background unset when they match.

* Good, because it restores host-theme background behavior with a small renderer change.
* Good, because it works across host terminals instead of only one emulator.
* Bad, because explicit backgrounds equal to the default color remain ambiguous.
* Bad, because it is a pragmatic approximation rather than a perfect semantic distinction.

## Links

* Template [ADR_TEMPLATE.md](/Users/brianrackle/Projects/codelima/ADR_TEMPLATE.md)
* Follow-up [TODO.md](/Users/brianrackle/Projects/codelima/TODO.md)
