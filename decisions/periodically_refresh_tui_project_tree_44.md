# Periodically refresh the TUI project tree

## Context and Problem Statement

The TUI already reloads the project tree after CodeLima-owned mutations, but node runtime status and metadata can also change outside the current TUI process. The tree should stay aligned with the same Lima-reconciled read surface used by `project tree` and `node list` without requiring the operator to restart the TUI.

## Decision Drivers

* Keep TUI node status and newly added or removed metadata current.
* Reuse the existing `Service.ProjectTree` reconciliation path.
* Avoid blocking terminal input while refresh work is running.

## Considered Options

* Refresh only after TUI-owned mutations.
* Add a manual refresh key.
* Periodically refresh the tree through the TUI event loop.

## Decision Outcome

Chosen option: "Periodically refresh the tree through the TUI event loop", because it keeps the tree current automatically while preserving the existing service-owned runtime reconciliation behavior.

### Positive Consequences

* External node status changes and metadata edits appear in the TUI without restart.
* The refresh path preserves selection, expansion state, and live terminal sessions.
* Refresh work is posted back as TUI events instead of running directly on the render path.

### Negative Consequences

* The TUI performs recurring read work while open.
* Refresh errors are intentionally quiet to avoid overwriting user-action status messages.
* Very slow runtime reads can still delay the next refresh result.

## Pros and Cons of the Options

### Refresh only after TUI-owned mutations

Continue reloading only when a TUI action creates, updates, starts, stops, clones, or deletes a resource.

* Good, because the implementation stays simple.
* Bad, because runtime state can drift when `limactl` or another CodeLima process changes nodes.

### Add a manual refresh key

Expose a keybinding that reloads the tree on demand.

* Good, because it gives users explicit control.
* Bad, because stale state remains the default and users need to notice when a refresh is needed.

### Periodically refresh the tree through the TUI event loop

Post lightweight refresh ticks, run `Service.ProjectTree`, and apply the result as a TUI event.

* Good, because the visible tree stays current with low operator effort.
* Good, because the existing ProjectTree read contract remains the source of truth.
* Bad, because the TUI now owns a small recurring background task.

## Links

* Extends [Use Lima as the runtime-status source for read surfaces](use_lima_as_runtime_status_source_for_read_surfaces_37.md)
