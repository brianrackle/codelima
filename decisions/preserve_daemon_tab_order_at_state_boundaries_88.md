# Preserve daemon tab order at state boundaries

Status: Accepted

## Context and Problem Statement

The daemon owns terminal runtimes in a Go map, while the TUI, session cache, and live-update manifest all expose terminal states as ordered lists. Iterating the runtime map directly made tab order change when a TUI reconnected or a daemon state boundary was crossed.

## Decision Drivers

* Surviving tabs must keep their creation order when the TUI quits and reopens.
* Persistence and live update must use the same order as the live list API.
* Existing session version 2 metadata already records each tab's creation time.

## Considered Options

* Return and persist direct map iteration order.
* Sort only in the TUI client after `terminal.list`.
* Sort every daemon state boundary by creation time and preserve that time during respawn.

## Decision Outcome

Chosen option: "sort every daemon state boundary by creation time and preserve that time during respawn", because the daemon is the common owner for live listing, persistence, restart, snapshots, and handoff.

`daemonHost.list` returns states ordered by `CreatedAt`, with terminal ID as a deterministic tie-breaker. Session restore sorts saved states before respawning them and restores the original creation time before writing the normalized session. Consumers therefore receive one canonical order without depending on Go map iteration.

### Positive Consequences

* TUI restarts retain per-node tab order.
* Session files, daemon snapshots, and handoff manifests share one deterministic order.
* Existing session version 2 files need no migration.

### Negative Consequences

* Listing performs an `O(n log n)` sort; terminal counts are expected to remain small.
* Truly simultaneous legacy timestamps use terminal ID order because the old schema has no explicit sequence field.

## Pros and Cons of the Options

### Return direct map order

* Good, because it performs no sorting.
* Bad, because Go intentionally does not define map iteration order.

### Sort only in the TUI

* Good, because it fixes one visible consumer.
* Bad, because persisted sessions and live-update manifests remain nondeterministic.

### Sort at daemon state boundaries

* Good, because every consumer observes the same order.
* Good, because it reuses existing durable creation metadata.
* Bad, because the daemon performs a small sort for each list or persistence operation.

## Links

* Refines [ADR 64](daemon_owned_terminal_runtimes_64.md).
* Refines [ADR 66](version_daemon_session_persistence_66.md).
* Preserves the tab model from [ADR 77](open_node_host_shells_as_ordinary_terminal_tabs_77.md).
