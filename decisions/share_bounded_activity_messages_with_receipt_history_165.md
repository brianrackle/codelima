# Share bounded Activity messages with receipt history

## Context and Problem Statement

Lifecycle receipts already survive frontend exit, but other Activity messages
were kept only in each window. Merging these sources must preserve paging and
selection, avoid duplicate lifecycle output, and distinguish pending local
messages from committed shared evidence.

## Considered Options

* Persist the frontend ring as a whole on every change.
* Add messages to lifecycle operation receipt files.
* Use an independent daemon journal and merge read projections.

## Decision Outcome

Use a daemon-owned `_activity` directory under the metadata root (0700), with
atomic per-message JSON files (0600). Stable client UUIDs make retries idempotent.
The daemon assigns commit time and sequence. Retain at most 10,000 messages for
up to seven days, with 4 KiB text and a 32 KiB serialized-record ceiling. Prune
on admission, history reads and startup. Corrupt records are reported, retained
for diagnosis, and do not hide independent lifecycle receipts.

Protocol 14 adds `activity.append` on the control lane and `activity.list` on
the query lane. Journal access participates in the daemon replacement barrier.
The merged query returns all running operations and at most 100 history rows,
bounded below half a transport frame. History contains canonical operation
request/completion projections and general messages. Completion keeps the
running receipt ID; requests have a separate suffix. Time and ID define stable
ordering and cursors. Expired cursors require an explicit reload.

New lifecycle requests carry optional CLI/TUI provenance. Provenance does not
change idempotency inputs and does not invent origin for legacy receipts.
An explicit admission callback lets the frontend suppress republishing known
receipt results/output, while retaining admission and local failures.

Each frontend retains at most 200 pending/local messages, writes asynchronously
one at a time, and retries the same UUID after failure or a lost completion.
Pending messages are labeled “this window”. Acknowledged messages overlay the
newest page until its epoch/sequence watermark includes them. A newer daemon
epoch makes its snapshot authoritative. Journal errors never recursively create
journal messages. Footer notices expire after six seconds without clearing
history. There is no durable frontend outbox.
Record a connection outage once per window, then its recovery. Reconnect and
refresh retries continue to log diagnostic detail without filling shared
history with the same unavailable-daemon condition.

### Positive Consequences

* Windows share durable history with stable selection and bounded storage.
* Receipt output remains in its authoritative store, without notification spam.
* A failed message store does not disable lifecycle inspection/cancellation.

### Negative Consequences

* Pending messages can be lost on frontend exit or queue eviction.
* Shared history needs polling and a second bounded store to maintain.
* Native route/capacity/daemon events need explicit producers; frontend messages
  do not substitute for events that happen while no frontend is connected.

## Pros and Cons of the Options

Persisting whole frontend rings is simpler locally but produces multi-writer
conflicts and duplicates. Extending lifecycle receipts mixes unrelated retention
and identity contracts. An independent journal adds files and projection logic,
but keeps each source authoritative and allows graceful partial availability.

## Links

* [Daemon operation ownership](daemon_owned_node_operations_and_retained_history_161.md)
