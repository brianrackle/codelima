# 188. Read the selected fleet preview from retained metrics and runtime configuration

## Context and Problem Statement

The home artboard includes CPU average/peak, memory history and available bytes,
runtime/mount/nesting identity and workspace access. CPU-only inventory trends
cannot supply memory history, and configured node resources or host capabilities
cannot establish these guest/runtime facts. Marked rows also show the working
task associated with each target.

## Decision Drivers

* Reuse the daemon's existing sampler and bounded history.
* Keep rendering free of I/O and preserve missing observations.
* Inspect the selected instance's configuration without preparing a guest.
* Keep inventory/subscriber payload size independent of preview detail.

## Considered Options

* Extend the selected-node fleet evidence projection.
* Add memory history and runtime configuration to every inventory row.
* Sample the selected guest and inspect configuration during rendering.

## Decision Outcome

Ordinary protocol **25** adds an optional selected preview to
`node.fleet_evidence`. Its existing selected/recent node requests at most 60
aggregate samples from the retained last minute. The forwarder's read lock
protects the ring; every returned pointer value is copied. No process probe,
guest command, new sampler or whole-hour history is needed. Session protocol 2
and handoff protocol 6 remain unchanged.

An optional runtime configuration inspector reads the selected Lima instance's
effective `lima.yaml` under the configured Lima home. It shares Spec's bounded
regular-file YAML parser, validates the instance name, and returns only bounded
VM/mount types, optional nesting and the matching workspace mount's path/access.
This describes configuration, not a fresh guest capability probe. A missing
setting remains unknown. A mount for another path cannot establish workspace
access. Runtime observation VM type takes precedence when available. Spec
continues inspecting the separately retained rendered recipe.

The frontend fences this projection by selected node and metadata revision and
reuses the existing asynchronous cancellation/staleness handling. CPU statistics
exclude samples outside the displayed minute and missing values. Memory charts
preserve gaps; available memory is total minus observed used memory, matching
the sampler's definition. Retained observations are labeled as last when stale.
The preview includes uptime, creation/auth state and Lima identity. Configured
agent checks are shown as passed only for matching fresh succeeded-start
evidence on a running, bootstrap-complete node.

Marked rows include the first terminal-reported working task, working count,
prompt count or explicit unknown activity. Titles never determine activity.
Durable idle timing and complete multi-agent validation remain in TODO #57.

### Positive Consequences

* New frontends see retained CPU and memory history immediately.
* Resource and runtime labels use the owning sources rather than host guesses.
* Preview detail does not expand every node's inventory or telemetry event.

### Negative Consequences

* The visible selected preview reads one bounded local configuration each poll.
* Configuration may differ from guest behavior after an out-of-band edit; the
  display does not claim a capability probe or a successful new boot.
* Optional integration and unsupported runtimes leave explicit unknowns.

## Pros and Cons of the Options

### Selected evidence projection

Shares existing polling, ownership and fences while limiting work to one node;
requires an ordinary protocol bump and selected-response tests.

### Full inventory enrichment

Would simplify rendering but repeats memory history and configuration reads for
unselected nodes and risks the bounded protocol frame; rejected.

### Render-time probes

Would couple input/redraw latency to guest access and add competing samplers;
rejected.

## Links

* [Fleet evidence](read_bounded_fleet_evidence_independently_of_activity_pages_187.md)
* [Design ledger](../plans/tui-vnext/README.md)
* [QA](../QA.md)
