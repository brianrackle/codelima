# Reload Activity after a failed daemon handoff

## Context and Problem Statement

A successor daemon can acquire the process lock and accept Activity writes
before its committed acknowledgment reaches the predecessor. If that final
exchange fails, the predecessor resumes with an older journal cache. Reusing
that cache can hide acknowledged messages or allocate duplicate sequences.

## Considered Options

* Retain the predecessor's cached journal after rollback.
* Reload the journal on every request.
* Invalidate the cache when rollback follows release of daemon ownership.

## Decision Outcome

Invalidate the Activity journal and any cached initialization error after the
successor has stopped, while holding the exclusive update barrier and before
resuming the predecessor's public listeners. The next Activity request reloads
the retained records and obtains a new journal epoch. Existing idempotency keys
and sequence numbers survive; newly appended messages continue after the last
committed sequence. Failures before ownership was released retain the cache.

### Positive Consequences

* A failed update preserves messages accepted by its successor.
* A lost message acknowledgment can be retried without duplicate records.
* Frontends discard stale watermarks when they observe the new journal epoch.

### Negative Consequences

* The first Activity request after rollback reads the bounded retained journal.
* Invalid successor records remain visible as journal errors rather than being
  silently replaced with the predecessor's older view.

## Pros and Cons of the Options

Retaining the cache is fast but breaks the journal's single-writer assumptions
when ownership changes. Reading every request is consistent but adds disk work
to normal polling. Invalidating only after ownership was released concentrates
that cost at the failure boundary and preserves ordinary cached reads.

## Links

* [Shared Activity history](share_bounded_activity_messages_with_receipt_history_165.md)
