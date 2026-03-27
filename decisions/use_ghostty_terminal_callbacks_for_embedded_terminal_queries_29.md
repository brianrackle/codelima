# Use Ghostty Terminal Callbacks for Embedded Terminal Queries

## Context and Problem Statement

The embedded terminal already delegated VT parsing, key encoding, mouse encoding, viewport state, and render-time color semantics to Ghostty, but several terminal-originated queries were still answered by local Go code or local response plumbing. Ghostling registers Ghostty terminal callbacks for device attributes, color scheme, XTWINOPS size, and XTVERSION, so the question here was how to move those query semantics into Ghostty without changing CodeLima's runtime-loaded bridge model or prematurely coupling this branch to PTY transport changes.

## Decision Drivers

* Keep terminal query semantics aligned with Ghostty's own callback model
* Reduce local handcrafted response logic in Go
* Preserve the runtime-loaded `libghostty-vt` packaging and compatibility bridge
* Keep PTY transport changes scoped to a later dedicated branch

## Considered Options

* Keep answering terminal queries through local Go response handling
* Register Ghostty terminal callbacks for supported queries while retaining the existing buffered PTY response transport
* Rework query handling and PTY writes together into a single larger transport refactor

## Decision Outcome

Chosen option: "Register Ghostty terminal callbacks for supported queries while retaining the existing buffered PTY response transport", because it moves terminal semantics into Ghostty now without mixing this branch with the separate backpressure and partial-write work reserved for PTY transport.

### Positive Consequences

* Color-scheme queries, XTWINOPS size reports, device-attributes queries, and XTVERSION now use Ghostty callbacks instead of local handcrafted response strings.
* Host color-theme changes now update Ghostty's stored color-scheme state and reuse Ghostty's reporting path when guest applications enable mode 2031.
* The Go TUI layer keeps ownership of host-event routing while Ghostty owns the query semantics themselves.

### Negative Consequences

* Response bytes are still buffered through the compatibility bridge and drained from Go, so the PTY transport path remains a follow-up.
* Title-change callbacks are still not surfaced because the current TUI has no consumer for embedded terminal titles.
* The bridge owns more callback glue code that must stay compatible with upstream Ghostty callback signatures.

## Pros and Cons of the Options

### Keep answering terminal queries through local Go response handling

Continue formatting supported terminal-query responses from Go and keep Ghostty limited to parsing and screen state.

* Good, because it avoids widening the compatibility bridge again.
* Good, because it keeps all response bytes visible in one Go layer.
* Bad, because it duplicates terminal semantics Ghostty already exposes through callbacks.
* Bad, because it leaves CodeLima farther from the Ghostling ownership boundary than the other completed Ghostty refinements.

### Register Ghostty terminal callbacks for supported queries while retaining the existing buffered PTY response transport

Use Ghostty callback registration for supported query types, but keep the current bridge-owned response buffer and Go-side draining for transport.

* Good, because it lets Ghostty own more terminal semantics immediately.
* Good, because it narrows the Go code to host-event updates plus response transport.
* Good, because it keeps this branch focused and leaves PTY transport for a later isolated change.
* Bad, because local response draining still exists until the transport work lands.
* Bad, because not every Ghostty callback is useful to CodeLima today.

### Rework query handling and PTY writes together into a single larger transport refactor

Replace local query handling and synchronous PTY writes in one combined change.

* Good, because it could remove more local plumbing in one step.
* Good, because direct callback-to-transport wiring may be cleaner once backpressure handling exists.
* Bad, because it combines two riskier changes into one branch.
* Bad, because it makes regressions in query semantics and PTY transport harder to isolate.

## Links

* Template [ADR_TEMPLATE.md](/Users/brianrackle/Projects/codelima/ADR_TEMPLATE.md)
* Related [use_ghostty_explicit_color_semantics_for_embedded_terminal_defaults_28.md](/Users/brianrackle/Projects/codelima/decisions/use_ghostty_explicit_color_semantics_for_embedded_terminal_defaults_28.md)
