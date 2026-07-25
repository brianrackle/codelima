# Sample Live Node CPU over the Daemon SSH Peer

## Context and Problem Statement

The TUI showed node lifecycle status and configured resources but did not show whether a running node was actively using its vCPUs. Live CPU must refresh once per second without starting one recurring `limactl` process per node or duplicating monitoring work in every open TUI.

## Decision Drivers

* Every visible node needs a comparable live CPU percentage.
* A one-second refresh must remain lightweight with multiple nodes and TUI windows.
* CPU telemetry is transient runtime truth and must not churn `node.yaml`.
* Sampling must work with both Lima VZ and QEMU guests.
* Missing, first-sample, stopped, and reset-counter states must render honestly.

## Considered Options

* Spawn a guest command from every TUI once per second.
* Sample host hypervisor processes.
* Extend the daemon's existing per-node SSH observation.

## Decision Outcome

Chosen option: "extend the daemon's existing per-node SSH observation", because the daemon already owns one persistent Go SSH client per running node and reconciles guest listeners once per second.

The observation command reads the aggregate `cpu` line from `/proc/stat` together with the existing `/proc/net/tcp*` inputs. The daemon calculates busy time from consecutive counter samples, excludes the duplicate `guest` fields, and normalizes aggregate utilization to `0..100%` regardless of configured vCPU count. The first sample, a counter reset, invalid counters, a stopped node, or an unavailable peer has no percentage.

The latest percentage and sample time are attached only to the in-memory `RuntimeObservation` returned by the daemon's `node.list` request. The TUI obtains that list once per second, preserves path scoping locally, and renders a fifth `CPU` property line plus the selected node's CPU value in its info and empty-terminal surfaces. The transient fields are never included in the durable node wire format.

### Positive Consequences

* Any number of TUI clients share one sample per running node.
* Sampling creates no recurring `limactl` subprocesses.
* Guest CPU semantics are the same across supported host hypervisors.
* CPU data follows the existing pure-read runtime-observation boundary.

### Negative Consequences

* CPU usage is unavailable until two valid samples have been collected.
* The metric represents guest aggregate busy time, not host hypervisor overhead.
* Each node block consumes one additional terminal row.
* CPU telemetry depends on the daemon's Lima SSH peer being healthy.

## Pros and Cons of the Options

### Spawn a guest command from every TUI once per second

* Good, because it is simple to initiate from the presentation process.
* Bad, because work multiplies by node count and TUI window count.
* Bad, because recurring `limactl shell` processes add avoidable CPU and latency.

### Sample host hypervisor processes

* Good, because it can include virtualization overhead.
* Bad, because VZ and QEMU expose different process models.
* Bad, because reliably associating a host process with a Lima node is platform-specific.

### Extend the daemon's existing per-node SSH observation

* Good, because it reuses one authenticated transport and one-second poll per node.
* Good, because Linux `/proc/stat` has stable counter semantics across supported guests.
* Good, because all TUI clients receive the same sample.
* Bad, because an SSH outage removes both dynamic discovery and CPU telemetry for that node.

## Links

* Refines [Render TUI Nodes as Multiline Property Blocks](render_tui_nodes_as_multiline_property_blocks_105.md).
* Extends [Periodically Refresh the TUI Project Tree](periodically_refresh_tui_project_tree_44.md).
* Follows [Use Lima as the Runtime Status Source for Read Surfaces](use_lima_as_the_runtime_status_source_for_read_surfaces_37.md).
