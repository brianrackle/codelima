# Retain observed lifecycle phases and boot output

## Context and Problem Statement

The Starting and Failed design views need trustworthy progress and elapsed
times. Bootstrap attempts already record command-level evidence; inferring
other phases from a node's coarse status would invent progress after reconnect.

## Decision Drivers

- Persist phase boundaries before their side effects begin.
- Distinguish skipped work, measured execution and pending work.
- Retain failure and interruption evidence across frontend and daemon exit.
- Capture boot output without duplicating bootstrap streams or query JSON.

## Considered Options

- Infer phases in each frontend from status and elapsed wall time.
- Add observed phase transitions to existing daemon operation receipts.
- Replace service methods with a new durable workflow engine.

## Decision Outcome

Add observed phases to operation receipts in protocol 12. Start/retry record
Prepare, Boot, Host credentials, Workspace, Bootstrap, Validate agent and Ready.
Create, stop, delete and clone initially record their complete mutation as one
phase. Creating and starting a VM remain separate receipts; a start does not
invent a duration for an earlier creation.

The daemon installs a private observer on its execution service copy. Service
boundaries advance only after previous work succeeds, persisting the new phase
before the next side effect. Repeated bootstrap updates change the current
command detail without restarting the phase clock. Bootstrap attempt records
remain the source of individual commands, provenance and retry history.

Phases have an identity, label, state, bounded detail and optional start/end
times. Already-running VMs, completed imports/workspaces and completed
bootstrap are skipped without fabricated timestamps. Pending phases remain
pending after earlier failure. Old receipts without phases remain unrecorded.
Summaries carry only the current phase identity; inspection returns the phases.

Execution completion closes the current phase before cancellation cleanup;
the overall receipt remains canceling until the VM is confirmed stopped.
Shutdown/crash recovery marks an active phase interrupted without inventing
its finish time or replaying work. Phase persistence failure halts progression;
after provisioning begins, the service records failure and releases its token.

During boot, a local runtime copy tees both streams into bounded operation
output. Lima start explicitly streams stdout as well as stderr. Query stdout,
including version/list JSON, stays internal. Bootstrap tees its explicit
streams once, avoiding duplication through diagnostic writers. Later UI work
will read this evidence without estimating unobserved completion or ETA.

### Positive Consequences

- Reconnected clients can explain current work and measured phase durations.
- Skipped/interrupted work remains distinct from successful work.
- Boot failures retain both streams alongside their receipt.

### Negative Consequences

- Phase transitions add atomic writes and a protocol version change.
- Storage failure stops execution before an unrecorded next phase.
- Lifecycle detail views, queued attachment and shared messages remain work.

## Pros and Cons of the Options

### Frontend inference

Cheap to render, but cannot recover trustworthy execution timing after exit.

### Existing operation receipts

Reuses persistence and cancellation without replacing the tested workflow;
requires explicit instrumentation at service boundaries.

### Workflow engine

Could model every substep, but adds replay semantics that arbitrary shell
commands cannot safely satisfy without operator decisions.

## Links

- Extends [ADR 161](daemon_owned_node_operations_and_retained_history_161.md).
- Complements [bootstrap attempts](record_frozen_bootstrap_steps_and_explicit_retry_160.md).
