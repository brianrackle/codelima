# Preserve shared tabs across scoped TUI refresh

Status: Accepted

## Context and Problem Statement

The daemon owns one global terminal set for a CodeLima home, while each TUI may show only nodes beneath its launch directory. On refresh, a scoped TUI treated every target absent from its projection as deleted and closed those daemon terminals, so two processes rooted in different directories could delete one another's tabs during restart.

## Decision Drivers

* Quitting and reopening one TUI must not destroy tabs belonging to another scoped process.
* Explicit node stop and delete operations must still close affected tabs.
* Ambiguous metadata read failures must not trigger destructive reconciliation.

## Considered Options

* Treat absence from the scoped TUI list as node deletion.
* Never prune tabs during TUI refresh.
* Confirm absence against the global node store before pruning.

## Decision Outcome

Chosen option: "confirm absence against the global node store before pruning", because the durable store can distinguish a node outside the current directory projection from a node that is actually missing or deleted.

The scoped list remains the fast positive check. When a terminal target is absent from that list, refresh loads the node by durable ID and closes the tab only when metadata confirms `NotFound`, `DeletedAt`, or the terminated node lifecycle state. Other metadata errors retain the tab. Explicit stop and delete results continue to close target tabs immediately.

### Positive Consequences

* Disjoint path-scoped TUI processes preserve one another's daemon tabs across refresh and restart.
* External node deletion still removes stale terminal views after refresh.
* Transient or corrupt metadata cannot silently destroy a live terminal.

### Negative Consequences

* Refresh may perform one metadata read for each restored target outside the current scope.
* A tab can remain visible in daemon state while an ambiguous metadata failure persists.

## Pros and Cons of the Options

### Treat scoped absence as deletion

* Good, because it needs no additional metadata reads.
* Bad, because a projection cannot distinguish deletion from another window's directory scope.

### Never prune on refresh

* Good, because refresh cannot destroy shared tabs.
* Bad, because deletion performed by another process leaves stale terminal views indefinitely.

### Confirm against the global store

* Good, because it separates view scope from durable lifecycle truth.
* Good, because it preserves existing external-deletion cleanup.
* Bad, because it adds small per-target file reads during reconciliation.

## Links

* Refines [Move terminal runtime ownership into a per-home daemon](daemon_owned_terminal_runtimes_64.md).
* Refines [Replace projects with directory-bound nodes and reusable configurations](replace_projects_with_directory_bound_nodes_and_reusable_configurations_72.md).
* Complements [Preserve daemon tab order at state boundaries](preserve_daemon_tab_order_at_state_boundaries_88.md).
