# Own parallel node batches in the daemon

## Context and Problem Statement

The Dialogs and NodesFilter artboards require one captured batch review,
parallel execution and per-node progress. The frontend previously submitted
targets sequentially, stopped at its first error and lost pending submissions
when it exited. Individual daemon operations already survive requester exit.

## Decision Drivers

* Admit the complete reviewed target set before changing any node.
* Preserve partial outcomes, queued work and explicit cancellation ownership.
* Reuse ordinary node operations and their lifecycle cleanup guarantees.
* Bound concurrency, persisted records and polling responses.

## Considered Options

* Submit independent operations concurrently from the frontend.
* Persist a root batch and execute ordinary child operations in the daemon.
* Introduce an independent scheduler and separate operation storage.

## Decision Outcome

Chosen option: persist a root batch and execute ordinary child operations in
the daemon. A batch accepts 1–64 ordered, unique canonical node IDs with the
reviewed names and start/retry/restart/stop/delete action kinds. Invalid,
renamed, missing or conflicting targets reject the whole request. The root
receipt commits all targets and their future child IDs before acknowledgement
or execution. One root owns all node/slug reservations until completion.

The daemon runs at most four children per batch and sixteen lifecycle operations
globally. Up to sixteen root owners may be admitted. Each child receipt is
persisted before execution and uses ordinary service claims, phases, output,
startup cleanup and retained errors. Child failure does not stop other targets.
Canceling the root cancels active children and skips queued work; canceling one
running child leaves its siblings running. Requester cancellation only ends its wait.

The root retains counts and bounded per-member outcomes linked to full child
receipts. A partially unsuccessful batch is failed with explicit outcome counts.
On daemon restart, completed children are projected back into the root;
uncertain/unstarted work becomes interrupted and is never replayed automatically.
The atomic root admission remains authoritative if a later progress snapshot
cannot be persisted; child receipts permit recovery without guessing effects.

Active list summaries include bounded member identities/state for queued fleet
rows. Completed history summaries omit member lists; opening the root fetches
all outcomes. Child receipts retain normal per-node detail and Activity routes.
Ordinary protocol becomes **29**; session/renderer/handoff stay **2/6/7**.

Live fresh-home qualification exposed Lima 2.1's first-start SSH-key race:
its key existence check precedes its generation lock, so a concurrent loser
tries to overwrite the winner's key. `LimaClient.Start` now takes a separate
cross-process `.codelima-start.lock` under the resolved Lima `_config` directory
only while the identity is incomplete. Lima still generates and owns the keys.
The gate releases when the private file is nonempty and a bounded public-key
read parses successfully, allowing both VM boots to proceed concurrently.
Failure releases the gate; cancellation stops a waiter. The persistent lock
inode is never unlinked during use. No metadata domain lock spans runtime work.

Marked actions and directory palette actions share a themed, scrollable review.
It captures membership and action kinds, revalidates before submission, shows
actual session/resource evidence, requires the untrimmed exact count for delete,
and refuses pasted/repeated/released activation or unreadable terminal sizes.
Esc discards the review. Successful marked actions clear only their captured
marks; directory actions preserve unrelated marks.

A failed/interrupted deletion may retain its durable terminating marker. Fresh
revision-matched daemon evidence of an unclaimed target, with no active local,
daemon or batch reservation, exposes only a new deletion review. The fleet shows
`delete incomplete`. The existing service claims and recovers that marker under
its locks on explicit resubmission; the frontend does not edit lifecycle state.
Shared dialog validation is retained across non-editing input events and is
cleared only when the field value changes.
The review distinguishes stop/restart (guest sessions end, host shells survive)
from deletion (both kinds of node-owned sessions end; host workspace files stay).

### Positive Consequences

* Closing a TUI cannot discard accepted pending targets.
* Partial failure and cancellation retain inspectable per-node outcomes.
* Existing lifecycle behavior and output are shared instead of duplicated.
* A restart does not repeat uncertain destructive work.

### Negative Consequences

* Older clients/daemons must update for protocol 29.
* A completed member remains reserved until its whole batch finishes.
* Interrupted queued work requires a new explicit review/submission.
* The identity gate coordinates CodeLima starts; an external `limactl start`
  does not participate in this gate.
* Root and child records require bounded recovery/projection logic; this is
  atomic admission, not transactional rollback of completed VM mutations.

## Pros and Cons of the Options

### Frontend concurrent submissions

* Good: small implementation change.
* Bad: frontend exit still loses pending work and cannot atomically admit a set.

### Root receipt and ordinary children

* Good: durable membership and familiar per-node phases, cancellation and errors.
* Bad: introduces linked records and explicit partial-result recovery.

### Separate scheduler/storage

* Good: could support broader scheduling policies.
* Bad: duplicates lifecycle ownership and history for the current design scope.

## Links

* [Daemon-owned node operations](daemon_owned_node_operations_and_retained_history_161.md)
* [Clone/start ownership](retain_clone_and_start_under_one_daemon_receipt_197.md)
* [Lima 2.1 identity initialization](https://github.com/lima-vm/lima/blob/v2.1.0/pkg/sshutil/sshutil.go)
* [Design and qualification ledger](../plans/tui-vnext/README.md)
