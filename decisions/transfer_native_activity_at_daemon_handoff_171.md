# Transfer pending native Activity and observation baselines during daemon handoff

## Context and Problem Statement

Live replacement blocks the predecessor's Activity writer and suppresses its
shutdown flush. Pending native notices were therefore lost on a successful
update. Seeding terminal observers from restored renderer metadata also hid
output that the old observer had not yet announced.

## Considered Options

* Flush the queue immediately before starting replacement.
* Stop every producer and require a durable final drain before releasing ownership.
* Transfer the bounded queue and its observation baselines at one admission boundary.

## Decision Outcome

Handoff version 6 adds a separate Activity payload, bounded at 8 MiB and carried
in existing size-limited chunks. Versions 2–5 remain importable. Ordinary daemon
and renderer protocols remain 17 and 5 because their client contracts are unchanged.

After terminal PTY reads quiesce, the predecessor freezes native observation
admission under the same mutex that protects queue insertion and terminal,
route and capacity baselines. It copies pending requests with their original
IDs, the overflow count and all baselines into the transfer. The update barrier
already excludes queue removal. A late snapshot cannot advance a baseline after
this boundary. No producer performs journal I/O.

The successor validates the complete payload before installing it or activating
terminals. Its queue stays memory-only until daemon ownership and the committed
reply. Terminal observers start from the transferred last observation, allowing
the restored renderer to reveal previously unobserved output exactly once.
Older handoff versions retain the fresh-renderer baseline behavior because they
provide no prior observation. Route and capacity baselines prevent unchanged
observations from being announced again.

Rollback retains the predecessor's original queue and baselines, resumes native
admission only if ownership is intact, and requests fresh terminal observations.
Successful replacement retires the predecessor without flushing its copied queue.
Overflow retains the existing omission-count semantics. Process crashes before
persistence can still lose memory-only events; this change does not make producer
admission a durable acknowledgment.

Journal initialization failures are retried at most once per second under the
existing ownership barrier. A live blocked-journal check showed that retaining
the queue alone was insufficient while the initial open error remained cached
forever. The bounded retry restores persistence after storage is repaired without
restarting the daemon or losing its in-memory queue.

### Positive Consequences

* Successful replacement preserves admitted notices and their retry identities.
* Observed and unobserved terminal output have a consistent handoff boundary.
* Rollback resumes from the same queue without generating duplicate identities.

### Negative Consequences

* The handoff protocol gains another bounded payload and validation step.
* Intermediate transitions while observation is frozen remain subject to sampling.
* Older predecessors cannot transfer pending events they never exported.

## Pros and Cons of the Options

A pre-update flush is small but leaves a race before ownership release and cannot
survive a temporarily unavailable journal. A mandatory final drain avoids queue
transfer but couples updates to journal availability and requires stopping all
observers. Transferring queue and baselines adds protocol state while allowing
the successor to retry the same events after replacement.

## Links

* [Native Activity queue](record_native_route_and_capacity_activity_168.md)
* [Terminal Activity and typed navigation](record_terminal_activity_with_typed_navigation_170.md)
