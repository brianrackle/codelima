# Keep Daemon Requests Idle-Safe and Coalesce Renderer Publications

## Context and Problem Statement

A terminal-freeze capture showed healthy daemon and terminal probes alongside
recurring TUI request-socket timeouts. The request client left its bounded
handshake deadline on the long-lived response reader, so an otherwise healthy
connection expired after five idle seconds. At the same time, every renderer
mutation synchronously built and logged a full snapshot plus four read views,
amplifying ordinary terminal output and one-second TUI refresh focus calls.

## Decision Drivers

* An authenticated request connection must remain valid while its TUI is idle.
* Individual writes and calls must retain bounded failure detection.
* Terminal mutations must be acknowledged without synchronously serializing a
  full screen bundle.
* Rendering must preserve the established 20 FPS active-update ceiling and do
  no timer work while idle.
* The latest mutation in a burst must eventually appear in the immutable cache.
* Diagnostics must retain failures and genuinely slow renderer calls without
  logging two records for every normal operation.
* Live node CPU, memory, and disk sampling must retain its one-second product
  cadence unless evidence identifies it as the bottleneck.

## Considered Options

* Reduce or remove live node telemetry.
* Clear only the stale client deadline and keep synchronous renderer publication.
* Clear phase deadlines and coalesce renderer publications behind a dirty edge.

## Decision Outcome

Chosen option: "clear phase deadlines and coalesce renderer publications behind
a dirty edge", because it fixes both captured failure mechanisms without
weakening telemetry or terminal correctness.

The client applies a connection-wide deadline only while dialing and completing
the exact-version handshake. After a successful hello it clears that deadline
before starting the long-lived response reader. Every later request still has
a bounded write deadline and a context/timeout-bounded pending response.
Application heartbeats and the reconnect supervisor remain responsible for
detecting a silent established connection.

The renderer worker publishes initialization and explicit recovery snapshots
synchronously. Renderer operations apply to the Ghostty actor and acknowledge
their request; only an actual renderer invalidation enqueues a capacity-one
dirty edge. Key and focus encoding that changes no screen state therefore does
not publish an unchanged pre-echo cursor frame. A separate publisher emits the
full immutable snapshot/read bundle no more than once every 50 milliseconds.
Work arriving during publication retains one trailing edge, so the final state
is not lost. Publication and worker shutdown share a mutex because a
non-returning native publication must remain terminal-local and killable by the
existing supervisor.

The TUI sends `terminal.focus` only on transitions into focus, rather than on
every one-second refresh. A renderer publication marks the daemon cache stale
and wakes its snapshot publisher, but the daemon broadcasts only the resulting
fresh dirty event. Renderer workers log failed operations and successful calls
slower than 250 milliseconds; normal start/completion records are omitted. The
client redraws every new daemon snapshot sequence even when its output
generation and geometry are unchanged, preserving cursor- and viewport-only
changes.

### Positive Consequences

* An idle request connection no longer reconnects every handshake-timeout interval.
* Terminal mutation acknowledgements are not serialized behind full-grid JSON
  generation.
* Output bursts produce at most 20 full renderer publications per second and
  wake no publication timer while idle.
* Ordinary key encoding waits for real PTY output instead of publishing an
  unchanged cursor position first.
* Repeated telemetry refreshes no longer generate repeated focus publications.
* Each renderer publication produces one client-visible fresh dirty edge.
* Daemon logs retain actionable renderer failures and latency outliers without
  per-operation volume.
* Live resource telemetry continues to refresh once per second.

### Negative Consequences

* A visible terminal update may wait up to 50 milliseconds for the next
  publication boundary.
* Renderer workers now have one additional goroutine and a publication/shutdown
  mutex.
* Normal renderer-operation duration is no longer recoverable from info logs;
  focused profiling or a lower temporary threshold is required for that detail.

## Pros and Cons of the Options

### Reduce or remove live node telemetry

* Good, because it would reduce one recurring TUI refresh source.
* Bad, because the captured guest observation takes only a few milliseconds and
  is shared by every client.
* Bad, because it does not clear the stale request-socket deadline.
* Bad, because terminal output would still publish and log one full bundle per
  mutation.

### Clear only the stale client deadline

* Good, because it stops the five-second request reconnect cycle.
* Good, because it is a small transport fix.
* Bad, because output, input, and focus remain serialized behind full snapshot
  generation and large JSON frames.
* Bad, because per-call renderer logging continues to amplify active terminals.

### Clear phase deadlines and coalesce renderer publications

* Good, because it addresses both independently observed failure mechanisms.
* Good, because bounded request writes, call timers, heartbeats, and renderer
  health checks preserve failure detection.
* Good, because capacity-one dirty edges preserve the latest state with bounded
  active work and no idle polling.
* Bad, because publication is eventually consistent within the 50-millisecond
  active cadence.

## Links

* Refines [Treat daemon connections as disposable, resumable sessions](treat_daemon_connections_as_disposable_resumable_sessions_107.md).
* Refines [Isolate Ghostty in per-terminal renderer processes](isolate_ghostty_in_per_terminal_renderer_processes_108.md).
* Preserves [Sample Live Node CPU over the Daemon SSH Peer](sample_live_node_cpu_over_daemon_ssh_110.md).
* Preserves [Show Live Node Memory and Root-Disk Usage](show_live_node_memory_and_disk_usage_111.md).
