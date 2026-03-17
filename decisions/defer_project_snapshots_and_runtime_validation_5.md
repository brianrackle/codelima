# Defer Project Snapshots And Runtime Validation For Metadata Mutations

## Context and Problem Statement

Project create and update plus reusable environment config mutations were taking much longer than expected because they performed broad runtime validation and, in the create path, captured an initial full-workspace snapshot. Those operations only need to persist local metadata, so they should stay fast and usable even when Lima is slow or unavailable.

## Decision Drivers

* Project and environment-config saves must feel immediate in the CLI and TUI.
* Metadata-only commands should not depend on live Lima state.
* The system still needs on-demand snapshots for fork and patch workflows.

## Considered Options

* Keep eager project snapshots and broad runtime validation on all mutating operations.
* Keep the eager snapshot but add only a TUI progress indicator.
* Treat metadata-only mutations as local store updates and defer snapshots to workflows that actually need them.

## Decision Outcome

Chosen option: "Treat metadata-only mutations as local store updates and defer snapshots to workflows that actually need them," because it removes the unnecessary slow path instead of only masking it with UI feedback.

### Positive Consequences

* `project create` and `project update` no longer block on `limactl list`.
* Reusable environment config create, update, and delete no longer require Lima to be available.
* Project registration becomes metadata-only, while fork and patch flows still capture snapshots on demand.

### Negative Consequences

* Newly created projects no longer have an initial snapshot recorded automatically.
* Runtime validation policy is now split by operation type instead of being uniformly tied to all mutations.
* Operators who expected an eager snapshot artifact immediately after project create will need to rely on fork or patch flows to generate snapshots.

## Pros and Cons of the Options

### Keep eager project snapshots and broad runtime validation on all mutating operations

Preserve the previous behavior where all mutating calls validate external dependencies and project create captures an initial snapshot.

* Good, because the readiness policy is simple and uniform.
* Good, because every project immediately has a snapshot artifact on disk.
* Bad, because metadata saves can stall on slow Lima calls or large workspaces.
* Bad, because local metadata management is unusable in environments without `limactl`.

### Keep the eager snapshot but add only a TUI progress indicator

Improve operator feedback while keeping the backend behavior unchanged.

* Good, because the UI no longer looks frozen during slow saves.
* Good, because the eager snapshot behavior remains intact.
* Bad, because the operation is still slow.
* Bad, because CLI users and API callers still pay the same unnecessary cost.

### Treat metadata-only mutations as local store updates and defer snapshots to workflows that actually need them

Separate metadata persistence from runtime-backed operations and capture snapshots only in fork or patch workflows.

* Good, because local project and config saves stay fast.
* Good, because snapshot cost is paid only by features that actually consume snapshots.
* Bad, because the service behavior is less uniform across mutation types.
* Bad, because operators lose the eager initial snapshot artifact.

## Links

* Related to [package_binary_releases_and_homebrew_tap_4.md](package_binary_releases_and_homebrew_tap_4.md)
