# Treat empty selection release as a no-op

## Context and Problem Statement

The native selection API returns `GHOSTTY_NO_VALUE` (-4) when no selection
exists. CodeLima formats the selection after every mouse release, including a
plain click without dragging. That normal gesture incorrectly produced
`copy terminal selection: native result -4` in Messages, obscuring investigation
of guest OSC 52 copying, which uses a separate path.

## Decision Drivers

* Keep native selection semantics and report real copy errors.
* Do not change the host clipboard when a gesture selected no text.

## Considered Options

* Treat no selection on mouse release as an empty successful interaction.
* Continue reporting a native error for plain clicks.

## Decision Outcome

Chosen option: "Treat no selection on mouse release as an empty successful
interaction". Normalize only `GHOSTTY_NO_VALUE` from selection formatting after
a release. Preserve other native errors and explicit copy behavior. The TUI's
existing empty-text guard skips clipboard delivery. Normal selection invalidation
still runs, so clearing a previous selection updates the display.

### Positive Consequences

* Plain clicks no longer create misleading clipboard error messages.
* Regression tests cover clicks with and without a previous selection and
  confirm that releasing a word selection still returns its text.

### Negative Consequences

* A selection that disappears before mouse release is also a quiet no-op.

## Pros and Cons of the Options

### Treat no selection on mouse release as an empty successful interaction

* Good, because absence of a selection is expected during normal clicks.
* Bad, because it requires recognizing one native result at this boundary.

### Continue reporting a native error for plain clicks

* Good, because every non-success native result stays visible.
* Bad, because normal gestures appear to fail copying when no copy was intended.

## Links

* Refines [native interaction adoption](adopt_static_libghostty_vt_with_bounded_worker_contracts_132.md).
* Separate from [guest clipboard routing](route_clipboard_to_the_seat_owners_event_connection_148.md).
