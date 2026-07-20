# Fall Back to IPv6 for Guest Loopback Forwarding

Status: Accepted

## Context and Problem Statement

Dynamic forwarding discovers listening sockets from both `/proc/net/tcp` and `/proc/net/tcp6`, including IPv4 and IPv6 loopback. Route dialing nevertheless always targeted `127.0.0.1`. Development servers such as Node/Vite can resolve `localhost` to `::1` and listen only on IPv6, so CodeLima discovered and advertised their port but every request failed with `codelima could not reach the node service`.

## Decision Drivers

* Make every loopback address accepted by discovery reachable through the forwarding tunnel.
* Preserve the existing IPv4 path for services bound to `127.0.0.1` or IPv4 wildcard.
* Avoid changing the route and snapshot schemas merely to describe address family.
* Preserve host-side loopback-only exposure and the existing HTTP/WebSocket boundary.
* Include both attempted guest addresses in daemon diagnostics when neither works.

## Considered Options

* Continue dialing only guest IPv4 loopback.
* Change the discovery model to retain every listening address and family.
* Ask the guest SSH server to resolve `localhost` for each tunnel connection.
* Try guest IPv4 loopback and then IPv6 loopback for each new upstream connection.

## Decision Outcome

Chosen option: "try guest IPv4 loopback and then IPv6 loopback for each new upstream connection", because it closes the mismatch between dual-stack discovery and IPv4-only dialing without expanding persisted or daemon snapshot state. The forwarder first dials `127.0.0.1:{port}` to retain the established fast path. If that fails while the request context remains active, it dials `[::1]:{port}` through the same multiplexed SSH peer. If both attempts fail, their address-specific errors are joined for the daemon log while the HTTP client retains the stable 502 response.

### Positive Consequences

* IPv6-only `localhost` services are reachable through generic and node-specific hostnames.
* Existing IPv4-only and wildcard-bound services behave unchanged.
* Discovery and snapshots remain port-oriented and backward compatible.
* Tunnel logs identify whether IPv4, IPv6, or both guest loopback attempts failed.

### Negative Consequences

* A genuinely unavailable IPv4 service incurs one additional IPv6 dial attempt before returning 502.
* Discovery still does not preserve the exact socket address, so IPv6-only routes always make one expected failed IPv4 attempt first.
* Non-loopback guest bindings remain outside automatic forwarding.

## Pros and Cons of the Options

### Continue dialing only IPv4 loopback

* Good, because it is the smallest and fastest implementation for IPv4 services.
* Bad, because it advertises IPv6 listeners that it cannot reach.
* Bad, because modern `localhost` resolution can select IPv6 without an application-level indication.

### Retain address families during discovery

* Good, because each route could dial the exact discovered family immediately.
* Good, because snapshots could expose more precise listener metadata.
* Bad, because the same port may have several wildcard and loopback sockets that need merge and preference rules.
* Bad, because it expands routing state for a fallback that requires only two bounded attempts.

### Dial guest `localhost`

* Good, because guest name resolution could select the available loopback family.
* Bad, because resolver ordering and fallback behavior would belong to the guest SSH server implementation rather than CodeLima.
* Bad, because diagnostics would no longer identify the concrete addresses attempted.

### Fall back from IPv4 to IPv6 loopback

* Good, because it covers all loopback families accepted by current discovery.
* Good, because it is deterministic and requires no schema change.
* Bad, because IPv6-only services pay for one failed IPv4 dial per new upstream connection.

## Links

* Refines [Daemon dynamic node-hostname forwarding](daemon_dynamic_node_hostname_forwarding_70.md)
* Preserves [Expose the first active node port claim on generic localhost](expose_first_claimant_on_generic_localhost_79.md)
