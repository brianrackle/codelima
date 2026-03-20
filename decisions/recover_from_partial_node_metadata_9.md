# Recover from partial node metadata after failed node creation

## Context and Problem Statement

`node create` wrote the generated Lima template into `CODELIMA_HOME/nodes/<id>/` before the node metadata was persisted. If the Lima create step failed, the incomplete node directory remained on disk without `node.yaml`, and later node enumeration failed with `NotFound: node not found`, which blocked the TUI and unrelated node operations.

## Decision Drivers

* Existing homes must recover automatically after upgrading.
* Failed node creation should not leave behind metadata that breaks future commands.
* The fix should keep normal node persistence behavior simple.

## Considered Options

* Leave the current behavior and require manual deletion of broken node directories.
* Make failed node creation clean up partial metadata and ignore incomplete node directories during enumeration.
* Redesign node creation to stage all metadata outside `CODELIMA_HOME/nodes/` until the whole operation succeeds.

## Decision Outcome

Chosen option: "Make failed node creation clean up partial metadata and ignore incomplete node directories during enumeration", because it fixes broken existing homes immediately and prevents the specific regression from recurring without a larger storage refactor.

### Positive Consequences

* `codelima` and TUI startup recover automatically even when older failed creates left partial node directories behind.
* Future failed `node create` attempts no longer poison the node store.
* The change is localized to node creation and node listing.

### Negative Consequences

* Incomplete node directories are skipped silently instead of being surfaced directly to operators.
* The store now has an implicit notion of "directories under `nodes/` that are not valid nodes".
* A future storage redesign may still be desirable if more metadata is staged before persistence.

## Pros and Cons of the Options

### Leave the current behavior and require manual deletion of broken node directories

Keep the existing `NodeCreate` and `ListNodes` behavior unchanged.

* Good, because it avoids changing storage behavior.
* Good, because it keeps node enumeration strict.
* Bad, because a failed create can brick the TUI and unrelated node operations.
* Bad, because recovery depends on users manually inspecting `CODELIMA_HOME`.

### Make failed node creation clean up partial metadata and ignore incomplete node directories during enumeration

Delete the node directory when `NodeCreate` fails before persistence completes, and skip `nodes/<id>/` directories that do not contain `node.yaml`.

* Good, because older broken homes recover immediately after upgrade.
* Good, because future failed creates no longer leave the store in a broken state.
* Bad, because skipped incomplete directories are not explicitly reported yet.
* Bad, because it treats some directories under `nodes/` as non-authoritative.

### Redesign node creation to stage all metadata outside `CODELIMA_HOME/nodes/` until the whole operation succeeds

Write templates and other intermediate artifacts into a separate staging area and only materialize the node directory after the provider call succeeds.

* Good, because it creates a cleaner persistence boundary.
* Good, because it reduces the need for recovery logic in `ListNodes`.
* Bad, because it is a larger refactor than needed for the current regression.
* Bad, because it still would not repair already-broken homes without additional migration logic.
