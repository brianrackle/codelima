# Dynamic Localhost and Node-Hostname Forwarding Specification

Status: Routing behavior retained; Microsandbox transport superseded by ADR 92's persistent Lima SSH transport
Purpose: Reproduce Lima-style automatic guest-listener forwarding for HTTP and WebSocket development servers at `http://localhost:{port}` and `http://127.0.0.1:{port}` while preserving explicit `http://{node}.localhost:{port}` access when nodes share a port, without declaring ports before sandbox creation.

## 1. Problem Statement

Microsandbox published ports are immutable boot configuration. CodeLima currently guesses common ports and passes them during sandbox creation, which does not reproduce Lima's listener-driven behavior, creates collisions across nodes, and cannot expose an unanticipated development server. CodeLima's daemon already owns per-home background lifecycle and is the correct owner for dynamic discovery and host listeners.

## 2. Assumptions

* The required public surface is HTTP and WebSocket traffic addressed as `localhost:{guest-port}`, `127.0.0.1:{guest-port}`, or `{node}.localhost:{guest-port}`.
* `{node}` is the node's stable `sandbox_name`, derived from its user-facing slug; ordinary node slugs such as `test-node` are unchanged.
* All `*.localhost` names resolve to host loopback. No DNS server, mDNS registration, `/etc/hosts` edit, or privileged setup is required.
* Microsandbox `0.6.6` SSH `direct-tcpip` support is the runtime transport into guest loopback.
* Arbitrary raw TCP cannot be routed by hostname on a shared loopback IP because the original hostname is absent from a TCP connection. UDP is not supported by the Microsandbox SSH forwarding seam.

## 3. Goals and Non-Goals

### 3.1 Goals

* Discover unprivileged TCP listeners in every running CodeLima node without predeclared port metadata.
* Make the earliest active listener on a port available at `http://localhost:{port}` and `http://127.0.0.1:{port}`, and transfer that ephemeral claim when it disappears.
* Make each discovered listener available at `http://{node}.localhost:{port}`.
* Allow multiple nodes to expose the same guest port through one host listener by routing on the HTTP `Host` header.
* Reach services bound to guest `127.0.0.1` as well as wildcard guest addresses.
* Start and remove routes as listeners and nodes change, and recover after daemon restart.
* Bind only host loopback and expose route state and failures through daemon status/logging.
* Preserve explicit Microsandbox `HOST:GUEST` publishing as an advanced escape hatch.

### 3.2 Non-Goals

* Transparent arbitrary-TCP, UDP, TLS certificate issuance, or TLS termination in v1.
* Routing `.local` names; `.local` is reserved for mDNS.
* Mutating a running sandbox's Microsandbox published-port configuration.
* Forwarding privileged guest ports below 1024 in v1.
* Making node services reachable from non-loopback host interfaces.

## 4. System Overview

### 4.1 Components

* `DynamicForwarder`: daemon-owned reconciliation loop and route registry.
* `SandboxSSHRuntime`: narrow Microsandbox seam that authorizes the per-home key and opens `msb ssh serve {sandbox} --stdio` transports.
* `SSHPeer`: one multiplexed SSH client per running sandbox, used for listener discovery and `direct-tcpip` connections.
* `PortHTTPServer`: one logical HTTP server with host
  `127.0.0.1:{port}` and `[::1]:{port}` listeners per discovered guest port
  across all nodes.
* `NodePortRoute`: mapping from normalized node name plus guest port to an SSH peer.

### 4.2 External Dependencies

* Microsandbox Go SDK exactly `0.6.6`.
* `golang.org/x/crypto/ssh` for the SSH protocol over Microsandbox stdio.
* A hidden CodeLima SDK SSH helper process, with no `msb`, host `ssh`, `ssh-keygen`, DNS daemon, or privileged helper invocation.

## 5. Domain Model and Invariants

`NodePortRoute` contains:

* `NodeID`: stable CodeLima node ID.
* `NodeName`: stable `sandbox_name`, lowercased for hostname matching.
* `GuestPort`: integer in `[1024, 65535]`.
* `DiscoveredAt` and `LastSeenAt`.

Invariants:

1. Route identity is `(NodeName, GuestPort)`.
2. A host listener is unique by `GuestPort` and may serve routes for many nodes.
3. Host listeners bind `127.0.0.1` and `::1` only. A host without IPv6 support
   may retain only the IPv4 listener; no listener may bind a wildcard or
   non-loopback address.
4. The route with the earliest `DiscoveredAt` for a port is its generic claimant while active; ties use normalized node name for deterministic recovery.
5. Requests whose `Host` is `localhost`, case-insensitively and with an optional trailing dot, or the exact IPv4 address `127.0.0.1` use the generic claimant.
6. Requests whose `Host` is exactly `{NodeName}.localhost` use that named route regardless of the generic claimant.
7. Generic claims are in-memory only and are reconstructed after daemon restart.
8. The URL port must equal the listener and route guest port.
9. Unknown hosts return HTTP 421; known nodes without that listener return HTTP 404; tunnel failures return HTTP 502.
10. Listener discovery and request routing may run concurrently without data races.

## 6. Discovery Contract

For each running node, the daemon opens or reuses an SSH peer and executes:

```sh
cat /proc/net/tcp /proc/net/tcp6
```

The host parser must:

* accept only state `0A` (`LISTEN`);
* accept wildcard and loopback local addresses;
* deduplicate TCP and TCP6 results;
* ignore ports below 1024;
* reject malformed rows without discarding valid rows;
* replace the node's observed route set atomically after a successful scan.

A failed scan retains the previous route set for one poll interval. Repeated failures close and recreate the SSH peer, then remove stale routes after the peer is no longer usable. The initial poll interval is one second.

## 7. SSH Transport and Key Contract

The daemon creates one Ed25519 key pair per `CODELIMA_HOME` under `_daemon/forwarding/` with directory mode `0700`, private key mode `0600`, and public key mode `0644`. Creation is atomic and idempotent.

Before serving routes, the runtime prepares the per-sandbox SDK SSH server with the public key. It then launches the current CodeLima executable in a hidden helper mode; that helper connects with the SDK and serves the sandbox's SSH connection over stdin/stdout. The parent performs an SSH client handshake as `root` and multiplexes discovery sessions and TCP channels over that peer.

Host-key verification is intentionally connection-local: Microsandbox generates a per-sandbox host key, while the transport itself is a child process started for an already resolved sandbox identity. CodeLima must not write global `known_hosts` state.

Closing the daemon, stopping/deleting a node, or losing the Microsandbox transport must close the SSH client and child process. Authorization is additive in Microsandbox's user-private state; rotating/removing stale authorized keys is future work and must be recorded in `TODO.md`.

## 8. HTTP and WebSocket Routing Contract

Each `PortHTTPServer` uses Go's reverse proxy with a custom transport whose dialer opens `SSHPeer.Dial("tcp", "127.0.0.1:{GuestPort}")`. It must preserve the original `Host` header so node development servers see `{node}.localhost:{port}`. HTTP connection reuse and WebSocket upgrades must remain enabled.

Examples:

```text
http://localhost:8080            -> earliest active claimant guest 127.0.0.1:8080
http://127.0.0.1:8080            -> earliest active claimant guest 127.0.0.1:8080
http://test-node.localhost:8080  -> test-node guest 127.0.0.1:8080
http://api-node.localhost:8080   -> api-node guest 127.0.0.1:8080
```

The host listeners start when the first route for a port appears and close
after the final route disappears. The IPv4 and IPv6 binds form one transaction:
a conflict on either family closes the other listener so the same logical port
cannot have split ownership. The first active route claims generic `localhost`;
named hostnames always bypass that claim. If the claimant disappears, the
earliest remaining active route takes over during reconciliation. If binding
fails because another host process or a static publication owns the port, the
daemon records a conflict and retries every one-second reconciliation without
disrupting other ports.

## 9. Lifecycle and Recovery

States per node peer: `disconnected -> connecting -> ready -> failed -> disconnected`.

States per host port: `absent -> binding -> serving | conflicted -> absent`.

Rules:

* Daemon startup begins reconciliation after the control sockets are ready.
* Node start is eventually reflected within one poll interval.
* Node stop/delete closes its peer and removes its routes.
* Removing a generic claimant transfers generic routing to the earliest remaining active route within one poll interval.
* Daemon graceful stop closes HTTP listeners before SSH peers.
* Daemon crash relies on process teardown to close listeners and stdio transports; the next daemon reconstructs all state.
* Live daemon update does not transfer forwarding sockets in v1. The importer retries while the old daemon owns them, and routes recover after commit closes the old daemon. Live PTYs remain governed by ADR 67.

## 10. Defaults and Compatibility

Fresh homes must default `default_ports` to an empty list. The legacy guessed defaults `3000`, `5173`, `8000`, and `8080` are no longer necessary.

An untouched global config containing exactly the legacy default list may migrate to empty during a mutating readiness pass. Project and node port metadata remains authoritative and is never silently rewritten. Existing static mappings continue to work, but may conflict with the dynamic host listener for the same host port; the conflict is observable and actionable.

## 11. Logging, Status, and Observability

The daemon log must record peer connect/disconnect, route add/remove, listener bind/close, and conflicts without per-request success noise. Request failures include node, port, and category.

`daemon snapshot` must include dynamic forwarding state:

* active routes with URL;
* host ports, generic `default_node` (empty until a bind succeeds), and listener status;
* node peer status;
* last error and observation timestamp.

## 12. Security and Operational Safety

* Bind only `127.0.0.1` and `::1`; never trust request headers to widen the
  bind address.
* Validate node hostnames against current CodeLima node metadata, not arbitrary `Host` input.
* Do not forward privileged ports in v1.
* Keep private keys and daemon forwarding state user-private.
* Treat all proxied services as local developer services; CodeLima adds no authentication or cross-origin policy.
* Do not follow redirects or inspect/modify application payloads beyond reverse-proxy requirements.

## 13. Test and Validation Matrix

Automated tests must cover:

* `/proc/net/tcp` and TCP6 parsing, malformed rows, address filtering, deduplication, and privileged-port exclusion;
* hostname normalization and rejection;
* two nodes sharing one port and routing to different upstreams;
* generic `localhost` and `127.0.0.1` choosing the earliest active route while named hosts remain explicit;
* generic claimant transfer after the current claimant disappears;
* unknown host, missing route, and tunnel failure status codes;
* WebSocket/HTTP upgrade passthrough;
* route add/remove and host listener lifecycle;
* generic and node-qualified Host routing over both host loopback families;
* bind conflict followed by recovery;
* node stop and daemon close cleanup;
* key generation permissions and idempotency;
* Microsandbox SSH command construction and error propagation;
* config migration only for the exact untouched legacy default list;
* race detector coverage for concurrent reconciliation and requests.

Manual QA must verify on native macOS and Linux:

1. Start unplanned HTTP ports in two nodes with the same port.
2. Verify generic `localhost:{port}` and `127.0.0.1:{port}` reach the first claimant.
3. Reach each through `{node}.localhost:{port}`.
4. Stop the first claimant and verify generic localhost transfers to the second within the one-second retry loop.
5. Verify guest-loopback binding works.
6. Verify WebSocket hot reload.
7. Stop/restart nodes and the daemon and observe route removal/recovery.
8. Verify host bind conflicts are reported without affecting unrelated ports.
9. Confirm forwarding listeners bind only `127.0.0.1` and `::1`.

## 14. Implementation Checklist

* [x] Add ADR and dependency decision.
* [x] Add `SandboxSSHRuntime` seam and ExecMSB implementation.
* [x] Generate and authorize the per-home Ed25519 key.
* [x] Implement SSH peer lifecycle and `/proc` parser.
* [x] Implement route registry and HTTP/WebSocket proxy.
* [x] Add ephemeral generic localhost claims, one-second retry, and claimant transfer without removing named routes.
* [x] Wire start/stop and live-update recovery into `daemonHost`.
* [x] Add forwarding state to daemon snapshot.
* [x] Remove guessed defaults for fresh homes and migrate untouched global defaults.
* [x] Update README, BUILD, QA, PATTERNS, ROADMAP, TODO, and migration progress.
* [x] Run automated verification and the available nested-Linux manual forwarding matrix. Native macOS and Linux release qualification remains tracked in `TODO.md`.
