# Use framed Unix streams for portable daemon handoff

Status: Accepted

## Context and Problem Statement

Live daemon update used an `AF_UNIX` `SOCK_SEQPACKET` (`unixpacket`) socket to
preserve JSON message boundaries while passing PTY descriptors. Darwin rejects
that socket type with `protocol not supported`, so every macOS live update
failed before the replacement process could start. How should the handoff keep
message boundaries and `SCM_RIGHTS` descriptor passing on both macOS and Linux?

## Decision Drivers

* The production transport must work on Darwin and Linux.
* PTY descriptors must continue to move through authenticated `SCM_RIGHTS`.
* Stream fragmentation and coalescing must not corrupt message boundaries.
* A new importer should retain compatibility with the previous Linux
  `unixpacket` handoff during the upgrade boundary.
* A running legacy Darwin daemon cannot be patched in place, so the update
  client needs a safe, narrow recovery path.

## Considered Options

* Keep `unixpacket` and declare macOS live update unsupported.
* Use an unframed Unix stream and assume one read per write.
* Use a length-prefixed Unix stream with a legacy importer fallback.

## Decision Outcome

Chosen option: "use a length-prefixed Unix stream with a legacy importer
fallback", because Unix stream sockets support peer credentials and
`SCM_RIGHTS` on both supported hosts.

Handoff version 3 uses a private `unix` stream. Every JSON payload has a
four-byte big-endian length prefix and remains capped at 1 MiB. Reads consume
the prefix and exact payload length through `ReadMsgUnix`, retaining ancillary
descriptor data even when the stream fragments. Writes send the prefix,
payload, and descriptor rights together, then finish any short stream write.
Control frames reject unexpected descriptors, and the importer rejects unknown
or duplicate terminal descriptor IDs so malformed handoffs cannot leak or
silently replace process descriptors.

A new importer first dials the framed stream. If that socket type does not
match, it may dial the previous `unixpacket` transport and use unframed
handoff version 2; this compatibility is importer-only and exists for the
immediately previous Linux daemon.

An already-running legacy Darwin daemon fails before spawning the new importer.
When and only when the explicit update RPC reports both `unixpacket` and
`protocol not supported`, the already-authenticated update client requests a
clean daemon stop, waits for socket and identity cleanup, and starts the caller
binary. Session state respawns saved terminal tabs. VMs and workspace data stay
running, but terminal child processes restart because descriptor transfer was
impossible. Other handoff failures still roll back without stopping the old
daemon.

### Positive Consequences

* Native macOS live handoff works without a platform-specific transport.
* Stream message boundaries are explicit and bounded.
* Linux can upgrade once from the previous packet transport without losing
  live PTYs.
* Legacy macOS installations recover through the documented `daemon update`
  command instead of requiring PID discovery.

### Negative Consequences

* The framing and exact-read implementation is more complex than packet reads.
* The one-time legacy macOS fallback restarts terminal processes.
* Handoff has a second independently versioned wire-format revision.

## Pros and Cons of the Options

### Keep unixpacket

* Good, because kernel packet boundaries match the existing implementation.
* Bad, because Darwin rejects the transport and live update cannot start.

### Use an unframed Unix stream

* Good, because streams and descriptor passing are portable.
* Bad, because one write is not guaranteed to equal one read.
* Bad, because concatenated JSON and descriptor association become ambiguous.

### Use a framed Unix stream

* Good, because framing is deterministic across fragmentation and coalescing.
* Good, because peer authentication and `SCM_RIGHTS` remain unchanged.
* Bad, because compatibility needs an explicit legacy codec at the rollout
  boundary.

## Links

* Refines [ADR 67](authenticated_scm_rights_daemon_handoff_67.md).
* Extends the caller-binary compatibility path from
  [ADR 84](use_the_caller_binary_for_cross_protocol_daemon_update_84.md).
* Shutdown readiness and recovery are refined by
  [ADR 86](use_the_daemon_lock_for_shutdown_and_startup_recovery_86.md).
