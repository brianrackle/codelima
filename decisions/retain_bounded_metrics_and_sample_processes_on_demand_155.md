# Retain bounded daemon metrics and sample processes on demand

## Context and Problem Statement

The manager's Metrics tab needs one hour of CPU, memory and root-disk history,
including data collected before a window opened. Process CPU/RSS and per-core
readings must describe the guest, and opening more manager windows must not
multiply the daemon's regular telemetry work.

## Decision Drivers

* Reuse the existing forwarding SSH peer and one-second aggregate sampler.
* Keep history and wire responses bounded, with explicit unavailable samples.
* Run process probes only when requested by a visible, unpaused Metrics tab.
* Keep remote work outside the frontend event loop and forwarding mutex.

## Considered Options

* Keep bounded aggregate rings in the daemon and request process snapshots.
* Give every frontend its own SSH sampler and history.
* Persist metrics and continuously sample every guest's process list.

## Decision Outcome

Choose bounded daemon rings and optional process snapshots. Protocol 9 adds
`node.metrics`, an epoch and timestamp cursor, current per-core/cache/swap/uptime
details, and optional process results. Each node retains at most 3,600 aggregate
points for one hour. Replaced timestamps overwrite; old samples are ignored;
clearing current readings appends a gap. History expires after an hour without
observations and resets on daemon restart. It is not node metadata or handoff
state. Unknown readings remain absent rather than becoming zero.

The existing telemetry command also reads per-core `/proc/stat`, memory detail
and uptime. Process requests use that node's existing SSH peer with a separate
three-second deadline. A per-peer cancellable gate coalesces concurrent callers
and caches the result for one second. Stdout is capped at 2 MiB, stderr at
64 KiB, parsing at 4,096 processes, and the response at the top 64 rows. Names
and commands have fixed display limits. A frame-size regression includes a
full hour, 256 cores and worst-case escaped process text.

Process CPU is the delta of user and system ticks divided by guest elapsed
uptime and CLK_TCK; one logical core equals 100%. A changed process start time,
counter reset, reboot, first sample or interval over five seconds produces an
unknown CPU value. RSS comes from `ps`; load comes from `/proc/loadavg`.
Sampling failure is returned separately so aggregate history stays usable.
Process errors never alter forwarding routes or peer health.

The frontend rejects responses from older navigation generations and request
serials, cancels local waits on pause/navigation, and requests no further
process work while hidden. The server still bounds an already-admitted probe.
Time buckets average actual samples; empty intervals stay empty. Wide layouts
show resource plots and processes side by side; compact layouts use sparklines
and a scrolling process selection. Selection follows PID across sorting and
refreshes; clipboard delivery uses the existing host clipboard path.

### Positive Consequences

* Reconnecting frontends immediately recover aggregate history without a
  second persistent store or duplicate SSH connections.
* Slow or unavailable process probes do not prevent history display or affect
  routing health. Work and memory have explicit bounds.
* Multiple windows share process samples and can sort independently.

### Negative Consequences

* History is intentionally lost on daemon restart or live daemon replacement.
* The initial paused table could sort only the retained top rows. ADR 156
  extends the bounded snapshot to retain both ranking sets.
* The process sampler uses Linux procfs and procps inside the supported guest.
* A resumed process view needs two nearby samples for meaningful CPU deltas.

## Pros and Cons of the Options

Per-window sampling would simplify ownership but duplicate connections and
lose pre-open history. Persisting every metric and continuously collecting
processes would support historical process analysis, at the cost of unrelated
disk writes and recurring guest work. Neither is required by this design.
