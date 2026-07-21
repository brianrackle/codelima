# Route Dialog Arrow Keys by Field Semantics

## Context and Problem Statement

TUI dialogs use the same row model for editable text fields and selector fields. The dialog-level key handler reserved `Right` for opening selectors before checking the active field type, so `Right` was discarded in configuration text fields and users could move the cursor left but not back to the right. How should arrow keys be routed without breaking selector activation?

## Decision Drivers

* Text fields must retain normal single-line cursor movement.
* Selector fields must remain keyboard-accessible from the same dialogs.
* Key routing should depend on field capability rather than dialog title or field position.
* Existing form navigation and submit shortcuts must remain unchanged.

## Considered Options

* Route `Right` to selector activation only when the active field has an activation callback; otherwise pass it to the text input.
* Reserve `Right` globally and add alternate text-cursor shortcuts.
* Open selector fields only with `Enter` and pass `Right` to every text model.

## Decision Outcome

Chosen option: "Route `Right` by active-field capability", because it restores expected text editing while preserving the established `Right choose` interaction for selectors. Dialog-level handling consumes `Right` only when the active field exposes an activation callback. All other input keys, including unmodified `Left` and `Right`, reach the active text-input model.

### Positive Consequences

* Configuration slugs, image names, agent profiles, and resource values can be edited with both horizontal arrow keys.
* The rule applies uniformly to every dialog text field.
* Selector fields still open with `Right` or `Enter`.
* Form-level `Tab`, `Up`, `Down`, submit, and cancel behavior is unchanged.

### Negative Consequences

* `Right` has context-sensitive behavior that depends on the active field type.
* Future non-text fields must expose an activation callback to participate in the selector behavior.

## Pros and Cons of the Options

### Route `Right` by active-field capability

* Good, because text inputs receive the cursor key they already know how to handle.
* Good, because selector activation remains backward compatible.
* Good, because one generic rule covers all dialogs.
* Bad, because footer wording must communicate the field-sensitive behavior.

### Reserve `Right` globally and add alternate text-cursor shortcuts

* Good, because the dialog handler stays simple.
* Bad, because it violates standard text-input behavior and leaves arrow-key navigation broken.
* Bad, because users would need to discover nonstandard cursor shortcuts.

### Open selectors only with `Enter`

* Good, because every horizontal arrow could always be passed through.
* Bad, because it removes the documented `Right choose` interaction unnecessarily.
