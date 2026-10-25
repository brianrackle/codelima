# Record committed daemon updates in Activity

## Context and Problem Statement

The Activity design includes successful daemon updates and the sessions they
preserved. A CLI or TUI notice can disappear when that client exits and cannot
prove that the successor took ownership. Failed imports must not claim success.

## Considered Options

* Publish from the updating frontend or retiring daemon.
* Publish from the successor after its final commit acknowledgment.

## Decision Outcome

The successor queues one informational Activity record after successfully
sending the committed acknowledgment and opening native publication. It records
the previous binary version, its own version, and the number of adopted terminal
sessions. Version labels are bounded plain text; missing labels say unknown.
The existing owned, bounded native queue supplies stable retry identity and
durable storage. No protocol or storage-shape change is required.

### Positive Consequences

* Update history exists without a TUI or a surviving CLI caller.
* Unauthorized commits and failed acknowledgment writes cannot announce success.
* Retained session counts come from the actual adopted set.

### Negative Consequences

* A crash before queue persistence can lose this informational record.
* Sending the acknowledgment is the local commit boundary; it does not prove
  the predecessor received it before a subsequent connection failure.
* Cold restarts and legacy restart fallbacks do not claim an in-place update.

## Pros and Cons of the Options

Frontend or predecessor publication is easy to display but can report success
too early or disappear with the caller. Successor publication ties the record
to ownership and actual adoption, with the existing queue's delivery limits.

## Links

* [Native Activity ownership and delivery](record_native_route_and_capacity_activity_168.md)
