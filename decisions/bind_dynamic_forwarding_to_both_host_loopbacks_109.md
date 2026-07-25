# Bind dynamic forwarding to both host loopback families

## Context and Problem Statement

Dynamic forwarding listened only on host `127.0.0.1`. The public
`localhost:{port}` and `{node}.localhost:{port}` names may resolve to `::1`
first, so hostname requests could fail before the HTTP Host router was reached
even while direct IPv4 requests worked.

## Decision Drivers

* Every documented hostname route must work with either host loopback family.
* Forwarding must remain inaccessible from non-loopback interfaces.
* IPv4 and IPv6 must not route the same logical port to different owners.
* Hosts without IPv6 support must retain IPv4 forwarding.

## Considered Options

* Bind separate IPv4 and IPv6 loopback listeners as one logical port server.
* Keep the IPv4-only listener and rely on client address fallback.
* Bind a wildcard or implementation-dependent dual-stack listener.

## Decision Outcome

Chosen option: "Bind separate IPv4 and IPv6 loopback listeners as one logical
port server", because it makes address-family behavior explicit without
widening exposure beyond loopback.

Each discovered guest port binds `127.0.0.1:{port}` and `[::1]:{port}` and
serves both listeners through the same HTTP server, route registry, and generic
claimant. Binding is transactional: a real conflict on either family closes
the other listener and leaves the logical port conflicted for reconciliation
to retry. If the host kernel reports that IPv6 or its loopback address is
unavailable, the IPv4 listener remains valid. Daemon snapshots retain the
legacy primary `address` and add the complete `addresses` list.

### Positive Consequences

* `localhost` and `{node}.localhost` work when a resolver or client chooses
  IPv6 loopback.
* Both families always share one claimant and named-route table.
* Existing IPv4-only hosts and snapshot consumers remain compatible.

### Negative Consequences

* Every active guest port normally consumes two host listener sockets.
* A conflicting IPv6-only host listener now blocks CodeLima from claiming the
  corresponding IPv4 port as well.

## Pros and Cons of the Options

### Bind separate IPv4 and IPv6 loopback listeners

* Good, because the exact exposure and failure semantics are portable and
  testable.
* Good, because both listeners can share one `http.Server`.
* Bad, because listener lifecycle and diagnostics become a small collection
  rather than one socket.

### Keep the IPv4-only listener

* Good, because it requires no code change.
* Bad, because client fallback is not guaranteed and the documented hostname
  routes remain unreliable.

### Bind a wildcard or dual-stack listener

* Good, because one socket may accept both families on some systems.
* Bad, because wildcard binding can expose guest services beyond loopback.
* Bad, because IPv4-mapped behavior differs across kernels and Go network
  settings.

## Links

* Extends [Route dynamically discovered node HTTP ports through node.localhost](daemon_dynamic_node_hostname_forwarding_70.md)
* Extends [Expose the first dynamic forwarding claimant on generic localhost](expose_first_claimant_on_generic_localhost_79.md)
