# Record frozen bootstrap steps and explicit failed-step retry

## Context and Problem Statement

The fleet design needs the command, source, outcome, duration and output of a
failed bootstrap step. Existing metadata stored only the frozen command arrays
and a completion marker. Starting an incomplete node reran every command, so
the interface could neither substantiate per-step success nor resume safely
from a recorded failure.

## Decision Drivers

* Preserve the existing frozen recipe and ordinary start behavior.
* Record observations without inventing history for older nodes or clones.
* Make retry explicit and retain failure evidence when preflight rejects it.
* Keep runtime work outside metadata locks and persist cancellation outcomes.

## Considered Options

* Add current execution records to the frozen bootstrap metadata.
* Infer command success and provenance from log text and today's template.
* Replace lifecycle execution with a new operation engine in this change.

## Decision Outcome

Chosen option: "Add current execution records to the frozen bootstrap
metadata", because it provides a small durable execution boundary shared by
CLI and TUI while retaining existing runtime and locking contracts.

Creation freezes ordered agent, environment and template sources beside each
command. Each step stores its latest state, attempt count/ID, executing node,
start/finish times, elapsed milliseconds, observed exit code and bounded error.
The existing command arrays remain the execution authority. Missing records
or changed commands rebuild conservative records; completed legacy commands
are explicitly unrecorded. Current templates cannot supply old provenance.

Each command is persisted as running before execution, then persisted with
its outcome using bounded rollback locks even after caller cancellation. Log
headers and node events carry the attempt ID so Spec opens the right captured
output even when commands share a prefix. Spec anchors selection to the frozen
step index while changing outcomes add or remove wrapped error lines; identical
commands in separate steps remain separate selections. Bootstrap retains its 45-minute
budget; validation retains the existing runtime budget. Transport failures
without an exit status do not invent one.

`node retry` and the TUI's `r` resume the first recorded failed or interrupted
step of a failed node. Incomplete bootstrap requires success evidence for all
earlier steps. Ordinary start still reruns the entire incomplete bootstrap;
completed bootstrap only revalidates. Runtime/host-port preflight preserves
previous failure evidence. A canceled attempt requires ordinary start.
An interrupted attempt has no invented finish time and may be executed again;
these are at-least-once commands, not transactions. Operators must consider
partial side effects when retrying.

Clones inherit their disk's records and original execution node IDs. Validation
on a child records a new child-owned attempt without claiming that the child
ran its inherited installation. Existing metadata formats remain readable;
both custom JSON and YAML wire representations include the optional records.
The daemon protocol remains 10 because this change adds no RPC contract.

### Positive Consequences

* Spec and future lifecycle views can show observed source, timing and failure.
* Explicit retry avoids repeating a proven successful prefix.
* Existing start semantics, short locks and clone provenance are preserved.

### Negative Consequences

* Metadata retains only the latest execution per step and an attempt counter;
  older output/events remain subject to their existing retention policies.
* Crash recovery cannot prove whether an interrupted command had side effects.
* Daemon-owned operations, phase progress and Starting/Failed presentation
  remain separate required work in TODO #57.

## Pros and Cons of the Options

### Persist execution records

* Good: durable evidence is independent of mutable templates and log parsing.
* Bad: each command adds two short metadata writes and associated events.

### Infer from logs and current templates

* Good: no metadata extension.
* Bad: rotation, shared command prefixes and recipe edits make retry unsound.

### Replace the operation engine now

* Good: could establish daemon ownership and history together.
* Bad: couples the step storage contract to a broader RPC/lifetime migration.

## Links

* Extends [bootstrap output capture](bounded_local_node_logs_and_bootstrap_capture_157.md).
* Supplies evidence to [Spec inspection](inspect_frozen_node_specs_without_runtime_preparation_159.md).
