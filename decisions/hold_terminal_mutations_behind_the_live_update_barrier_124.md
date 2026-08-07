# Hold terminal mutations behind the live-update barrier

Status: Accepted

## Context and Problem Statement

A live update hands every running terminal to a successor daemon over an
authenticated socket, then stops. Three defects made that handoff unsafe.

Adoption on the importing side populated `host.terminals` and `terminalOrder`
with no lock while the runtimes it had already started were delivering
callbacks that read the same maps, and it assigned `host.broadcast` after those
runtimes' publisher goroutines could already read it. That is a concurrent map
write: a fatal runtime panic sitting on every live update.

`session.json` is recovery intent, not a cache of the past: on any unclean exit
it is the only record of which tabs to respawn. The committing daemon deleted
every handed-off terminal from its own state and then persisted during
shutdown, writing an empty terminal list over the record of the tabs that had
just survived the handoff. The successor did not rewrite that file until some
later mutation, so a crash anywhere in that window lost every tab even though
every shell was still alive.

ADR 120 then made control requests dispatch concurrently on a connection. A
`terminal.open` can now land while `daemon.update` is quiescing, creating a tab
that is not in the handoff manifest and that lives only in a daemon which is
about to stop.

## Decision Drivers

* A live update must never panic the daemon, and must never lose a tab.
* `session.json` must describe the tab set that actually exists at every point
  in the handoff, including the instant the old daemon is released to exit.
* Ordering must be enforced where the state lives, not by re-serializing the
  connection that ADR 120 deliberately de-serialized.
* A runtime callback must never be blocked for the duration of an update; the
  terminal actor it comes from would be wedged behind it.
* A rejected request must be distinguishable from a failure, so a client knows
  to reissue it rather than surface an error.

## Considered Options

* Serialize `daemon.update` with control requests again by returning
  `terminal.open` to a shared lane.
* Capture the manifest, then reconcile: let concurrent opens proceed and
  transfer any tab created during the handoff in a second round.
* Add a host-level update barrier that holds terminal-set mutations for the
  duration of the update, and resolve them by its outcome.

## Decision Outcome

Chosen option: "add a host-level update barrier", because the tab set is
daemon-owned state and the invariant is about that state, not about one
connection's request order. A second connection could always have raced the
first; only a lock on the host closes it.

`daemonHost` now takes three locks in a fixed order: `updateGate`, then
`persistMu`, then `mu`. `daemon.update` takes `updateGate` for writing across
the whole handoff, so the quiesced runtimes and the manifest session are
captured under one lock and can no longer describe different tab sets.
`terminal.open`, `terminal.close` and `terminal.move` take it for reading;
`terminal.move` is included because the tab order it mutates travels in the
manifest. The read side is not reentrant while a writer waits, so each gated
entry point is a wrapper over an ungated body, and `open`'s rollback closes
through the ungated body.

**Barrier semantics: hold until the update resolves, then serve or reject.** A
mutation that arrives during a handoff blocks. If the update fails, this daemon
still owns the tabs and the mutation is served normally. If the update commits,
a `replaced` flag is set while the write lock is still held, and the mutation is
answered `PreconditionFailed` with a typed `daemon_replaced` field, so the
caller reissues it against the successor. A tab therefore either exists before
the manifest is captured, and is handed over, or is never created at all.
Serving the held request from the new daemon is not available: the request
arrived on a connection the successor does not own.

**Persist before commit.** The successor writes `session.json` for the tab set
it adopted before it reports `committed`, because that message is what releases
the predecessor to exit. A failed write is reported as a handoff failure so the
predecessor rolls back with its own session intact, rather than continuing with
the tab set unrecoverable. The predecessor sets `replaced` the moment it reads
`committed` and before it tears down its own state, and `persist` is a no-op
from then on: shutdown and any late mutation can no longer overwrite the
successor's record with the empty list this daemon is left holding.

**Adoption under the host lock.** Server links are published before the first
terminal entry exists, because an entry starts a publisher goroutine that reads
them with no synchronization other than having been created afterwards. Each
adopted entry is then published under `mu`, and the explicit tab order is
rebuilt from the manifest session under one final `mu`. The adoption call
itself runs outside `mu`, so a callback arriving from a runtime that has just
started cannot deadlock against the adoption that started it.

**The terminal-closed callback path is deliberately not gated.** It runs on a
runtime callback goroutine, and holding it for the length of an update risks
wedging the terminal actor it belongs to — the failure ADR 120 exists to
prevent. A terminal whose child exits during quiescence therefore leaves the
manifest session and the quiesced runtimes disagreeing, which the importer
rejects and the exporter rolls back. That is a clean, observable failure of one
update; no tab is lost, because the tab in question is the one that ended.

Publication is additionally gated on the server actually serving. An imported
daemon owns live terminals before its server runs, and `Server.Broadcast` reads
daemon-epoch state that `Server.Run` writes while binding its sockets. Nothing
can be subscribed before the server serves, so a suppressed early event has no
audience, and opening the gate wakes every publisher so suppressed state is
republished rather than lost.

### Positive Consequences

* A live update can no longer panic on a concurrent map write.
* A crash immediately after a handoff commits still finds every handed-off tab
  in `session.json`.
* A tab created while an update is in flight is either handed over or never
  created; it can never exist only in a daemon that has already handed off.
* The manifest and the quiesced runtimes are captured under one lock, closing a
  pre-existing window in which they could disagree.
* A second concurrent `daemon.update` is refused instead of handing off an
  empty tab set from a daemon that no longer owns anything.
* Clients discriminate on a typed `daemon_replaced` field rather than on error
  text.

### Negative Consequences

* `terminal.open` blocks for the duration of a live update, which is bounded by
  the successor's own readiness deadline rather than by a request timeout.
* A held `terminal.open` is answered with a rejection the caller must reissue;
  until a client retries automatically, a user may see one failed tab.
* A terminal that exits during quiescence fails the update instead of being
  handed over as an already-dead tab.
* The importing daemon suppresses events until its server answers, so the very
  first publication after adoption is a republication rather than the original.

## Pros and Cons of the Options

### Re-serialize update with control requests

* Good, because it needs no new host state.
* Bad, because it reinstates the head-of-line blocking ADR 120 removed, and a
  live update is the longest control operation there is.
* Bad, because it only orders one connection; a second connection's
  `terminal.open` still races the manifest.

### Capture the manifest, then reconcile afterwards

* Good, because no request is ever held or rejected.
* Bad, because a tab created after quiescence has a live PTY that was never
  transferred, so reconciliation means a second descriptor-passing round with
  its own failure modes.
* Bad, because the old daemon must stay alive to serve that round, which
  contradicts committing.

### Host-level update barrier

* Good, because the invariant is enforced where the state lives, so it holds
  across connections.
* Good, because both outcomes are safe by construction: served if the update
  failed, rejected if it committed.
* Bad, because it introduces a third lock and an ordering that every future
  mutation path has to respect.

## Links

* Resolves finding 3 of [plans/ISSUES_PLAN.md](../plans/ISSUES_PLAN.md)
  ("concurrent map write during live update", "live update clobbers
  session.json") and the concurrency window flagged by
  [ADR 120](schedule_daemon_requests_by_delivery_class_120.md).
* Preserves the recovery contract from
  [ADR 66](version_daemon_session_persistence_66.md).
* Refines [ADR 67](authenticated_scm_rights_daemon_handoff_67.md) and
  [ADR 85](use_framed_unix_streams_for_portable_daemon_handoff_85.md).
