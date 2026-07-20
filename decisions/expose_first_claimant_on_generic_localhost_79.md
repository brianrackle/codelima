# Expose the First Active Node Port Claim on Generic Localhost

Status: Accepted

## Context and Problem Statement

CodeLima's daemon routes dynamically discovered HTTP and WebSocket services only through `{node}.localhost:{port}`. Lima also makes the first guest service for a port available directly on `localhost:{port}`, but relying exclusively on that behavior prevents two VMs using the same port from being addressed independently. CodeLima needs Lima-like generic localhost access without removing its collision-free node hostname routes.

## Decision Drivers

* Make the common single-node development-server path work at `localhost:{port}` and `127.0.0.1:{port}`.
* Preserve deterministic access to every colliding VM through `{node}.localhost:{port}`.
* Avoid persisted leases, a central allocation database, or broadcasting one request to multiple VMs.
* Recover automatically when an external host process temporarily occupies the port or the current node stops listening.
* Keep host exposure loopback-only and retain the existing HTTP/WebSocket transport boundary.

## Considered Options

* Keep only `{node}.localhost:{port}` routing.
* Replace node hostnames with first-successful-bind forwarding like Lima.
* Broadcast generic localhost requests to every VM listening on the port.
* Add an ephemeral generic localhost claimant while retaining all node hostname routes.

## Decision Outcome

Chosen option: "add an ephemeral generic localhost claimant while retaining all node hostname routes," because it combines Lima's convenient default URL with unambiguous access to services sharing a port.

The daemon continues to own one HTTP listener on `127.0.0.1:{port}`. The earliest active route observed for that port becomes the in-memory generic claimant, so requests whose `Host` is `localhost` or the exact address `127.0.0.1` use that node. Requests for `{node}.localhost` bypass the generic claim and select the named node directly. The original `Host` is preserved for the guest service. The claim is not persisted and exists only while the selected guest listener remains active.

When the claimant disappears, the earliest remaining active route takes over during reconciliation. A host bind conflict leaves the listener in `conflicted` state and is retried by the existing one-second reconciliation loop. This retry is intentionally faster than a 30-second recovery interval.

### Positive Consequences

* A single development server is reachable through the familiar `localhost:{port}` and `127.0.0.1:{port}` URLs.
* Multiple VMs can still expose the same port without losing direct named access.
* Claim release and conflict recovery require no persistent ownership records or explicit handoff protocol.
* Daemon snapshots expose the current `default_node` for each host port.

### Negative Consequences

* Generic localhost is inherently ambiguous when multiple nodes listen on the same port; discovery order chooses the default.
* The generic claimant may change after its node stops or after daemon reconstruction.
* Dynamic forwarding remains HTTP/WebSocket-only; raw TCP, UDP, and direct TLS still require other mechanisms.

## Pros and Cons of the Options

### Keep only `{node}.localhost:{port}` routing

* Good, because every URL is explicit and collision-free.
* Good, because it preserves the existing implementation unchanged.
* Bad, because common tools and copied development URLs default to `localhost:{port}`.
* Bad, because it diverges from Lima's convenient default forwarding behavior.

### Replace node hostnames with first-successful-bind forwarding like Lima

* Good, because it closely matches Lima's host-facing behavior.
* Good, because no HTTP hostname router is required for generic access.
* Bad, because only one of several VMs using the same port remains reachable from the host.
* Bad, because it discards a useful existing CodeLima capability.

### Broadcast generic localhost requests to every VM listening on the port

* Good, because it avoids choosing an owner.
* Bad, because one HTTP request cannot safely produce multiple independent responses.
* Bad, because duplicated mutating requests would be incorrect and dangerous.

### Add an ephemeral generic localhost claimant while retaining all node hostname routes

* Good, because generic and explicit access coexist on the same loopback listener.
* Good, because inactive claims disappear naturally with discovered routes.
* Good, because one-second reconciliation provides automatic conflict recovery and claimant transfer.
* Bad, because the daemon must track and report one additional per-port routing choice.

## Links

* Extends [ADR 70](daemon_dynamic_node_hostname_forwarding_70.md)
* Detailed contract: [Dynamic Node-Hostname Forwarding Specification](../plans/DYNAMIC_PORT_FORWARDING_SPEC.md)
