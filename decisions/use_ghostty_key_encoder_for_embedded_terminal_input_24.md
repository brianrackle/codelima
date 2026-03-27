# Use Ghostty Key Encoder for Embedded Terminal Input

## Context and Problem Statement

The embedded terminal already delegated VT parsing and render state to Ghostty, but keyboard input was still encoded by local Go tables modeled after the Vaxis terminal widget. That duplicated terminal-sequence logic in a part of the stack Ghostty already implements, and it made it harder to follow the Ghostling pattern of letting Ghostty own more terminal semantics. The question was how to improve keyboard-sequence correctness without replacing the existing runtime-loaded Ghostty integration.

## Decision Drivers

* Reduce duplicated terminal input-encoding logic in Go
* Keep the existing runtime-loaded `libghostty-vt` packaging model
* Preserve compatibility for keys not covered by the current Ghostty C API surface
* Improve behavior for release events and base-layout-driven key encoding

## Considered Options

* Keep the existing Go key encoder only
* Switch the embedded terminal keyboard path to Ghostty's key encoder with a runtime-loaded optional bridge and fallback
* Replace runtime loading with direct linking to a larger Ghostty C API

## Decision Outcome

Chosen option: "Switch the embedded terminal keyboard path to Ghostty's key encoder with a runtime-loaded optional bridge and fallback", because it adopts a Ghostling-style ownership boundary for keyboard encoding while keeping packaging and startup behavior stable.

### Positive Consequences

* Common embedded-terminal key sequences now come from Ghostty instead of local escape-sequence tables.
* Release events can be suppressed through Ghostty's encoder instead of being treated like presses.
* Keys outside the currently mapped Ghostty enum surface still fall back to the existing Go encoder, so compatibility is preserved.
* The runtime-loaded bridge remains in place, which keeps the packaged library model unchanged.

### Negative Consequences

* The bridge now depends on Ghostty key-encoder headers being present after `make init`.
* The Ghostty keyboard path still does not inherit every newer Ghostty input API that Ghostling uses, such as mouse encoders or terminal-driven keyboard-mode helpers.
* The integration surface in the cgo bridge is larger and therefore slightly more maintenance-heavy.

## Pros and Cons of the Options

### Keep the existing Go key encoder only

Continue encoding all keyboard input locally in Go.

* Good, because it keeps the cgo bridge smaller.
* Good, because it avoids any compile-time dependency on Ghostty key headers.
* Bad, because it continues duplicating terminal input logic that Ghostty already owns.
* Bad, because it keeps release-event handling and base-layout mapping in a less accurate custom path.

### Switch the embedded terminal keyboard path to Ghostty's key encoder with a runtime-loaded optional bridge and fallback

Use Ghostty's key encoder when the runtime-loaded library exports it, and fall back to the legacy Go encoder for unsupported keys or older libraries.

* Good, because it follows the same ownership direction Ghostling uses for keyboard encoding.
* Good, because it improves correctness without forcing a packaging-model change.
* Good, because it allows gradual adoption of more Ghostty input APIs later.
* Bad, because it adds bridge code and key mapping glue.
* Bad, because newer Ghostty features still require additional API surface before they can be adopted.

### Replace runtime loading with direct linking to a larger Ghostty C API

Drop `dlopen` and link directly against Ghostty's C API.

* Good, because it would make all compile-time-declared Ghostty APIs immediately available.
* Good, because it simplifies some symbol loading code.
* Bad, because it changes the current packaging and runtime-library discovery model.
* Bad, because it is broader in scope than the keyboard improvement required here.

## Links

* Template [ADR_TEMPLATE.md](/Users/brianrackle/Projects/codelima/ADR_TEMPLATE.md)
* Related [adopt_ghostty_0.md](/Users/brianrackle/Projects/codelima/decisions/adopt_ghostty_0.md)
