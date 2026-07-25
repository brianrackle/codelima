# Show Live Node Memory and Root-Disk Usage

## Context and Problem Statement

The TUI reported live guest CPU but showed memory and disk only as configured
capacity in the selected-node details. Operators need current consumption for
all three primary node resources, refreshed at the same one-second cadence,
without adding recurring runtime processes or counting a mounted host workspace
as guest-disk usage.

## Decision Drivers

* Memory and disk values must share the existing lightweight daemon sampling path.
* The displayed values must have stable semantics across supported Lima providers.
* Live telemetry must remain transient and must not churn `node.yaml`.
* One unavailable or malformed metric must not break service discovery or other metrics.
* Stopped nodes and unavailable peers must not display stale usage.

## Considered Options

* Show only configured memory and disk capacity.
* Infer consumption from host hypervisor processes and image files.
* Extend the daemon's existing per-node guest SSH observation.

## Decision Outcome

Chosen option: "extend the daemon's existing per-node guest SSH observation",
because it already polls every running node once per second for listener and CPU
data and is shared by every TUI client.

The observation reads `MemTotal` and `MemAvailable` from `/proc/meminfo` and
defines used memory as their difference. It reads the guest root filesystem
with `df -Pk /` and reports its used and total bytes. The root-filesystem scope
deliberately excludes a separately mounted host workspace from disk
consumption. Values are parsed with overflow and range checks; an invalid
memory or disk value is independently unavailable without invalidating
listener or CPU data.

The daemon stores the latest valid instantaneous values only in memory, clears
them on peer loss or node stop, and attaches them to transient
`RuntimeObservation` values returned by daemon `node.list`. The TUI renders
used/total binary units in `Memory` and `Disk` lines within each seven-line node
block and in both selected-node surfaces.

### Positive Consequences

* CPU, memory, disk, and listener discovery share one SSH command per node per second.
* Any number of TUI clients see the same daemon-owned sample.
* Memory semantics account for readily reclaimable Linux cache through `MemAvailable`.
* Disk semantics describe guest root capacity rather than host workspace contents.
* No live usage value is persisted.

### Negative Consequences

* Telemetry depends on Linux procfs, `awk`, `df`, and a healthy daemon SSH peer.
* Root-disk usage does not summarize additional guest filesystems.
* Two additional property rows reduce the number of nodes visible at once.
* A one-second sample can miss shorter resource spikes.

## Pros and Cons of the Options

### Show only configured memory and disk capacity

* Good, because the values already exist in durable node metadata.
* Bad, because capacity does not reveal current consumption.

### Infer consumption from host hypervisor processes and image files

* Good, because host observations could include virtualization overhead.
* Bad, because VZ and QEMU expose different process and storage models.
* Bad, because sparse image-file allocation does not equal guest filesystem usage.

### Extend the daemon's existing per-node guest SSH observation

* Good, because it adds no recurring process or transport.
* Good, because Linux guest interfaces give provider-independent semantics.
* Good, because the daemon can clear all live values at one lifecycle boundary.
* Bad, because an SSH outage removes listener and resource observations together.

## Links

* Extends [Sample Live Node CPU over the Daemon SSH Peer](sample_live_node_cpu_over_daemon_ssh_110.md).
* Refines [Render TUI Nodes as Multiline Property Blocks](render_tui_nodes_as_multiline_property_blocks_105.md).
* Follows [Use Lima as the Runtime Status Source for Read Surfaces](use_lima_as_runtime_status_source_for_read_surfaces_37.md).
