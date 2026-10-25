# Observe daemon runtime without terminal snapshots

## Context and Problem Statement

The System artboard needs current session/connection counts, seat ownership,
forwarding health, telemetry cadence and VirtioFS reclaim observations. The
existing full diagnostic snapshot also collects terminal grids and renderer
state. Polling it would make ordinary navigation pay diagnostic costs and its
response size would grow with terminal content.

## Considered Options

* Poll the full daemon snapshot and select the required fields in the TUI.
* Add a typed cached overview and refresh it only while System is visible.
* Read settings locally and infer runtime health from configuration.

## Decision Outcome

Choose a typed cached overview. Ordinary protocol 20 adds daemon.overview and
the unique subscribed logical-client count in daemon.status. Session 2,
handoff 6 and renderer 5 do not change. An optional handler supplies runtime
observations; absent support is represented explicitly rather than as zero.

The overview combines server identity/session/seat data with cached forwarding
and reclaim summaries. It does not read terminal grids, probe guests, invoke
renderer diagnostics or perform disk I/O. Forwarding counts actual listener
sockets, configured port entries, degraded port entries and lapsed routes as
separate quantities. A node-inventory error remains visible. Subscribers count
unique logical event clients, including observers; request connections and
probes are excluded. The UI therefore labels them subscribers, not TUIs.

A successful importer records its update time after sending the committed
acknowledgment. Failed imports and ordinary restarts cannot claim an in-place
update. This timestamp describes the current daemon incarnation and is not a
new persisted history store; existing Activity keeps the durable update record.

System issues one background request at a time, normally every three seconds,
with a five-second deadline. Explicit C requests an immediate refresh. Navigation and shutdown cancel it; generation
fences reject late replies. Reconnection starts a fresh generation. A failed
refresh retains the previous observation and its timestamp with a stale label.
Compact and wide panels expose scrollable observations and keyboard/mouse
details for complete wrapped errors. No repair or lifecycle action occurs as
part of rendering or refresh.

### Positive Consequences

* Routine refresh cost is independent of terminal grid/output size.
* Visible runtime state remains truthful when settings differ from a running
  daemon or a refresh fails.
* Slow requests do not block the event loop or accumulate after navigation.
* Full error text remains reachable at compact terminal sizes.

### Negative Consequences

* Cached observations can be three seconds old; component sample timestamps
  differ, and the overview is not an atomic cut of every subsystem.
* Exact ordinary protocol matching requires updating the daemon with the TUI.
* A subscription is not proof that a client is a TUI; the label is broader
  than the reference's example data.

## Validation

Use make test-system-daemon for observed summaries, subscriber deduplication,
no-grid access, real-socket deadlines/cancellation/recovery, stale completions
and wide/compact details. Run full verify/race/integration and the System
daemon overview flow in QA.md. Lifecycle controls and diagnostic capture remain
tracked in TODO #57 as the next part of the same System implementation.

## Links

* [System reference](../plans/tui-vnext/reference/System.dc.html)
* [Retained global Doctor findings](retain_global_doctor_findings_and_recheck_repairs_179.md)
