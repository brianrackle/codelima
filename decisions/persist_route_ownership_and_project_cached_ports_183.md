# Persist route ownership and project cached Ports

## Context and Problem Statement

The Ports design needs the actual discovered routes, process names, generic-host
ownership and published TCP state. Reconstructing these from node metadata loses
conflicts, retry state and runtime ownership. Discovery order was also lost when
the daemon restarted, and the design's pin action needs a durable acknowledgement.

## Considered Options

* Reconstruct rows and store pin preferences in each frontend.
* Query guests on every Ports request and keep ownership only in memory.
* Project the daemon's routing cache and persist claim identity and pins there.

## Decision Outcome

Choose the daemon cache and a private versioned routing policy. Ordinary protocol
22 adds query `ports.list` and control `ports.pin`. Pinning does not acquire the
terminal input seat. Both methods use immutable node IDs; the mutation enters the
existing daemon update barrier. The query reads cached inventory, routes, host
listeners and optional process labels without filesystem, SSH or terminal reads.
Configured stopped/held/legacy TCP mappings remain visible alongside discovered
HTTP routes. An unavailable inventory retains the previous observed fleet.

`_daemon/forwarding.json` stores active/recent claims and pins as version 1 JSON,
with a 16 MiB bound, atomic private writes and deterministic ordering. Missing
files start empty. Invalid or unsupported files remain untouched and surface an
error; pinning and live update refuse to discard unreadable preferences. Only
the current owning daemon persists. The update barrier freezes claim identity and flushes before handoff; in-flight
scans cannot change the saved cut, and a failed update resumes reconciliation.
HTTP/TCP requests and cached inspection remain available. Shutdown drains policy
writers before releasing daemon ownership. Candidates
may read before acquiring ownership but cannot write.

A policy mutex serializes route identity changes and writes; the routing mutex
only protects the small in-memory projection and is never held during disk I/O.
Pinning writes before changing the generic owner. A failed write leaves ownership
unchanged. Claim timestamps preserve ordinary first-active and port-1455
newest-listener ordering across daemon restart. A genuinely removed/reappearing
1455 listener gets a new timestamp. Pins survive temporary tunnel loss and
restart, then expire on successful listener absence, failed readiness, confirmed
node stop or deletion. A pinned lapsed HTTP route returns retryable 502 rather
than silently selecting a different node. Startup restores saved endpoints as
unavailable until a successful scan confirms them, so an unreachable pinned
guest cannot redirect its URL during restart. Named routes keep their exact target.

Optional `ss -H -ltnp` output enriches the existing telemetry sample with bounded,
sanitized process names for loopback/wildcard listeners. Missing tools or
permissions leave names unknown; probe failure clears them. This does not affect
the independent listener scan or readiness gate.

Ports uses bounded asynchronous refresh, separate query/mutation generations,
stable row IDs and retained snapshots. Navigation cancels reads. A pin remains a
single mutation when navigation changes or its reply is lost; the UI asks for a
refresh to inspect the outcome and never retries automatically. Compact rows put
addresses on their own line and make complete details scrollable. Keyboard,
mouse and palette actions share the same selection and mutation paths.

Activity identifies physical node/host-port endpoints. A published TCP mapping
supersedes an overlapping conflicted HTTP projection at that endpoint, while the
Ports table retains both protocol-specific rows. This preserves the existing
Activity handoff identity, so handoff 6, session 2 and renderer 5 stay unchanged.

### Positive Consequences

* All frontends share authoritative routing and durable ownership.
* Ports inspection adds no guest or terminal polling and remains responsive.
* Restart, temporary loss and lost replies have explicit, tested semantics.

### Negative Consequences

* Ordinary protocol 22 requires a matching frontend/daemon deployment.
* Policy writes and handoff can delay discovery changes, although requests keep routing.
* Corrupt preferences require recovery before pinning or an in-place update.
* Process visibility depends on guest tools and permissions.

## Pros and Cons of the Options

Frontend preferences avoid daemon storage but disagree across windows and cannot
acknowledge actual routing. Per-request guest inspection is simple to describe
but adds latency and duplicate probes. Cached daemon projection with durable
identity preserves shared ownership at the cost of a versioned file and write
barrier. Two-guest Linux qualification is recorded in the design ledger; final
artboard/native-terminal qualification remains in TODO #57.

## Links

* [Readiness and TCP ownership](gate_application_routes_and_own_published_tcp_in_the_daemon_182.md)
* [Ports design](../plans/tui-vnext/reference/Ports.dc.html)
