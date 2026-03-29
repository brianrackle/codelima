# Persist only node lifecycle metadata in `node.yaml`

## Context and Problem Statement

CodeLima was writing `status`, `last_reconciled_at`, and `last_runtime_observation` into `node.yaml` even though Lima is the authority on current VM runtime state. That made the node file a second runtime-status store and left room for stale `running` or `stopped` values to linger on disk after Lima had already changed.

## Decision Drivers

* Keep Lima as the source of truth for live VM runtime state.
* Remove runtime-derived fields from durable node metadata.
* Preserve CodeLima-owned lifecycle states such as `created`, `failed`, and `terminated`.

## Considered Options

* Keep persisting runtime-derived node status in `node.yaml`.
* Stop persisting only the top-level `status` field but keep the other runtime-derived fields.
* Persist only lifecycle metadata in `node.yaml` and reconstruct runtime state in memory from Lima.

## Decision Outcome

Chosen option: "Persist only lifecycle metadata in `node.yaml` and reconstruct runtime state in memory from Lima", because it removes the durable duplicate of Lima runtime state while still preserving CodeLima-owned lifecycle facts that Lima does not model for us.

### Positive Consequences

* `node.yaml` no longer claims to know whether a VM is currently `running` or `stopped`.
* Read surfaces can still expose a `status` value after reconciling with Lima, without treating the file as runtime truth.
* Older node files can be migrated by loading legacy `status` values into the in-memory model and rewriting them in the new shape.

### Negative Consequences

* Store reads that do not reconcile with Lima can no longer rely on persisted runtime observations being present.
* The in-memory `Node` model still overloads one `status` field for both lifecycle and runtime until a broader API split is done.
* Migration logic is needed so older `node.yaml` files remain readable.

## Pros and Cons of the Options

### Keep persisting runtime-derived node status in `node.yaml`

Continue writing `status`, `last_reconciled_at`, and `last_runtime_observation` into the node metadata file.

* Good, because reads can show a last-known runtime state without querying Lima.
* Good, because it preserves the previous file shape.
* Bad, because the file becomes a stale duplicate of Lima runtime state.

### Stop persisting only the top-level `status` field but keep the other runtime-derived fields

Remove the top-level `status` key while still writing cached runtime observations into the node file.

* Good, because it narrows the most obvious source-of-truth conflict.
* Good, because it touches fewer call sites than a fuller persistence change.
* Bad, because runtime-derived state still lives on disk under a different field name.

### Persist only lifecycle metadata in `node.yaml` and reconstruct runtime state in memory from Lima

Write lifecycle-only node metadata to disk and keep runtime observation data in memory on read surfaces that reconcile with Lima.

* Good, because it makes Lima the only durable source of live runtime facts.
* Good, because it preserves CodeLima lifecycle data that still needs persistence.
* Bad, because callers that want runtime state must reconcile explicitly instead of reading it from the file.

## Links

* Refines [ADR 37](/Users/brianrackle/personal/codelima/decisions/use_lima_as_runtime_status_source_for_read_surfaces_37.md)
