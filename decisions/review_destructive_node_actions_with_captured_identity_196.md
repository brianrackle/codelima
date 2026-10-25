# Review destructive node actions with captured identity

## Context and Problem Statement

The confirmation artboard requires a typed node name for deletion and a stop
review with guest-session impact. Generic dialogs omitted this evidence and
treated any terminal without a prompt marker as affected by a stop, including
host shells. Stop completion also discarded host tabs in the frontend.

## Decision Drivers

* Preserve the reviewed node and attachment identity across asynchronous work.
* Explain actual guest/host impact without inferring activity from titles.
* Keep service mutations outside drawing and the event loop.
* Make long reviews accessible at supported terminal sizes.

## Considered Options

* Compose a dedicated node review around the existing dialog input/submission.
* Add all review layout and session behavior to the generic dialog.
* Capture only a slug and resolve the selected node when submitting.

## Decision Outcome

Chosen option: compose a dedicated review. It retains the node ID and original
slug, revalidates action availability and conflicts before submission, and
accepts deletion only when the untrimmed input exactly matches that slug.
Errors retain the review; Esc discards disposable confirmation input directly.
Keyboard actions activate on press, never on paste, release or repeat.

The stop review uses recorded terminal kind and shared fresh activity evidence.
Only an idle guest prompt permits skipping the default busy review; unknown,
paused and working guest sessions ask. Host shells do not trigger it. The
checkbox persists `confirm_stop` through the existing asynchronous settings
owner, then revalidates the captured node before starting the stop. Cancellation
does not save the preference.

Inspect-before-stop captures an exact guest terminal ID, preferring observed
work. An ended terminal cannot fall back to another session or create one.
Raw attachment retains the review so Ctrl+] returns to it. Stop completion
closes guest tabs locally and retains host tabs; deletion keeps its existing
all-target cleanup. The daemon remains the operation and process owner.

The modal uses the shared theme, borders, text input and wrapping. Its height
follows content, with a bounded scroll area and fixed input/actions. Mouse
controls share keyboard validation. An unreadably small modal cannot submit.
Activation checks the current root size as well as the drawn modal: below the
manager's shared minimum dimensions, the overlay may not receive Draw at all.
Disk capacity is labeled virtual disk size; resource release follows runtime
observations. Historical context uses matching confirmed-stop evidence, with
an explicit unavailable fallback, rather than manufacturing last-up time.

### Positive Consequences

* Selection changes and late preference saves cannot retarget the operation.
* Copy/mounted workspace consequences and guest/host impact remain explicit.
* Session metadata, settings persistence and lifecycle ownership stay shared.

### Negative Consequences

* Full session lists require scrolling on compact terminals.
* Missing or stale evidence cannot reproduce the artboard's sample timestamps.
* Clone/start and durable parallel bulk review remain separate implementation
  work within the accepted full design, tracked in TODO #57.

## Pros and Cons of the Options

### Dedicated composed review

* Good: isolates domain evidence and interaction while reusing input/validation.
* Bad: requires its own responsive layout and mouse regression coverage.

### Extend the generic dialog

* Good: fewer overlay types.
* Bad: mixes terminal attachment and node lifecycle rules into every form.

### Resolve current selection at submission

* Good: simpler callbacks.
* Bad: asynchronous refresh and selection changes can alter the reviewed target.

## Links

* [Source](../plans/tui-vnext/reference/Dialogs.dc.html)
* [Captured shell choices](bind_shell_choices_to_captured_sessions_194.md)
* [Delivery and qualification](../plans/tui-vnext/README.md)
