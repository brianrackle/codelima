# Tear down runtime instances before incomplete-node metadata cleanup

## Context and Problem Statement

A node directory becomes "incomplete" when creation fails or a write is interrupted before `node.yaml` lands, but after the runtime instance was created and its `lima-instance.ref` written. `node cleanup-incomplete --apply` (and the Doctor flow that recommends it) removed every incomplete directory with `os.RemoveAll`, taking the `lima-instance.ref` with it. When the referenced Lima instance was still live, that removal orphaned a running VM and destroyed the only pointer back to it — the exact failure recorded as TODO #10. How should cleanup remove incomplete metadata without orphaning a live runtime instance, and what readiness tier should the command take now that applying is a mutation that may drive `limactl`?

## Decision Drivers

* Never delete the metadata that names a live runtime instance without first tearing that instance down.
* Keep a dry run cheap and offline: inspecting the home for incomplete directories must not require `limactl`, so it still works in CI and on read-only homes.
* Match user intent — asking cleanup to remove an incomplete node means "make it go away", including the runtime instance it half-created.
* Preserve the existing Service-level lock discipline; do not push runtime knowledge into the pure-filesystem `Store`.

## Considered Options

* Keep removing incomplete metadata unconditionally (the status quo that produced the bug).
* Refuse to clean any incomplete directory whose ref names a live instance, returning an actionable error and leaving teardown to the operator.
* Consult the runtime, tear the matching live instance down first, then remove the metadata; on teardown failure keep that directory and report an actionable error naming the instance.

## Decision Outcome

Chosen option: "Consult the runtime, tear the matching live instance down first, then remove the metadata", because it is the only option that both honors the user's intent to reclaim the incomplete node and guarantees the runtime instance is gone before the pointer to it disappears. `NodeCleanupIncomplete` now reads `lima.List` once on apply; for each incomplete directory whose `lima-instance.ref` matches a live observation it calls `lima.Delete` first and only removes the directory (and its instance index) once teardown succeeds. A directory with no matching live instance keeps the historical straight-removal behavior. If teardown fails, that directory is left intact — including its ref — and the command returns an `ExternalCommandFailed` error naming the surviving instance(s) so a retry or a manual `limactl delete` can still find them.

Readiness tiers are split by mode. A dry run (`--apply` absent) is a read: it stays on the read tier (`EnsureReady(false)` — directory skeleton only, no dependency validation) so it never requires `limactl`. Applying is a mutation that can drive runtime teardown, so it takes the write tier (`EnsureReady(true)` — locked seed/repair plus dependency validation, which requires `limactl` and a working `lima.List`). Separately, `NodeDelete` now calls `ensureReady(ctx, true)` instead of the legacy `EnsureReady(true)` wrapper, threading the caller's context into dependency validation and the delete's `lima.List` rather than a detached background context.

### Positive Consequences

* `node cleanup-incomplete --apply` can no longer orphan a running VM; TODO #10 is closed at its real root cause.
* Dry runs remain usable without a runtime backend, so inspection works in CI and on read-only homes.
* A failed teardown is non-destructive and self-describing: the directory and ref survive and the error names the instance, so recovery is a retry, not a forensic hunt.
* `NodeDelete` dependency validation and teardown now honor caller cancellation.

### Negative Consequences

* `--apply` now requires `limactl` even when no incomplete directory turns out to reference a live instance, because dependency validation runs before the per-item runtime lookup.
* `NodeCleanupIncomplete` keeps its two-argument signature and issues the runtime `List`/`Delete` calls with `context.Background()` rather than a threaded context; that is acceptable for a deliberate CLI maintenance command but is not the fully context-threaded ideal that read surfaces adopted.
* Cleanup now depends on runtime availability at apply time, mirroring the read-surface coupling that ADR 37 already accepted.

## Pros and Cons of the Options

### Keep removing incomplete metadata unconditionally

Delete every incomplete directory and its instance index with no runtime consultation.

* Good, because it needs no runtime dependency and the code stays trivial.
* Good, because dry run and apply share one path.
* Bad, because it orphans any live instance the incomplete directory referenced — the recorded TODO #10 bug.

### Refuse to clean directories whose ref matches a live instance

Consult `lima.List` and error out for any incomplete directory that still names a live instance, leaving teardown to the operator.

* Good, because it never orphans a live instance.
* Good, because the Service never issues a destructive runtime call on the user's behalf.
* Bad, because it does not match user intent — the operator asked to remove the node and must now run `limactl delete` by hand before cleanup can proceed.

### Tear the instance down first, then remove the metadata

Consult `lima.List`, `lima.Delete` any matched live instance, then remove the directory; keep the directory and report an actionable error if teardown fails.

* Good, because it honors the intent of "clean this up" end to end.
* Good, because ordering teardown before metadata removal makes orphaning structurally impossible, and a failed teardown is non-destructive and names the instance.
* Bad, because apply now depends on runtime availability and issues a destructive runtime call on the user's behalf.

## Links

* Refines [ADR 37](/Users/brianrackle/projects/codelima/decisions/use_lima_as_runtime_status_source_for_read_surfaces_37.md) — cleanup joins the read surfaces in consulting live runtime observations, and extends that from status reads to teardown ordering.
* Builds on [ADR 57](/Users/brianrackle/projects/codelima/decisions/read_surfaces_do_not_write_metadata_57.md) — reuses the read/write readiness split that item 0.3 introduced.
* Implements work item 0.4 of [plans/IMPROVEMENT_PLAN.md](../plans/IMPROVEMENT_PLAN.md)
