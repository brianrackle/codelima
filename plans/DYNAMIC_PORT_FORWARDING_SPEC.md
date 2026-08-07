# Dynamic Localhost and Node-Hostname Forwarding Specification

Status: Shipped contract. Transport is Lima per-instance SSH (ADR 92); routing is ADRs 70/79/83/109/119; resilience is ADR 121 and stability invariant I6.
Purpose: Reproduce Lima-style automatic guest-listener forwarding for HTTP and WebSocket development servers at `http://localhost:{port}` while preserving unambiguous `http://{node}.localhost:{port}` access when nodes share a port, without declaring ports before node creation — and specify what the system does when that forwarding degrades, which is where every reported "`localhost` stopped reaching the VM" failure lived.

This document describes the system as shipped. It is the contract; a change that violates a MUST here is a regression, not a refinement.

## 1. Scope

### 1.1 In scope

* Discovery of unprivileged TCP listeners in every running node, with no predeclared port metadata.
* One host loopback HTTP listener per discovered guest port, shared by every node using that port.
* Host-header routing to a specific node, plus a generic claimant for unqualified `localhost` requests.
* Graded degradation of forwarding state, and the exact evidence required before any of it is removed.

### 1.2 Out of scope

* Transparent raw TCP, UDP, TLS termination, or certificate issuance. A shared loopback TCP connection does not carry the requested hostname, so hostname routing requires an application protocol that does.
* `.local` names, which belong to mDNS.
* Non-loopback host exposure.
* Guest ports below 1024.
* Explicit `HOST:GUEST` published ports, which remain a separate, frozen-at-creation escape hatch for non-HTTP workflows.

## 2. Routing model

Addresses:

```text
http://localhost:{port}          -> the port's generic claimant
http://127.0.0.1:{port}          -> the port's generic claimant
http://[::1]:{port}              -> the port's generic claimant
http://{node}.localhost:{port}   -> exactly that node
```

* `{node}` is the node's `sandbox_name` lowercased. `sandbox_name` is `slugify(slug)` truncated to 128 characters, so for ordinary slugs it is the slug.
* All `*.localhost` names resolve to host loopback by convention. CodeLima MUST NOT require a DNS server, mDNS registration, `/etc/hosts` edit, or any privileged host setup.
* Route identity is `(lowercased sandbox_name, guest port)` (`dynamicRouteKey`). A host listener is identified by guest port alone and serves routes for every node.
* The URL port always equals both the host listener port and the guest port. There is no port translation.
* Node-qualified hosts MUST bypass claimant selection entirely.

Route records (`dynamicForwardingRoute`) carry the node ID, the reverse proxy and its transport, `discoveredAt`, `seenAt`, and the `lapsed`/`lapsedAt` degradation marks of §5. Generic claims are in-memory only and are reconstructed from discovery after any daemon restart.

## 3. Transport

### 3.1 Endpoint resolution

The forwarder holds no runtime credentials of its own. `LimaSSHRuntime.ForwardingSSHConfig(ctx, sandboxName)` resolves one instance's Lima-owned SSH endpoint and MUST be the only source of connection parameters.

* The daemon observation cache is consulted first, but MUST NOT be the last word. A `limactl watch` event synthesizes an observation with no `SSHConfigFile`, and a failed initial list leaves the cache authoritative but empty; either would deny a plainly running node any route.
* When the cached entry is missing, is not `running`, or carries an empty `SSHConfigFile`, resolution falls back to a direct `limactl list`. That fallback is also what makes a "not running" answer trustworthy enough to act on. A failed fallback MUST surface its own error rather than being reported as "not found" or "not running".
* Absent instance → `NotFound`. Present but not running → `PreconditionFailed`. Running with no SSH config path → `MetadataCorruption`.

`parseLimaSSHConfig` validates the Lima-generated config against the trust root `LimaClient.resolvedLimaHome()` (ADR 117) and MUST reject anything that fails these checks:

* config path and identity path, after `EvalSymlinks`, contained within the resolved `LIMA_HOME`;
* both files regular, mode `&0o077 == 0`, owned by the effective uid;
* `Port` in `[1, 65535]`; `User` non-empty;
* `HostName` exactly `127.0.0.1`, `localhost`, or `::1`.

The result is `LimaSSHConfig{User, Host, Port, IdentityFile}`.

### 3.2 Client construction

`limaSSHForwardingPeerFactory.Connect` builds one `golang.org/x/crypto/ssh` client per node:

* `Prepare` is a no-op. CodeLima MUST NOT generate, install, or authorize any forwarding key; Lima owns the instance key pair.
* Read `IdentityFile` (`DependencyUnavailable` on failure) and `ssh.ParsePrivateKey` it (`MetadataCorruption` on failure).
* Dial with `net.Dialer{Timeout: 10s, KeepAlive: 15s}`. The TCP keepalive is the **floor** under §3.3: it lets the kernel tear down a connection whose peer vanished without a FIN, which is the ordinary state after host sleep. It is not sufficient on its own and MUST NOT be relied on for liveness.
* Apply a 10s connection deadline for the handshake, `ssh.NewClientConn` with `PublicKeys(signer)` and a 10s `Timeout`, then **clear the deadline**. The connection is long-lived; a persistent deadline would break idle tunnels. From that point liveness is §3.3 alone.
* `HostKeyCallback` is `InsecureIgnoreHostKey`, admissible only because §3.1 already constrained the endpoint to a Lima-owned config file and an exact loopback address. CodeLima MUST NOT write global `known_hosts` state.
* Connect and handshake failures are `ExternalCommandFailed` carrying the sandbox name and address.

The peer (`forwardingPeer`) exposes exactly `ScanPorts`, `SampleTelemetry`, `Ping`, `DialContext`, and `Close`. Guest commands run over a fresh SSH session per call; since `ssh.Session` has no context-aware API, the call runs on its own goroutine and the session is closed on cancellation so the caller's deadline is real.

### 3.3 Keepalive monitor

Each connected peer runs a `peerHealthMonitor` for as long as the peer exists — not for the length of one reconcile pass, because a dead peer must be found *between* passes.

* Every `forwardingKeepaliveInterval` (5s) it sends the OpenSSH `keepalive@openssh.com` global request with `wantReply` under a `forwardingKeepaliveTimeout` (5s) bound.
* A **refusal reply still proves the transport is alive.** Only a transport error or an expired context counts as a miss. A successful ping resets the miss counter.
* After `forwardingKeepaliveTolerance` (3) consecutive misses it logs one warning, closes the peer, and marks itself dead — in that order, so an observer that sees "not alive" can rely on the connection already being torn down. Closing is what unblocks anything already waiting on the dead connection.
* **Losing a peer MUST NOT remove any route.** The next reconcile pass reconnects and re-points the existing routes at the replacement transport (§5.3).

### 3.4 Proxied dials

Each route's `http.Transport` dials through the node's *current* peer, read under a per-route mutex so a reconnect re-points live routes without the forwarder lock.

* Every dial is bounded by `forwardingDialTimeout` (5s). A dead-but-not-closed peer MUST fail the request quickly rather than hold it open for as long as the client waits.
* `dialGuestLoopback` tries guest `127.0.0.1:{port}` and then `[::1]:{port}` (ADR 83), joining both address-specific errors for the log if neither works. It stops early if the request context is already done.
* The reverse proxy rewrites the upstream URL to `http://guest` while setting `request.Out.Host = request.In.Host`, so the guest service observes the original `Host` (`{node}.localhost:{port}` or `localhost:{port}`).
* `ForceAttemptHTTP2` is false; idle-connection settings are `MaxIdleConns 100`, `MaxIdleConnsPerHost 10`, `IdleConnTimeout 90s`, `ExpectContinueTimeout 1s`. HTTP connection reuse and WebSocket/`Upgrade` passthrough MUST remain enabled.
* A proxy error yields HTTP 502 with body `codelima could not reach the node service`, logged with node, port, and error.

## 4. Discovery

Reconciliation runs every `forwardingPollInterval` (1s), starting immediately at `Start`.

### 4.1 Two guest commands

Listener discovery and telemetry are **separate guest commands under separate deadlines**. Routing follows the scan alone.

| Concern | Command | Timeout |
| --- | --- | --- |
| Listeners | `guestPortScanCommand` = `cat /proc/net/tcp /proc/net/tcp6` | `forwardingScanTimeout` = 10s |
| Telemetry | `guestTelemetryCommand` — `head -n 1 /proc/stat`, an `awk` over `/proc/meminfo` emitting `codelima-memory {total} {available}`, and `df -Pk /` emitting `codelima-disk {total} {used}` | `forwardingTelemetryTimeout` = 10s |

A telemetry failure MUST NOT influence peers, routes, or node health. It clears that node's cached CPU/memory/disk samples and returns; the routes keep serving. A guest too busy to report CPU keeps its routes.

### 4.2 Parser contract

`parseProcNetTCP` MUST:

* accept only rows in state `0A` (`LISTEN`);
* accept only wildcard and loopback local addresses (`00000000`, `0100007F`, and the IPv6 all-zero and `::1` word encodings) — anything else is an interface-bound listener and is deliberately skipped (§9);
* deduplicate across `tcp` and `tcp6`;
* reject ports below 1024;
* skip malformed rows without discarding valid rows;
* return a sorted set.

### 4.3 Per-node fan-out and backoff

* Nodes are reconciled concurrently by a worker pool of `min(forwardingConcurrency=4, len(targets))`. One booting, wedged, or sleeping node MUST NOT delay any other node's discovery.
* Every connect or scan failure starts or doubles a per-node backoff: `forwardingBackoffInitial` 1s, doubling to `forwardingBackoffMaximum` 30s. A node inside its backoff window is skipped for that pass.
* Warnings are rate-limited per node to one per `forwardingWarnInterval` (30s), and the same interval rate-limits the inventory-failure warning. Retry spam MUST NOT be the observability mechanism.

### 4.4 Applying a successful scan

`applyScanResult` is the authoritative write:

* the node is marked healthy (failure count, first-failure time, lapse mark, backoff, and last error all cleared);
* surviving ports refresh `seenAt`, adopt the current peer, and un-lapse if lapsed (logged as `route restored`);
* new ports gain a route whose `discoveredAt` comes from §6.3;
* ports the guest no longer lists are removed.

That last bullet is the **only** route removal outside a confirmed stop or delete: a successful scan is positive proof the guest stopped listening. A failed scan removes nothing.

## 5. Resilience contract (invariant I6)

**Forwarding state degrades before it disappears.** Routes are torn down only on a confirmed node stop or delete — never inferred from a single failed scan, a failed `NodeList`, or a stale cache read. While a port's routes lapse, the host listener stays bound and serves a retryable 502 rather than closing, because a closed listener surfaces as connection refused, which clients cache.

### 5.1 Node state machine

`forwardingNodeState` moves through:

| State | Entered when | Routes | Transport |
| --- | --- | --- | --- |
| `healthy` | the last listener scan succeeded | serving | connected |
| `suspect` | a connect or scan failure has been recorded and none has succeeded since | still serving; telemetry dropped immediately | connected, backoff started |
| `lapsed` | at least 2 failures **and** ≥ `forwardingRouteGrace` (15s) since the first failure | present, listener still bound, every request answered 502 | retired; next pass rebuilds it |
| `retired` | confirmed stop or delete | removed, listener released | closed |

The daemon snapshot's `health` field reports, in precedence order, `lapsed`, `suspect` (failure count above zero), `connecting` (no peer yet, or the peer was just retired without a recorded failure), or `healthy`. `retired` has no snapshot value because the state record is deleted.

Keepalive misses are tracked by the monitor, not by this state machine: isolated misses below tolerance leave `health` unchanged, and a peer closed for keepalive loss shows `connecting` until the next pass reconnects or records a connect failure. The monitor's last failure is still visible — the snapshot's `last_error` falls back to it when the node has no recorded discovery error.

Stale telemetry is worse than none: CPU, memory, and disk samples are dropped on the *first* failure, long before routes are affected.

### 5.2 Confirmed stop

`confirmedStopped(now, grace)` MUST require **both**:

1. a not-running observation that has persisted continuously for `forwardingStopGrace` (15s) — any running observation resets the clock; and
2. the guest has stopped answering — either there is no transport at all (`peer == nil`), or discovery has been failing long enough that the routes already lapsed (`routesLapsed`).

A guest that still answers its scan is running, whatever a synthesized or stale cache entry says, and holds its routes indefinitely. Absence from a *successful* `NodeList` is a delete and retires the node immediately; that is the one single-signal teardown, and it is backed by a read that succeeded.

Peer and monitor teardown MUST happen outside the forwarder lock (`retiredPeer`), because closing an SSH connection and joining a ping goroutine can block while `ServeHTTP` holds the read lock.

### 5.3 Peer replacement

When a peer is missing or its monitor reports dead, the pass reconnects and `adoptPeer`:

* starts a fresh monitor;
* installs the new peer on the node state, retiring the replaced pair;
* re-points **every** route with that node ID at the new peer and closes its idle connections, because a pooled connection belongs to the dead transport and would send the next request back into it.

This is how a lapsed route repairs itself: the routes never left, so recovery is a transport swap plus the next successful scan.

### 5.4 `NodeList` failure

A failed inventory read is not evidence that any node stopped. `retainMembership` MUST:

* tear down nothing;
* record `inventoryErr` and `inventoryErrSince`, and also set the forwarder's `lastErr`/`lastErrAt`;
* keep **every** already-known node in the work set — including one whose peer was just retired, since without an inventory read this is the only path back to a working transport;
* warn at most once per 30s;
* surface the condition in the daemon forwarding snapshot as `node_list_error`, `node_list_error_since`, and `degraded: true`.

Reconciliation MUST NOT return early on a `NodeList` error. Retained routes hold their host ports for as long as a stop cannot be confirmed, which is unbounded while listing stays broken; that is intended, and the condition is reported.

## 6. Generic claimant selection

### 6.1 Rule

`preferredClaimantLocked(port)` selects the node that answers unqualified requests on a port.

* Ordinary ports: the route with the **earliest** `discoveredAt` wins.
* `codexLoginCallbackPort` (1455), Codex CLI's browser-auth callback: the **newest** `discoveredAt` wins (ADR 119), re-evaluated every reconciliation, so starting `codex login` in another node moves the callback there.
* Ties break deterministically on lowercased node name (`route.key.node < selected.key.node`).

### 6.2 Lapsed routes

`selectClaimantLocked` excludes lapsed routes on the first attempt. If — and only if — *every* route on the port is lapsed, it selects again including them, so the generic host still resolves to a node and answers 502 rather than 421 `unknown codelima node`. A degraded port MUST look degraded, not nonexistent.

### 6.3 Claim memory

A route's `discoveredAt` is sticky so a blink does not permanently hand `localhost:{port}` to another node:

* a lapsed route keeps its original `discoveredAt` (it is never removed, so nothing to restore);
* a removed route's `discoveredAt` is remembered in `routeDiscoveryMemory` and restored if the same `(node, port)` is re-discovered within `forwardingClaimMemory` (2 minutes); memory older than that window is pruned on each successful membership sync;
* port 1455 is excluded from claim memory — its newest-listener rule is the point.

The claimant is recomputed on every `reconcileServers` pass and a change is logged as `dynamic forwarding generic route claimed`.

## 7. Host listeners and request handling

### 7.1 Listener lifecycle

* The set of wanted ports is every port with a route, **including lapsed routes**. Keeping the listener bound is what turns a transient failure into a retryable 502.
* Binding a port is transactional (ADR 109): `127.0.0.1:{port}` on `tcp4` is required; `[::1]:{port}` on `tcp6` is attempted next. If the IPv6 bind fails with `EAFNOSUPPORT`, `EPROTONOSUPPORT`, or `EADDRNOTAVAIL`, the IPv4 listener alone is valid. Any other IPv6 failure closes the IPv4 listener and marks the logical port `conflicted`.
* Both listeners share one `http.Server` (`ReadHeaderTimeout` 10s), one route table, and one claimant. IPv4 and IPv6 MUST NOT route the same logical port to different owners.
* A `conflicted` port stays in the table with its error and is retried by the 1s loop. A conflict on one port MUST NOT disturb any other port.
* A port with no remaining routes has its server closed and its entry deleted. Listeners MUST bind loopback only; no wildcard or non-loopback bind is permitted, and no request header may widen the bind.

### 7.2 Host acceptance and status codes

`forwardingHostname` normalizes the request `Host`: it splits `host:port` (rejecting a port outside `[1, 65535]`), accepts a bracketed IPv6 literal with no port, rejects any other colon-bearing host, lowercases, and trims trailing dots.

* `{label}.localhost` with a non-empty, dot-free label selects that node directly.
* Otherwise `isGenericForwardingHost` accepts `localhost` **and every loopback IP literal** — `127.0.0.1`, other `127.0.0.0/8` addresses, and `::1`, including the bracketed `[::1]` form the forwarder itself binds — and uses the port's `default_node`.
* Anything else MUST return **HTTP 421** with exactly:

  ```text
  request host must be localhost, a loopback address, or {node}.localhost
  ```

Status codes:

| Condition | Status | Body |
| --- | --- | --- |
| Unusable host, or generic host with no claimant | 421 | `request host must be localhost, a loopback address, or {node}.localhost` |
| Named host that is not a known node | 421 | `unknown codelima node` |
| Known node with no route on this port | 404 | `the node is not listening on this port` |
| Route lapsed | 502 | `codelima is reconnecting to the node service` |
| Tunnel/dial/proxy failure | 502 | `codelima could not reach the node service` |

"Known" is the set of lowercased sandbox names in the forwarder's node map, rebuilt only on a *successful* membership sync; a failed `NodeList` therefore never turns a known node into an unknown one.

Listener discovery, membership sync, and request routing run concurrently and MUST remain race-free.

## 8. Observability

`codelima daemon snapshot` returns the daemon state map; its `forwarding` key is the forwarder snapshot and is the primary incident-diagnosis surface. When the runtime does not implement `LimaSSHRuntime` the value is `{"enabled": false}`.

Top level: `enabled`, `authorized` (the factory `Prepare` latch), `routes`, `ports`, `nodes`, `peers` (count of connected peers), `last_error`, `last_error_at` (omitted when unset), `last_poll_at`, `degraded` (true when the inventory read is failing or any route is lapsed), and, while the inventory read is failing, `node_list_error` and `node_list_error_since`.

* `routes[]`: `node`, `port`, `url`, `state` (`serving` | `lapsed`), `discovered_at`, `last_seen_at`, and `lapsed_at` when lapsed. Sorted by URL.
* `ports[]`: `port`, `address` (the first listener, retained for compatibility), `addresses` (all listeners), `default_node`, `status` (`serving` | `conflicted`), `error`. Sorted by port.
* `nodes[]`: `node`, `connected`, `health` (§5.1), `failures`, and — when set — `last_healthy_at`, `not_running_since`, `retry_after` (the backoff deadline), and `last_error` (the node's last discovery error, falling back to the keepalive monitor's last failure). Sorted by node.

Diagnosis map: `node_list_error` means inventory is broken and membership is frozen but retained; a port `status` of `conflicted` with an `error` means a host process owns the port; a route `state` of `lapsed` with the node's `health` and `last_error` means the guest or its transport is failing; `default_node` identifies the current generic claimant.

The daemon log MUST record peer connect/retire/release, route add/remove/restore/lapse, listener bind, generic-claim changes, and conflicts — and MUST NOT log per-request successes. Request failures carry node, port, and error.

Live telemetry (`addNodeUsage`) is merged into `node.list` responses only: guest CPU percent, memory used/total, disk used/total, and their sample times. It MUST NOT be persisted to `node.yaml`, and it is cleared on the first discovery failure or when a node is retired.

## 9. Documented limits

These are real, permanent properties of the design and MUST be documented where users meet them, not treated as bugs.

1. **Root-network-namespace `/proc` scan.** Discovery reads `/proc/net/tcp` and `/proc/net/tcp6` over the SSH session, which sees the guest's root network namespace only. A server bound to a specific interface address (`--host 192.168.x.x`) is filtered out by §4.2, and a server inside a container network namespace is invisible unless something publishes it into the root namespace — with Docker's userland proxy disabled, container ports do not appear.
2. **Ports below 1024 are excluded.** Privileged guest ports are never forwarded.
3. **HTTP and WebSocket only.** Non-HTTP TCP — TLS dev servers, databases, prior-knowledge h2c/gRPC — completes the host TCP handshake and then fails at the HTTP proxy, which always speaks plaintext HTTP/1.1 upstream. Use explicit published ports for those.
4. **Forwarding exists only while the daemon is healthy.** Any daemon hang or crash takes forwarding with it.
5. **Live update has a deliberate dead window.** Forwarding sockets are not transferred. The successor starts its forwarder before the predecessor exits, so its binds are `conflicted` and retried by the 1s loop until the predecessor's `Close` releases them. Live PTYs are governed separately by ADR 67.
6. **A confirmed stop takes roughly 15–30 seconds** to release host ports (§5.2). Nodes sharing the port are unaffected, but a host process wanting the port immediately after a stop waits.
7. **Generic `localhost` is ambiguous by nature** when several nodes listen on one port. Discovery order picks the owner; `{node}.localhost` is always unambiguous.
8. **A node that is simultaneously unreachable and reported not-running** for the full window is torn down even if it is in fact running — nothing contradicts both signals. Its listener has been answering 502 by then.

## 10. Lifecycle and recovery

* The daemon starts forwarding during startup, after session restore and Lima observation and before the control server runs. A forwarding start failure fails daemon startup (and, on the live-update path, rolls the import back).
* Node start is reflected within one poll interval. Node stop and delete are handled by §5.2.
* Daemon graceful stop cancels the loop, waits for it, closes every port server, then stops each monitor and closes each peer. `Close` MUST leave no route, server, peer, or telemetry entry behind.
* Daemon crash relies on process teardown; the next daemon reconstructs all state from discovery, including generic claims.

## 11. Security

* Bind `127.0.0.1` and `::1` only.
* Node hostnames are validated against current CodeLima node metadata, never against arbitrary `Host` input.
* Guest connections use only Lima-owned, `LIMA_HOME`-contained, correctly permissioned, loopback-scoped SSH material (§3.1). CodeLima generates no keys and authorizes nothing.
* No privileged port forwarding.
* Proxied services are local developer services. CodeLima adds no authentication, no cross-origin policy, does not follow redirects, and does not inspect or modify payloads beyond reverse-proxy requirements. Generic forwarding is not an authentication boundary.

## 12. Test and validation matrix

Automated coverage MUST include:

* `/proc/net/tcp`/`tcp6` parsing: state filter, address filter, dedup, privileged-port exclusion, malformed rows; telemetry parsing including invalid values and CPU counter deltas;
* hostname normalization and rejection, generic-host acceptance including the IPv6 loopback literal, and the 421/404/502 status table;
* two nodes sharing one port routed by node host; generic claim to the earliest active route; the port-1455 newest-listener rule; upgrade/WebSocket passthrough; IPv4→IPv6 guest loopback fallback;
* listener lifecycle over both host families, IPv4 rollback on IPv6 conflict, bind-conflict recovery, and transport-preparation retry that does not block the daemon;
* **resilience (I6):** keepalive loss closes the peer and the next pass re-points routes; routes survive a transient scan failure; lapsed routes answer 502 without closing the listener; routes are removed only on a confirmed stop; peers and routes survive a `NodeList` failure; the generic claim returns after a lapse and after a removal/re-discovery inside the claim-memory window; per-node concurrency; per-node backoff; telemetry failure leaves routes serving; the proxied dial is bounded;
* race-detector coverage of concurrent reconciliation and request handling.

Manual QA on native macOS and Linux MUST verify:

1. Two nodes with the same unplanned HTTP port; generic `localhost:{port}`, `127.0.0.1:{port}`, and `[::1]:{port}` reach the claimant, and each node is reachable by `{node}.localhost:{port}`.
2. Guest-loopback-only and IPv6-only guest servers are reachable; WebSocket hot reload works.
3. Stopping the claimant transfers the generic claim; restarting it inside two minutes returns the claim.
4. Host sleep/wake recovers within one keepalive tolerance plus one reconciliation, with no daemon restart.
5. During a transient guest stall, `localhost:{port}` returns 502 and never connection-refused, and recovers on its own.
6. A deliberately corrupted `node.yaml` leaves existing routes serving and shows `node_list_error` in `daemon snapshot`.
7. Node stop/delete and daemon stop/restart remove and reconstruct routes; bind conflicts are reported without affecting other ports; listeners bind only `127.0.0.1` and `::1`.

## 13. Superseded content

Earlier revisions of this document specified a Microsandbox transport. All of it is gone:

| Retired | Replacement |
| --- | --- |
| `msb ssh serve {sandbox} --stdio` over a hidden CodeLima helper process | Direct Go SSH client to the Lima instance endpoint (ADR 92) |
| Microsandbox Go SDK pinned to `0.6.6` | No SDK; `golang.org/x/crypto/ssh` over TCP |
| Per-`CODELIMA_HOME` Ed25519 key under `_daemon/forwarding/`, authorized per sandbox | Lima-owned per-instance key and `ssh.config`, validated under `LIMA_HOME` (ADRs 92, 117) |
| `SandboxSSHRuntime` seam | `LimaSSHRuntime.ForwardingSSHConfig` |
| One combined discovery command that also sampled telemetry; two failures deleted the peer and every route | Split scan/telemetry commands; graded degradation with teardown only on a confirmed stop (ADR 121) |
| `default_ports` migration away from the guessed `3000/5173/8000/8080` list | Retained in `LoadConfig`, but a runtime concern rather than a forwarding contract |

Routing (ADRs 70, 79, 83, 109, 119) survived the transport change unchanged and is specified in §2, §6, and §7.
