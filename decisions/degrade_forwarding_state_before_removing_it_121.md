# Degrade forwarding state before removing it

Status: Accepted

## Context and Problem Statement

`localhost:{guest-port}` intermittently stopped reaching a plainly running VM
and did not come back. Every mechanism behind that symptom removed routing
state on evidence that only looked authoritative.

Listener discovery was one combined SSH command that also sampled `/proc/stat`,
`/proc/meminfo`, and `df` under a single ten-second budget. Two consecutive
failures of that command deleted the peer and every route for the node, which
closed the port's host listener: `localhost:{guest-port}` went from 502 to
connection refused, which clients cache. A guest under heavy agent load could
miss the budget twice; host sleep and wake did it reliably, because nothing sent
keepalives after the SSH handshake and a proxied dial had no deadline, so a
dead-but-not-closed peer produced hangs rather than errors.

Reconciliation trusted the observation cache alone. Anything other than
`running` closed the peer and dropped the routes on the first pass, with no
grace at all: a `limactl watch` event synthesizes a cache entry without an SSH
config path, and a failed initial list leaves the cache empty but authoritative.
One `NodeList` error returned from the whole reconcile, so a single corrupt
`node.yaml` froze the route table indefinitely while only writing to
`daemon.log`. Reconcile was also sequential, so one unreachable node blocked
every other node's discovery for the length of its timeouts, and once a route
blinked, the generic claim migrated permanently because re-discovery assigned a
fresh discovery time.

## Decision Drivers

* A transient failure must not produce a cached connection refusal.
* Route teardown must follow evidence that a node actually stopped, not the
  failure of a read that happens to be about nodes.
* A dead SSH peer must be detected before a user's request finds it.
* One unhealthy node must not degrade the others.
* The generic `localhost` claim must return to its owner after a blink.
* The daemon must remain the only owner of this state, with no new process.

## Considered Options

* Keep immediate teardown and shorten the reconciliation interval.
* Retain routes on failure and remove them only on an explicit lifecycle event
  from the service layer.
* Give forwarding state graded health, with teardown gated on a confirmed stop
  that combines the observation with the guest's own reachability.

## Decision Outcome

Chosen option: "give forwarding state graded health, with teardown gated on a
confirmed stop", because the daemon already holds direct evidence of whether a
guest is reachable, and that evidence is strictly better than any single cache
read for deciding whether a node is gone.

Forwarding state degrades before it disappears. Each node's peer moves through
four states. It is **healthy** while its listener scan succeeds. It becomes
**suspect** on the first scan or connect failure, or on missed keepalives below
tolerance: routes keep serving, telemetry samples are dropped, and a per-node
exponential backoff starts. Its routes **lapse** once at least two failures span
a fifteen-second grace window: the routes and their host listeners stay in place
and answer HTTP 502, and the transport is retired so the next pass rebuilds it.
Only a **confirmed stop or delete** removes routes and releases listeners.

A stop is confirmed when the node is absent from a *successful* node list, which
is a delete, or when a not-running observation has persisted for fifteen seconds
*and* the guest has stopped answering — no transport at all, or discovery
failing long enough for the routes to have lapsed. A guest that still answers
its scan overrides a not-running observation indefinitely. A failed node list,
a single failed scan, and a stale or watch-synthesized cache entry are each
insufficient on their own.

Listener discovery is split from telemetry sampling into separate guest
commands, so a slow or failing telemetry probe cannot influence routing. Each
peer runs a keepalive monitor that sends `keepalive@openssh.com` every five
seconds and closes the connection after three consecutive misses, which fails
everything blocked on it immediately and lets the next pass reconnect and
re-point the existing routes at the replacement transport. Every proxied dial
carries a five-second bound covering both guest loopback families.

A failed node list retains all membership, keeps working every known node, and
records the condition in the daemon forwarding snapshot rather than returning
silently. Per-node reconcile runs concurrently under a small cap with per-node
exponential backoff that also rate-limits its warning logs.

The generic claim is sticky. A lapsed route keeps its original discovery time,
and a route removed and re-discovered inside a two-minute window has its
original discovery time restored, so earliest-claimant ordering returns the
claim to its previous owner instead of stranding it. The Codex callback port is
excluded from that memory because its newest-listener rule is intentional.

### Positive Consequences

* A transient guest, network, or metadata failure produces a retryable 502 from
  a listener that is still bound, never a connection refusal.
* Host sleep and wake recovers within one keepalive tolerance plus one
  reconciliation instead of requiring a daemon restart.
* A running VM whose cache entry is synthesized, stale, or missing keeps its
  routes and can still acquire new ones.
* A node whose `node.yaml` or `limactl list` output is unreadable no longer
  freezes every other node's routing, and the condition is visible in the
  daemon forwarding snapshot as `node_list_error` and `degraded`.
* One wedged node no longer delays any other node's discovery.
* A blinking service reclaims `localhost:{guest-port}` from a node that took it
  during the blink.

### Negative Consequences

* A real node stop holds its host ports for roughly fifteen to thirty seconds
  before releasing them. Nodes sharing a port are unaffected because one
  listener serves every node on that port, but a host process wanting the port
  immediately after a stop waits.
* Reachability of the guest, rather than a direct runtime query, is what
  confirms a stop. The forwarder does not own a path to `limactl` beyond its
  SSH transport, and "the guest answers" is the property forwarding actually
  depends on. An explicit stop and delete event feed from the service layer
  would allow immediate teardown and remains available as a later refinement.
* A node that is simultaneously unreachable and reported not-running for the
  full window is torn down even if it is in fact running, since nothing
  contradicts both signals. Its listener is already answering 502 by then.
* Discovery now costs two guest commands per node per reconciliation instead of
  one.
* Retained routes hold host ports for as long as a stop cannot be confirmed,
  which is unbounded while node listing stays broken. This is the intended
  behavior and the condition is reported.

## Pros and Cons of the Options

### Keep immediate teardown and shorten the interval

* Good, because state stays trivially consistent with the last observation.
* Bad, because it makes the failure more frequent, not less: the symptom is
  acting on a single unreliable read.
* Bad, because a closed listener is cached by clients while a 502 is not.

### Remove routes only on an explicit lifecycle event

* Good, because a user-initiated stop is unambiguous evidence.
* Bad, because it leaves no teardown path for a VM that stops outside CodeLima,
  crashes, or is removed by `limactl` directly.
* Bad, because it requires an event feed the forwarder does not have today.

### Graded health with confirmed-stop teardown

* Good, because every removal is backed by two independent signals.
* Good, because degradation is observable at every step in the daemon snapshot.
* Good, because the guest's own reachability is the strongest evidence
  available to the daemon and is immune to cache staleness.
* Bad, because teardown is slower than a single-signal rule.
* Bad, because the state machine is larger than the one it replaces.

## Links

* Refines [ADR 70](daemon_dynamic_node_hostname_forwarding_70.md).
* Refines [ADR 109](bind_dynamic_forwarding_to_both_host_loopbacks_109.md).
* Preserves the guest loopback dial order from
  [ADR 83](fallback_to_ipv6_for_guest_loopback_forwarding_83.md).
* Preserves the newest-listener rule from
  [ADR 119](route_codex_login_callback_to_newest_listener_119.md).
* Uses the Lima SSH transport adopted in
  [ADR 92](return_to_lima_as_the_sole_runtime_92.md).
* OpenSSH guidance:
  [keepalive@openssh.com](https://github.com/openssh/openssh-portable/blob/master/PROTOCOL).
