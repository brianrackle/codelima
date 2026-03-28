# Run Blocking TUI Mutations as Background Tasks

## Context and Problem Statement

The TUI already moved dialogs, menus, and selectors into the right pane, but long-running project and node mutations still behaved like a modal operation view. Once a start, stop, create, clone, or delete began, the app streamed output into a dedicated pane and ignored normal navigation and terminal-focus input until the work completed. Now that node and project state can be rendered directly in the primary pane, the remaining question is whether long-running mutations should still monopolize the layout.

## Decision Drivers

* Keep the TUI event loop responsive during slow Lima and guest-command work
* Let operators continue navigating the tree and focusing unaffected node terminals
* Prevent conflicting mutations on the same node or project from overlapping accidentally
* Preserve streamed command output without reintroducing centered overlays or frozen input

## Considered Options

* Keep a single modal operation pane that blocks most input until completion
* Run mutations as background tasks while rendering their transient state in the primary pane
* Allow fully unconstrained concurrent mutations and rely only on backend locks to serialize conflicts

## Decision Outcome

Chosen option: "Run mutations as background tasks while rendering their transient state in the primary pane", because it keeps the shell-first TUI interactive without giving up visibility into in-flight work or conflict protection.

### Positive Consequences

* Start, stop, create, clone, delete, and project-mutation work no longer blocks tree navigation or terminal focus changes.
* The tree and details pane can show transient task state such as `starting`, `stopping`, or `deleting` without waiting for the final reload.
* Each background task can clone the service's `ExecLimaClient` streams, so Lima and guest-command output stays isolated per task instead of sharing one global writer.
* Resource-scoped conflict checks keep obviously incompatible actions on the same node or project from being queued accidentally.

### Negative Consequences

* The footer and available hotkeys still describe the underlying node state, so a conflicting action can remain visible even though the TUI rejects it once pressed.
* Project-scoped global work such as project creation is summarized as a background task instead of owning a dedicated progress surface.
* The TUI now owns a small task-management layer in addition to the existing dialog/menu/selector state.

## Pros and Cons of the Options

### Keep a single modal operation pane that blocks most input until completion

Continue treating long-running mutations as a dedicated transient pane that captures the interaction model.

* Good, because it is simple to reason about and already existed.
* Good, because there is only one streamed-output surface to manage.
* Bad, because unrelated navigation and terminal work freezes while Lima commands run.
* Bad, because it underuses the new primary-pane state rendering that can already show transient status.

### Run mutations as background tasks while rendering their transient state in the primary pane

Track each long-running mutation independently, render task state in the tree/details pane, and only reject overlapping resource scopes.

* Good, because operators can keep using unaffected parts of the TUI during long operations.
* Good, because streamed output can stay attached to the specific task while the visible pane remains available for details or terminals.
* Good, because node and project state become the primary source of feedback instead of a modal progress surface.
* Bad, because the TUI needs explicit conflict checks and task bookkeeping.

### Allow fully unconstrained concurrent mutations and rely only on backend locks to serialize conflicts

Start every requested mutation immediately and let metadata locks or Lima failures sort out ordering.

* Good, because it minimizes TUI-side policy.
* Good, because unrelated work can overlap when backend locking allows it.
* Bad, because conflicting actions look accepted even when they only end up waiting on the same resource.
* Bad, because user intent becomes harder to understand once several incompatible tasks are queued invisibly.

## Links

* Refines [render_transient_tui_views_in_the_right_pane_34.md](/Users/brianrackle/Projects/codelima/decisions/render_transient_tui_views_in_the_right_pane_34.md)
