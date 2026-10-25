# Retain clone and optional child start under one daemon receipt

## Context and Problem Statement

The Dialogs artboard offers a suggested clone name and optional startup. A
frontend-owned second start would disappear when the frontend exits. Clone
temporarily stops a running source and owes its restoration even after explicit
cancellation; ordinary startup cancellation must never stop that source again.

## Decision Drivers

* Preserve source restoration, committed cloned disks and captured identity.
* Complete accepted work independently of the requesting connection.
* Keep Activity, node detail and fleet startup evidence accurate for the child.
* Show actual runtime/session impact and validate globally unique names.

## Considered Options

* Start the child from the frontend after clone returns.
* Start inside the clone body before its deferred source restoration.
* Complete cloning/restoration, then bind and start the child in the same daemon operation.

## Decision Outcome

Chosen option: complete cloning/restoration, then bind and start the child in the
same daemon operation. `NodeCloneInput.Start` defaults to false; CLI `--start`
and the TUI checkbox opt in. The local service separates clone execution from
startup so its source restoration defer finishes first. A failed source restore
prevents child boot. Startup failures retain the committed child and its stored
failure state for inspection and retry.

The manager reuses creation's receipt-binding transition and retains source,
child and slug reservations until completion. Cancellation cleanup tracks an
explicit startup target. It is empty throughout cloning and source restoration,
then becomes the child UUID before boot. Source restoration keeps its existing
independent bounded context. Canceling an observer still only ends its wait.

Clone/start receipts contain clone plus existing startup phases. Before the
first startup phase, they are clone operations on the source; afterward they
are child startup evidence. The bounded list preserves optional-start intent
and current phase without copying full inputs. Activity labels the combined
operation. Ordinary daemon protocol advances to **28** so version 27 cannot
silently ignore the startup request. Session/renderer/handoff remain **2/6/7**.

The TUI extends the captured-node review from ADR 196. It reads the complete
name index asynchronously, suggests the first available source-name suffix,
preserves edits, ignores results after dismissal and revalidates source identity
on submission. Service uniqueness remains authoritative if another client takes
the name. A mint border, name validation, actual source state and guest/host
impact accompany a keyboard/mouse checkbox. Ctrl+s submits; Esc cancels without
changing a preference. Pasted/repeated/released shortcuts and unreadable sizes
cannot submit. No duration estimate is fabricated.

### Positive Consequences

* Accepted clone/start work survives frontend exit with one retained result.
* Cancellation cannot undo source restoration by targeting the source as a child.
* Source-restoration and child-start failures preserve an inspectable clone.
* Direct service and daemon paths share clone/start ordering.

### Negative Consequences

* Clients must update an older daemon before using protocol 28.
* Source reservations remain held while the child starts; competing lifecycle
  work fails explicitly. Direct SDK callers still arbitrate through liveness
  tokens at the clone/start boundary and can receive a conflict.
* Name availability is advisory until service admission; reopen to refresh a
  failed name-index read. Source/runtime observations can change after review.

## Pros and Cons of the Options

### Frontend starts the child

* Good: small presentation change.
* Bad: lost work after disconnect and two unrelated receipts.

### Start inside the clone body

* Good: straightforward sequential code.
* Bad: the deferred source restart runs only after child boot; cancellation
  cleanup can confuse the original source with the child.

### Bind and start after source restoration

* Good: existing durable operation owner, reservations and phases are reused.
* Bad: requires explicit startup-target tracking and projection tests.

## Links

* [Captured node reviews](review_destructive_node_actions_with_captured_identity_196.md)
* [Daemon-owned node operations](daemon_owned_node_operations_and_retained_history_161.md)
* [Design and qualification ledger](../plans/tui-vnext/README.md)
