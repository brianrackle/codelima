# Share bounded CPU trends and paused process rankings

## Context and Problem Statement

The fleet needs an eight-second CPU sparkline and the selected preview needs
60 seconds of CPU history before a frontend has collected any pushes. A
paused Metrics table also needs both CPU and memory leaders when the operator
changes its sort order.

## Considered Options

* Add bounded presentation data to existing inventory and Metrics responses.
* Start additional samplers for fleet/preview and resume process probes on sort.
* Show only observations collected since the frontend opened.

## Decision Outcome

Extend the existing responses in protocol 10. Inventory observations include
at most 60 CPU-only timestamp/value points copied from the daemon's aggregate
ring, including unavailable samples. Usage pushes carry the metric epoch and
continue to send only the current sample. Frontends merge inventory and pushes
into a bounded 60-point CPU trend, preserve newer pushed history across stale
inventory responses, and discard previous-epoch history when the daemon
restarts. Node metadata still excludes transient runtime observations.

The fleet renders eight real-time buckets; the preview renders a 60-second
plot. Missing buckets remain blank. Neither surface creates guest work beyond
the aggregate sampler introduced by the existing telemetry architecture.

Process responses retain the top 64 CPU and top 64 memory rows, deduplicating
overlap. At most 128 rows cross the wire, while the visible table remains at
64. The inactive ranking lives in `alternate_rows`, so paused sorting can
choose the same global leaders without a new request. Selection follows its
PID if present in the new visible ranking, otherwise moves to the first row.
Clipboard requests always use a visible selected PID. The worst-case frame
test covers both rankings, a full hour of history and 256 current core values.

### Positive Consequences

* New frontends immediately show recent CPU history without extra SSH work.
* Pausing freezes observations while preserving both process rankings.
* Retention, ownership and response size have explicit bounds and regressions.

### Negative Consequences

* Inventory responses grow by at most 60 small points per running node.
* Metrics responses may carry another 64 rows, increasing their bounded size.
* Inventory reconciliation must preserve sample ordering and metric epochs.

## Pros and Cons of the Options

Frontend-only history would reduce inventory size but lose the pre-open
preview. Additional sampling or resuming probes on sort would duplicate guest
work and weaken pause semantics. Bounded response data reuses observations
already owned by the daemon.

## Links

* Extends [ADR 155](retain_bounded_metrics_and_sample_processes_on_demand_155.md).
