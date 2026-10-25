# Validate create forms outside the UI loop and bind navigation to admission

## Context and Problem Statement

The design's create form needs live global-name and path validation, template
choices, host capacity warnings and optional first-start attachment. Filesystem
reads in key handlers would block editing, and frontend callbacks after boot
would lose navigation intent or attach to the wrong startup after navigation.

## Considered Options

* Add service reads and lifecycle callbacks to the generic dialog.
* Replace every existing dialog and selector together.
* Wrap the existing input/discard model with a domain-specific create form.

## Decision Outcome

Wrap the generic dialog to retain its placeholder, paste and dirty-discard
behavior. The create form owns its presentation, inline errors, launch controls
and compact viewport. Parent overlays preserve form values across directory
browsing, template filtering/details and new-template editing. Sized overlays
center within the available terminal; the create flow dims the manager behind it.

Metadata and directory checks run outside the production event loop. Completion
events carry the exact form and path; obsolete paths are checked again after
the existing worker finishes. Metadata refreshes are single-flight and fenced
against intervening template creation. Global metadata supplies name uniqueness;
existing host observations supply fit warnings. Unknown allocation stays unknown,
and capacity warnings never make a valid form invalid. The service also compares
resolved paths to reject symlink aliases into CODELIMA_HOME.

Submit one create request with the ADR 173 startup flag. Keep the form until
admission returns. Definitive rejection preserves editable values; an ambiguous
reply preserves and freezes the exact payload and idempotency key for retry.
After admission, a transient receipt-bound intent follows the newly published
UUID into Overview. Explicit navigation revokes only this frontend intent.
Optional attachment reuses the successful-receipt and observed-readiness gate;
there is no unconditional terminal-pane switch.

The Starting panel sizes its compact phase list for the combined eight-phase
receipt. Shorter windows follow the active phase and show the visible phase
range instead of permanently clipping later startup stages.

### Positive Consequences

* Editing and rendering do not wait on metadata, runtime or filesystem work.
* A stale result cannot validate a newer path or reopen a dismissed form.
* Frontend exit does not discard accepted creation or first-start work.
* Template selection and capacity warnings use actual local state.

### Negative Consequences

* The create flow adds asynchronous state and nested overlay ownership.
* Validation is advisory until the service admits and executes the request;
  another frontend can change names or templates between observations.
* Direct SDK/test execution has no durable daemon admission boundary.

## Validation

The create suite covers name suggestions, ports, launch controls, discard,
stale paths, symlink aliases, nonblocking capacity warnings, ambiguous admission,
real-socket admission before boot, navigation revocation, compact scrolling,
template filtering/details/create-return and directory browsing. Full automated
and live guest qualification is tracked in the design ledger and TODO #57.

## Links

* [Create node](../plans/tui-vnext/reference/CreateNode.dc.html)
* [Template picker](../plans/tui-vnext/reference/TemplatePicker.dc.html)
* [Create and first-start ownership](own_creation_and_first_start_in_one_receipt_173.md)
