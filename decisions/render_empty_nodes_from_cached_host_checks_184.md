# Render empty Nodes from cached host checks

## Context and Problem Statement

The first-run artboard provides scoped creation, actual template defaults and
host readiness. The old empty table exposed one generic line and creation used
the process working directory even when the manager showed a different scope.
Rendering must not run host checks or read metadata on the event loop.

## Considered Options

* Keep the empty table and send users to other views for all setup information.
* Run prerequisite checks while rendering the empty guide.
* Render retained Doctor results and asynchronously loaded template defaults.

## Decision Outcome

Use retained Doctor results and the existing manager-data loader. The empty
Nodes guide applies only to an empty inventory with no active filter; zero
matches remain a searchable table. Template defaults load when the guide opens
or the inventory becomes empty. `R` rechecks the host without repair and reloads
the defaults. Rendering uses cached values and preserves explicit pending/error
states. Warnings do not block creation; failed checks and unavailable results
explain why the guide's create/start action cannot proceed. System opens its
Doctor panel from the guide. Keyboard, palette and mouse creation share the same
entry point, and repeated `R` events cannot start additional checks.

The creation directory is the displayed scope, falling back to the remembered
launch directory in all-node mode. Explicit caller-provided directories retain
precedence. The launch directory is captured before rendering; path validation
continues in the existing creation workflow.

Wide terminals use a guide plus host-readiness panel. Compact terminals combine
them into one scrollable panel. Long errors remain reachable with Page Up/Down
or scrolling, including overflow from the wide secondary panel. The display
does not invent credential presence, download sizes or startup durations.

### Positive Consequences

* First-run actions and displayed defaults describe the actual scope and host.
* Existing asynchronous checks and validation remain authoritative.
* Missing information stays visible and complete details remain reachable.

### Negative Consequences

* Initial creation through the empty guide waits for prerequisite checks.
* Template defaults are a snapshot; `R` reloads changes made elsewhere.
* Wide and compact layouts require independent interaction qualification.

## Pros and Cons of the Options

The existing one-line table is simple but does not deliver the design. Checks
during drawing would be fresh but block input and duplicate Doctor work. Cached
checks and explicit reload preserve responsiveness at the cost of representing
pending and stale observations explicitly. No wire or storage format changes.

## Links

* [First-run design](../plans/tui-vnext/reference/NodesEmpty.dc.html)
* [Delivery ledger](../plans/tui-vnext/README.md)
