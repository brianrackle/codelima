# Delete the dead Patch subsystem

## Context and Problem Statement

CodeLima carried a full "patch" subsystem — propose, list, show, approve, reject, apply — that diffed one project workspace snapshot against a lineage neighbour, stored the diff plus status/approval/apply metadata under `CODELIMA_HOME/patches/<id>/` and `_index/patches/by-status/`, and applied it back with `git apply`. None of it was reachable: the CLI never registered a `patch` command group (`isCommandGroup` omits it), the TUI never called any `Patch*` method, and the only callers were two service tests. The code still cost us: every fresh home created two dead directory trees, `doctor` walked `_index/patches/by-status` on every run, and the six `Service` methods plus their store layer and types were dead weight that the upcoming daemon and microsandbox work would have to keep compiling and reasoning about. Do we keep maintaining an unreachable feature, or delete it?

## Decision Drivers

* Track 0 is about shrinking and stabilizing the surface area before the daemon (Tracks 1–4) amplifies every lifecycle and storage concern it inherits.
* Dead code that touches storage layout, `doctor`, error taxonomy, and shared snapshot helpers is a standing tax on every future change to those seams.
* The snapshot machinery the patch code shared (`captureSnapshot`/`materializeSnapshot`/`SaveSnapshot`) is still live via `ProjectFork` and must not be disturbed.
* Existing homes written by older binaries still contain `patches/` and `_index/patches/` and must keep loading.

## Considered Options

* Delete the Patch subsystem entirely, keeping only the snapshot helpers that `ProjectFork` shares.
* Keep the subsystem but wire it up to a real `patch` CLI command group and finish it.
* Leave the code dead and untouched.

## Decision Outcome

Chosen option: "Delete the Patch subsystem entirely", because it is unreachable, unowned, and actively in the way of Track 0's stabilization goal, while nothing depends on it that cannot be preserved directly.

Concretely, the following were removed:

* The six `Service` methods `PatchPropose`/`PatchList`/`PatchShow`/`PatchApprove`/`PatchReject`/`PatchApply`, the `PatchProposeInput` type, and the patch-only helper `resolveLineageEdge` (its only caller was `PatchPropose`, and it was the only remaining reader of the `PatchDirection*` constants).
* `patches.go` in full (`buildPatch`, `applyPatchChecked`, `summarizePatch`, `rewritePatchPaths`, `copyTree`).
* The `Store` methods `SavePatch`, `PatchByID`, `LoadPatchDiff`, `ListPatches`, `AppendPatchEvent`, `PatchEvents`, `OrphanedPatchStatusIndexes`, all `patch*Path` helpers, and the `_index/patches/by-status` and `patches` mkdirs in `ensureDirectories`.
* The types `PatchProposal`, `DiffSummary`, `ApplyResult`, `ConflictSummary`, `ApprovalMetadata` and the `PatchDirection*`/`PatchStatus*` constants.
* The `doctor` orphaned-patch-status-index check and its warning wiring, so `doctor` output and its JSON no longer mention patches.
* The now-callerless `patchConflict` error constructor. The exit-code constants (`ExitPreconditionFailed`, to which `PatchConflict` mapped) are an external contract and stay; only the constructor and its `"PatchConflict"` category string are gone.
* The two service tests `TestPatchFlowApproveAndApply` and `TestPatchApplyConflictDoesNotMutateTarget`.

`snapshot.go` is kept intact. `captureSnapshot`, `materializeSnapshot`, and the store's `SaveSnapshot` are used by `ProjectFork` (`materializeSnapshot`'s only caller is `ProjectFork`), so the file survives even though `syncWorkspaceFromTree`/`restoreWorkspace` — which only `PatchApply` used — are now unreferenced package functions; they are left in place per the "keep snapshot.go whole" boundary and remove cleanly later if desired.

Existing homes are handled by tolerance, not migration: nothing new writes `patches/` or `_index/patches/`, and every read surface (`ProjectList`, `NodeList`, `Doctor`) ignores those trees if an older binary left them behind. Two guard tests pin this: `TestEnsureDirectoriesOmitsLegacyPatchDirs` asserts a fresh home creates neither directory, and `TestHomeWithLegacyPatchDirsStillLoads` asserts a home seeded with legacy `patches/<id>/proposal.yaml` and a by-status ref still loads and produces no patch-related `doctor` warning.

The export/sync capability that a patch-like flow was once imagined to serve (TODO #8 — returning files out of a node) is deliberately **not** resurrected from this model. It is designed fresh later as `codelima node export`/`sync`; TODO #8 continues to describe that, and nothing here revives the diff/approve/apply state machine.

### Positive Consequences

* The metadata store, `doctor`, and error taxonomy shrink to only what is reachable; fresh homes stop creating two dead directory trees.
* The daemon and microsandbox tracks inherit less surface area to preserve.
* `ProjectFork` and its snapshot path are untouched and stay green.

### Negative Consequences

* Any external tooling that read `CODELIMA_HOME/patches/` or `_index/patches/` directly (there is none we ship) would no longer see fresh data — though legacy contents are ignored, not deleted.
* Two snapshot helpers (`syncWorkspaceFromTree`/`restoreWorkspace`) are now dead but retained; a future cleanup should remove them once the "keep snapshot.go whole" constraint is lifted.

## Pros and Cons of the Options

### Delete the Patch subsystem entirely

* Good, because it removes unreachable, unowned code that taxes the storage, doctor, and error seams.
* Good, because it is verifiable: compilation drives completeness, and the grep sweep leaves only the substring "dispatch" and the two absence-guard tests.
* Bad, because a genuinely-designed export/sync feature must be rebuilt rather than adapted (accepted: the patch model was the wrong shape for it anyway).

### Keep the subsystem and wire up a real `patch` CLI command group

* Good, because it would preserve prior effort.
* Bad, because there is no product demand for a project-to-project git-diff/approve/apply flow, and finishing it would deepen coupling to storage and snapshot internals right before the daemon rework.

### Leave the code dead and untouched

* Good, because it is zero immediate work.
* Bad, because every future change to storage layout, doctor, or the snapshot helpers must keep the dead paths compiling and consistent, and fresh homes keep materializing dead directories.

## Links

* Relates to [ADR 5](/Users/brianrackle/projects/codelima/decisions/defer_project_snapshots_and_runtime_validation_5.md) — the snapshot machinery this decision preserves for `ProjectFork`.
* Refined-by TODO #8 — the node export/sync replacement is designed fresh, not resurrected from the patch model.
