# Close deleted-node sessions in the daemon

## Context and Problem Statement

The deletion review promises that guest and host sessions belonging to a deleted
node end. A real isolated CLI check with no frontend open violated that promise:
the node disappeared, but its host terminal remained registered and its shell
still accepted commands with the same PID and exported state. Frontend stale-tab
pruning had supplied the cleanup in previous foreground checks.

## Decision Drivers

* Honor accepted deletion independently of requester and frontend lifetime.
* Preserve sessions when runtime deletion fails and the node remains live.
* Fence slow terminal launches and stale session restoration.
* Reuse daemon terminal persistence, notifications and bounded PTY teardown.

## Considered Options

* Require a frontend to prune sessions.
* Finish committed deletion's session cleanup in the daemon operation owner.
* Periodically sweep terminals without ordering against operation completion.

## Decision Outcome

Chosen option: finish cleanup in the daemon operation owner. Each ordinary delete
operation, including batch children, invokes the daemon's post-delete hook before
finalizing its receipt or releasing reservations. The hook reads the canonical
node record: a committed `DeletedAt` authorizes cleanup even if event persistence
failed afterward. A failed VM deletion retains sessions. No canceled requester
context is used to skip cleanup owed by a committed deletion.

Under the existing update read barrier and terminal-map mutex, the daemon removes
all entries belonging to the deleted node. It then uses the same path as terminal
close to stop publishers, register bounded asynchronous runtime teardown, persist
the remaining session set and publish closure notifications. Persistence errors
remain operation errors. Completion means session ownership has been removed and
runtime teardown scheduled; it does not promise the child process has already
exited. Shutdown continues to track and wait for bounded teardown. Active lifecycle
receipts prevent live replacement until this cleanup has completed.

Terminal opens resolve slugs to canonical node IDs before launching. They reject
deleted records initially and recheck the original ID under the terminal-map
mutex before publication. An entry published before deletion's removal is included
in that removal; one published afterward sees the tombstone and its runtime is
closed without becoming a session. Reusing a slug cannot retarget an in-flight
open. Cold restoration uses the same admission check, so stale persisted launch
intent cannot respawn a deleted node's shell.

This changes daemon-owned CLI/TUI deletion. A direct local SDK service does not
own daemon terminals and continues to perform only its lifecycle mutation.
No wire or persisted schema changes: ordinary/session/renderer/handoff versions
remain **29/2/6/7**.

### Positive Consequences

* Closing every frontend no longer leaves deleted-node host sessions alive.
* Partial batches close only sessions whose node deletion actually committed.
* Late launch and cold restoration cannot recreate those sessions.
* Existing close notifications, persistence and process teardown remain shared.

### Negative Consequences

* A delete can report failure after the VM and node were removed if session or
  node-event persistence fails; retained errors expose that partial outcome.
* Publication adds one cached metadata read while holding the terminal mutex.
* Physical PTY teardown remains asynchronous and bounded, as for terminal close.

## Pros and Cons of the Options

### Frontend pruning

* Good: no additional backend coordination.
* Bad: no frontend means no cleanup, contradicting accepted operation ownership.

### Daemon operation completion

* Good: ties cleanup to committed deletion and existing replacement reservations.
* Bad: requires publication fencing and explicit partial-persistence semantics.

### Periodic sweeping

* Good: could repair independently mutated metadata.
* Bad: leaves a window after successful deletion and adds a separate scheduler.

## Links

* [Parallel node ownership](own_parallel_node_batches_in_the_daemon_198.md)
* [Design and qualification ledger](../plans/tui-vnext/README.md)
