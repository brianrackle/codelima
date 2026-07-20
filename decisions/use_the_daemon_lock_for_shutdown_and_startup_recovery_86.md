# Use the daemon lock for shutdown and startup recovery

Status: Accepted

## Context and Problem Statement

The first legacy-Darwin update fallback requested a clean daemon stop and then
waited five seconds for socket and identity pathnames to disappear. A daemon
closes its listeners before it finishes forwarding and terminal teardown, but
the Unix socket pathnames can remain until final cleanup. Terminal shutdown was
also serialized and can spend roughly 2.5 seconds escalating each process.
Consequently the client could report that the daemon did not stop even though
shutdown was progressing, while a following start lost the still-held daemon
lock and exited before becoming ready.

What state authoritatively determines that a replacement daemon may start, and
how should CodeLima recover when a compatible daemon cannot finish shutdown?

## Decision Drivers

* Never start two daemon owners for one home.
* Do not mistake stale socket or identity pathnames for a live daemon.
* Preserve terminal restoration intent before potentially slow cleanup.
* Bound shutdown time even when a terminal or forwarding peer is stuck.
* Never signal a PID unless the persisted daemon identity still matches.
* Keep ordinary application RPCs on exact-version negotiation.

## Decision Outcome

The advisory lock at `_locks/daemon.lock` is the authoritative startup gate.
Shutdown callers probe it with nonblocking `flock`; stale endpoint and identity
files do not delay a replacement once the lock is available.

Daemon host shutdown persists the session before forwarding teardown and closes
terminal runtimes concurrently. Legacy fallback waits at least 15 seconds plus
three seconds per reported terminal, capped at two minutes. If that deadline is
exceeded, CodeLima rereads `daemon.identity`, requires the token and PID to
still match, verifies the lock remains held, sends `SIGTERM`, and escalates to
`SIGKILL` only after another bounded grace period.

Daemon startup performs the same recovery when exact-version ping fails while
the lock remains held. A persisted protocol-2-through-current identity may be
used to authenticate the lifecycle client, request a graceful stop if its
socket still works, or identify an already-stopping owner before bounded
termination. An unrecognized, newer, missing, or malformed identity is never
signaled automatically.

This makes daemon lifecycle management (`daemon update` and startup recovery)
the only compatibility boundary. Ordinary status, terminal, node, and TUI RPCs
remain exact-version.

### Positive Consequences

* Legacy macOS update no longer fails merely because teardown exceeds five
  seconds or leaves endpoint pathnames temporarily present.
* TUI autostart can recover the exact socket-closed/lock-held state instead of
  spawning a daemon that immediately loses the lock.
* Many terminal tabs shut down within one escalation window instead of one
  window per tab.
* Forced termination is bounded and protected against PID reuse by identity
  and lock checks.

### Negative Consequences

* A genuinely stuck compatible daemon may be terminated during an explicit
  update or startup recovery.
* Lifecycle recovery adds a second persisted-identity compatibility use beside
  live update.
* Concurrent terminal teardown can create a short burst of process signals and
  file-descriptor closure.

## Links

* Extends [ADR 85](use_framed_unix_streams_for_portable_daemon_handoff_85.md).
* Refines [ADR 65](exact_version_json_lines_daemon_protocol_65.md) and
  [ADR 84](use_the_caller_binary_for_cross_protocol_daemon_update_84.md).
