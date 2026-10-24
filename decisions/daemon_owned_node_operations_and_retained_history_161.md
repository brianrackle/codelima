# Own node operations in the daemon and retain explicit execution receipts

## Context and Problem Statement

Lifecycle actions launched by the fleet manager used the TUI context and
disappeared from its operation list when that window closed. The retrieved
design requires work to survive frontend exit, one operation per node,
explicit cancellation and seven-day history shared by clients.

## Decision Drivers

* Frontend and connection lifetime must not own guest provisioning.
* Lost responses must not cause duplicate lifecycle mutations.
* Existing service liveness tokens and frozen bootstrap execution stay valid.
* Restart recovery must not silently replay partially executed commands.
* History and RPC responses must remain bounded and inspectable.

## Considered Options

* Add daemon-owned operation admission around existing service methods.
* Keep frontend goroutines alive with detached background contexts.
* Persist workflows and automatically replay unfinished work after restart.

## Decision Outcome

Chosen option: "Add daemon-owned operation admission around existing service
methods", because the daemon already owns shared runtime state and its process
lock provides one writer for operation records.

Protocol 11 adds `operation.submit`, `operation.get`, `operation.list` and
`operation.cancel`. Admission uses a caller-supplied UUID and returns a durable
receipt before runtime work begins. Repeating the same ID and request returns
the original receipt, including after deletion; reusing it for different work
fails. Canonical node IDs arbitrate conflicts across ID/slug requests, and
creation/clone target slugs are reserved. At most 16 operations run concurrently.

The daemon executes start/retry/stop/create/clone/delete on independent contexts.
TUI-facing service calls and existing synchronous node RPCs wait for receipts;
losing the caller ends its wait. Direct SDK service calls retain their existing
inline cancellation behavior. Existing filesystem liveness tokens continue to
arbitrate with those callers.

Private atomic `_operations/<id>.json` records retain request, identity, state,
timestamps, observed error category/code and a compact result. Requests are
limited to 64 KiB and records to 256 KiB. Completed history expires after seven
days. Listing always includes running operations and paginates history with
an explicit cursor; summaries omit request bodies and bound displayed strings.
Pages stay below half the daemon frame limit, leaving room for framing.

The receipt also retains the latest 200 output lines, bounded to 32 KiB and
4 KiB per line, with an absolute line sequence and bounded partial line.
Writes coalesce once per second and flush on completion. Bootstrap/validation
capture tees explicit runtime streams so custom runtimes behave consistently.
TUI waiters stream newly observed lines; Activity reads shared running/history
pages independently of directory scope, with serial-fenced asynchronous loads,
durable selection, cancellation and navigation back to the operation's node.
Other status messages remain explicitly labeled local until the shared message
journal is implemented.

Explicit cancellation of start/retry waits for the service to release its
liveness token, then stops the VM using a fresh bounded context while retaining
the manager's reservation. Failure to stop is recorded as failure, not a
successful cancellation. Daemon shutdown interrupts work without implicitly
stopping all VMs. Recovery marks unfinished receipts interrupted, leaves finish
time unknown, and never replays work automatically. Bootstrap step evidence
continues to follow ADR 160's conservative retry rules.

Stop decisions bypass the runtime observation cache before deciding whether
to stop and before reporting success. A cached stopped sample may predate a
canceled boot. Only an absent or explicitly stopped instance permits a no-op;
broken or installing states require a stop attempt. An unconfirmed stop is a
failure, so cancellation cannot falsely claim the VM was shut down.

Admission shares the daemon update barrier. Live replacement refuses while
lifecycle work is active, so a terminal-session handoff cannot abandon an
operation that it does not transfer.

### Positive Consequences

* Closing a frontend or request connection no longer cancels admitted work.
* Idempotent receipts make delivery recovery independent of mutable targets.
* All clients can inspect the same durable operation state.

### Negative Consequences

* Live daemon update waits for active operations to finish or be canceled.
* Interrupted commands may have partial effects; recovery needs an explicit
  operator choice rather than promising exactly-once execution.
* Long-lived processes must prune history and handle metadata failures.

## Pros and Cons of the Options

### Daemon admission around existing services

* Good: reuses tested runtime, bootstrap and lock boundaries.
* Bad: requires a versioned RPC contract and frontend projection of receipts.

### Detached frontend goroutines

* Good: minimal API change.
* Bad: process exit still loses ownership and other clients cannot inspect work.

### Automatic workflow replay

* Good: could resume without operator input.
* Bad: arbitrary shell commands have side effects that cannot be rolled back
  or reliably deduplicated after an interrupted transport.

## Links

* Builds on [recorded bootstrap attempts](record_frozen_bootstrap_steps_and_explicit_retry_160.md).
* Remaining shared messages, phase/boot output, CLI admission and lifecycle UI work is
  tracked in TODO #57 and the design delivery ledger.
