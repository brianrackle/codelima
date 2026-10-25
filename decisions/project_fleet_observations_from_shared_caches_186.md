# 186. Project fleet observations from shared caches

## Context and Problem Statement

The home artboards require routes discovered after node creation, observed
resource values, compact capacity, and a marked-node impact panel. The fleet
previously read only frozen port declarations, rendered percentages at every
width, and always showed one selected node even when actions targeted hidden
marks. Polling guests during drawing would block navigation and duplicate the
daemon's observation work.

## Decision Drivers

* Preserve identity, marks and responsiveness across filtering and navigation.
* Distinguish missing/stale observations from measured zero values.
* Keep host allocation global even when the fleet is scoped.

## Considered Options

* Join existing asynchronous snapshots in the fleet projection.
* Probe each guest from fleet rendering and key handlers.
* Keep configured resources and ports as the complete observed state.

## Decision Outcome

Use existing snapshots. Nodes and Ports share the single-flight, generation-
fenced `ports.list` loader; navigating between them keeps an in-flight read,
while leaving both cancels it. No protocol change or guest RPC is introduced.
Fleet port filters join configured guest ports with cached routes by node ID.
Failed/stale observations keep their rows with `?`, and unknown port counts
sort last. The fleet browser action selects the first live HTTP route by port
and route ID for the selected, ready, running node. It requires a successful
inventory, enabled/authorized forwarding and a poll no older than the greater
of ten seconds or three configured intervals. It opens the node-qualified
address recorded in the snapshot.

Wide memory/disk cells show observed used/total GiB alongside gauges; compact
cells retain percentages. Compact navigation abbreviates inactive views and
preserves daemon connectivity and global allocation. The selected/marked
summary gets reserved space even when the compact table scrolls. Flat tables
show directory context. Lifecycle rows at both widths prefer the daemon's
recorded running phase/fraction over frontend output, labeling fractions as
phases rather than bootstrap steps.

Marks replace the single-node preview, include filtered-out targets, and
project resource release by intersecting target IDs with globally observed
allocated VMs. Failed-but-running VMs still hold resources. Unknown capacity
and allocation remain explicit. Session counts describe associated sessions;
they do not infer agent activity from names or output.

### Positive Consequences

* Fleet and Ports agree without a second discovery mechanism.
* Filtering cannot conceal targets or resources outside the visible scope.
* Stale observations cannot authorize a fleet browser launch.

### Negative Consequences

* Nodes now consumes the existing one-second cached routing query.
* A temporarily stale snapshot requires recovery before opening a route.
* Durable batch queues, retained failed-step rows, recent-event previews and
  complete session-impact presentation remain in TODO #57; this decision does
  not claim completion of the full fleet artboards.

## Pros and Cons of the Options

### Shared snapshots

Reuses daemon ownership and responsive UI machinery, with explicit cache age
and navigation tests required.

### Guest probes in rendering

Could request immediate samples but blocks input and duplicates discovery;
rejected.

### Configuration-only projection

Simple, but omits discovered routes and confuses resource limits with usage;
rejected.

## Links

* [Design and delivery ledger](../plans/tui-vnext/README.md)
* [QA flows](../QA.md)
