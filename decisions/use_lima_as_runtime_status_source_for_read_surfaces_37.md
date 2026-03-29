# Use Lima as the runtime-status source for read surfaces

## Context and Problem Statement

CodeLima persists node metadata, including a `status` field, but Lima is the system that actually knows whether a VM is running or stopped right now. When a VM changes state outside the normal `codelima node start` or `node stop` path, read surfaces like `node list` and the project tree can drift away from real Lima state unless they explicitly reconcile first.

## Decision Drivers

* Keep user-facing status output aligned with the actual Lima VM state.
* Avoid duplicate or conflicting sources of truth for `running` versus `stopped`.
* Preserve CodeLima-owned lifecycle metadata such as `created`, `failed`, and `terminated`.

## Considered Options

* Trust persisted CodeLima node status on read surfaces.
* Reconcile each node independently with a separate Lima lookup.
* Batch-read Lima observations once per surface and merge them into returned nodes.

## Decision Outcome

Chosen option: "Batch-read Lima observations once per surface and merge them into returned nodes", because it makes read-time status correct without multiplying `limactl list --json` calls and without removing the CodeLima metadata that still tracks non-Lima lifecycle state.

### Positive Consequences

* `node list` and project-tree reads can reflect external Lima state changes immediately.
* One Lima lookup per surface keeps the fix efficient when many nodes are present.
* CodeLima can still store lifecycle states that Lima does not model directly.

### Negative Consequences

* Read operations still perform live runtime inspection, so they depend on Lima availability.
* Persisted node metadata can still contain last-seen runtime information, which is not a perfect single-source-of-truth design.
* A fuller lifecycle-versus-runtime split remains future work.

## Pros and Cons of the Options

### Trust persisted CodeLima node status on read surfaces

Return stored node metadata directly and rely on previous lifecycle commands to have updated it.

* Good, because reads stay simple and avoid live runtime calls.
* Good, because it minimizes read-time coupling to Lima.
* Bad, because the stored `running` or `stopped` value can become stale as soon as the VM changes outside CodeLima.

### Reconcile each node independently with a separate Lima lookup

Call the existing per-node reconciliation path for every node in a collection.

* Good, because it reuses existing code paths.
* Good, because it keeps the per-node reconciliation semantics unchanged.
* Bad, because collection reads would scale linearly in `limactl list --json` calls.

### Batch-read Lima observations once per surface and merge them into returned nodes

Fetch the Lima observation set once for a collection read, then map observations back onto each node before rendering or returning structured data.

* Good, because it keeps Lima as the live runtime source for the returned data.
* Good, because it avoids repeated `limactl list --json` calls for multi-node reads.
* Bad, because the code still has to merge Lima runtime facts with CodeLima-owned lifecycle metadata.

## Links

* Refines [ADR 5](/Users/brianrackle/personal/codelima/decisions/defer_project_snapshots_and_runtime_validation_5.md)
