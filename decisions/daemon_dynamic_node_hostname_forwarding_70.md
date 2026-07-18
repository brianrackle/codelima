# Route dynamically discovered node HTTP ports through node.localhost

Status: Accepted

## Context and Problem Statement

Lima automatically discovers guest listeners and creates host forwards. Microsandbox requires immutable published ports at sandbox creation, so CodeLima guessed four common ports. Those guesses miss arbitrary development servers and collide when multiple nodes use the same host port. CodeLima needs a dynamic model without privileged DNS or sandbox recreation.

## Decision Drivers

* Preserve the Lima workflow of starting an unplanned development server and immediately reaching it from the host.
* Address nodes by their names and allow multiple nodes to expose the same guest port.
* Avoid mDNS, `/etc/hosts`, host-wide DNS configuration, and non-loopback exposure.
* Reuse the per-home daemon and Microsandbox's supported SSH forwarding seam.

## Considered Options

* Continue static guessed ports.
* Recreate sandboxes when new listeners appear.
* Give nodes unique loopback IPs and install a host DNS resolver.
* Route HTTP/WebSocket requests by `{node}.localhost` and tunnel them over Microsandbox SSH.

## Decision Outcome

Chosen option: "daemon-owned `{node}.localhost` HTTP/WebSocket routing over SSH." The daemon discovers unprivileged guest TCP listeners, binds one host loopback HTTP server per port, routes by the HTTP `Host` header, and uses Microsandbox SSH `direct-tcpip` connections to guest loopback. Fresh homes no longer guess ports.

Static published ports remain an explicit advanced escape hatch. Raw TCP, UDP, and local TLS termination are deferred because a shared loopback TCP connection does not carry the original DNS hostname and Microsandbox SSH forwarding is TCP-only.

### Positive Consequences

* Unplanned development servers become reachable without node recreation.
* Multiple nodes can use the same guest port through distinct origins.
* Guest-loopback-only servers work.
* No privileged DNS or host configuration is required.
* The daemon can recover forwarding state from running sandboxes.

### Negative Consequences

* V1 is HTTP/WebSocket-specific rather than fully transparent TCP/UDP forwarding.
* The daemon gains SSH key, peer, discovery, listener, and proxy lifecycle responsibilities.
* Static host mappings may conflict with dynamic listeners until existing nodes are recreated or their mappings are changed.
* Live daemon update briefly reconstructs routes rather than transferring forwarding sockets.

## Links

* Detailed contract: [Dynamic Node-Hostname Forwarding Specification](../plans/DYNAMIC_PORT_FORWARDING_SPEC.md)
* Replaces the guessed-port convenience described by [ADR 55](replace_lima_with_microsandbox_as_sole_runtime_55.md)
* Extends daemon ownership from [ADR 64](daemon_owned_terminal_runtimes_64.md)
