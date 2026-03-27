# Use Ghostty Mouse Encoder for Embedded Terminal Input

## Context and Problem Statement

The embedded terminal already delegated VT parsing, render state, and keyboard encoding to Ghostty, but terminal mouse reporting was still encoded by local Go logic modeled after the Vaxis terminal widget. That duplicated protocol-selection logic in a part of the stack Ghostty already implements, and it made CodeLima diverge from Ghostling's pattern of letting Ghostty own terminal-side mouse semantics. The question was how to improve mouse-reporting correctness without replacing the existing runtime-loaded Ghostty integration.

## Decision Drivers

* Reduce duplicated terminal mouse-encoding logic in Go
* Keep the existing runtime-loaded `libghostty-vt` packaging model
* Preserve compatibility when the current Ghostty C API surface is unavailable
* Keep CodeLima-specific host gestures above the terminal-reporting boundary

## Considered Options

* Keep the existing Go mouse encoder only
* Switch the embedded terminal mouse-reporting path to Ghostty's mouse encoder with a runtime-loaded optional bridge and fallback
* Replace runtime loading with direct linking to a larger Ghostty C API

## Decision Outcome

Chosen option: "Switch the embedded terminal mouse-reporting path to Ghostty's mouse encoder with a runtime-loaded optional bridge and fallback", because it adopts a Ghostling-style ownership boundary for mouse encoding while keeping packaging and host-level TUI behavior stable.

### Positive Consequences

* Common embedded-terminal mouse sequences now come from Ghostty instead of local Go protocol selection.
* Terminal mode changes now drive the active mouse reporting format through Ghostty rather than duplicated local checks.
* Wheel, drag, motion, and release encoding can share the same terminal-owned encoder path.
* The runtime-loaded bridge remains in place, which keeps the packaged library model unchanged.

### Negative Consequences

* The bridge now depends on Ghostty mouse-encoder headers being present after `make init`.
* The Go terminal still needs fallback logic for unavailable Ghostty mouse APIs or unsupported event mappings.
* Scrollback ownership is still local for now, so Ghostty owns mouse reporting but not yet the full viewport model.

## Pros and Cons of the Options

### Keep the existing Go mouse encoder only

Continue encoding all terminal mouse-reporting sequences locally in Go.

* Good, because it keeps the cgo bridge smaller.
* Good, because it avoids depending on Ghostty mouse headers.
* Bad, because it continues duplicating protocol logic that Ghostty already owns.
* Bad, because it keeps mouse-format correctness tied to local mode handling instead of terminal state.

### Switch the embedded terminal mouse-reporting path to Ghostty's mouse encoder with a runtime-loaded optional bridge and fallback

Use Ghostty's mouse encoder when the runtime-loaded library exports it, and fall back to the legacy Go encoder for unavailable APIs or unsupported mouse events.

* Good, because it follows the same ownership direction Ghostling uses for mouse reporting.
* Good, because it improves correctness without forcing a packaging-model change.
* Good, because it allows gradual adoption of more Ghostty terminal APIs later.
* Bad, because it adds bridge code and mouse-event mapping glue.
* Bad, because Ghostty still does not own viewport scrolling in CodeLima yet.

### Replace runtime loading with direct linking to a larger Ghostty C API

Drop `dlopen` and link directly against Ghostty's C API.

* Good, because it would make all compile-time-declared Ghostty APIs immediately available.
* Good, because it simplifies some symbol loading code.
* Bad, because it changes the current packaging and runtime-library discovery model.
* Bad, because it is broader in scope than the mouse-reporting improvement required here.

## Links

* Template [ADR_TEMPLATE.md](/Users/brianrackle/Projects/codelima/ADR_TEMPLATE.md)
* Related [use_ghostty_key_encoder_for_embedded_terminal_input_24.md](/Users/brianrackle/Projects/codelima/decisions/use_ghostty_key_encoder_for_embedded_terminal_input_24.md)
