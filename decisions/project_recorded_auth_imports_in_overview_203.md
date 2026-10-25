# Project recorded credential imports in Overview

## Context and Problem Statement

Overview's design includes the host credential import date and item categories.
The node's completion flag cannot establish either fact, or distinguish an
empty import from a populated one. The importer already records a content-free
`node.auth.imported` event after successful placement.

## Considered Options

* Project the existing retained event for the selected node.
* Add another persistent receipt and migration path.
* Infer imports from enabled settings, the host's current files or parent state.

## Decision Outcome

Project the latest import event in the existing bounded event-log tail, only
when the selected node requested import and its completion flag is set. Reuse
the nonblocking regular-file reader and 2 MiB diagnostic limit. No credentials
are read and no guest commands run. A missing, malformed, truncated or unreadable
receipt remains unavailable. A malformed latest import cannot revive an earlier
receipt. The historical timestamp is preserved even if the clock later moves.

The response contains only the import timestamp, unique item count and ordered
categories (`ssh`, `git`, `codex`, `claude`). Recognize the importer's exact
artifact vocabulary; reject unknown or duplicate names. Do not forward raw
artifact names, skipped-item reasons, paths or contents. The existing selected
node/revision fence prevents one node's receipt from appearing on another.

Overview shows date/count and categories on adjacent identity rows so the
compact layout can display both. Empty imports show zero items and `none`.
Clones without their own receipt say inherited; never read a parent's mutable
history as proof of what the cloned disk contains. These are historical import
facts, not assertions that credentials remain present or valid.

The additive evidence response bumps ordinary daemon protocol to 30. Session,
renderer and handoff versions remain 2/6/7; storage and import behavior are
unchanged.

### Positive Consequences

* The design's import details come from the actual completed operation.
* Bounded selected-node inspection needs no new persistent state or guest work.
* Missing and inherited evidence stay explicit, including an empty import.

### Negative Consequences

* Receipts outside the retained 2 MiB tail cannot be summarized here; the
  complete event file remains available through Logs.
* Old clones have no frozen per-item receipt. They retain the inherited label.
* Older ordinary clients require an explicit daemon update.

## Pros and Cons of the Options

### Existing retained event

Uses the authoritative operation record and established bounded read path,
but cannot recover a receipt outside that read window.

### Separate persistent receipt

Would retain the summary independently of log size, but introduces a second
durable representation and migration/clone consistency requirements.

### Infer from settings or other state

Requires little code, but settings and current host or parent files do not
prove what this node imported at creation time.

## Links

* Extends [selected preview evidence](read_selected_fleet_preview_from_retained_metrics_and_runtime_configuration_188.md).
* Verification: `make test-fleet-evidence`, `make test-overview` and the Overview
  identity check in `QA.md`.
