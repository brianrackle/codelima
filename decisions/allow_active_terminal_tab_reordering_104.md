# Allow active terminal tab reordering

## Context and Problem Statement

CodeLima terminal tabs had a stable creation order and shortcuts for switching, opening, and closing, but operators could not change their left-to-right arrangement. A local-only TUI swap would be lost when the daemon list was restored, persisted, or handed to a replacement daemon, so tab movement needs one authoritative ordered state.

## Decision Drivers

* `Option+Shift+Left` and `Option+Shift+Right` should move the active tab one position.
* Moving a tab must not change which terminal is active.
* Reordered tabs must survive TUI restart, daemon restart, and live handoff.
* Tabs from different node targets must retain their own relative order.
* Runtime map iteration must never determine visible or persisted order.

## Considered Options

* Reorder only the current TUI's per-target tab slice.
* Rewrite terminal creation timestamps to encode the new order.
* Keep an explicit daemon-owned terminal-ID order and persist its sequence.

## Decision Outcome

Chosen option: "keep an explicit daemon-owned terminal-ID order and persist its sequence", because the daemon already owns terminal lifetime and every durable tab-order boundary.

The daemon keeps an ordered terminal-ID slice beside its runtime map. `terminal.move` swaps the named terminal with the adjacent terminal for the same target, skipping terminals for other targets in the global sequence. The request accepts only `delta: -1` or `delta: 1`; either edge is a successful non-wrapping no-op. The TUI updates its per-target projection only after the daemon mutation succeeds and leaves the active tab ID unchanged.

Session version 2 already stores terminals as an ordered array, so no session schema change is needed. Restore consumes that array without re-sorting, and live handoff imports the manifest's session order. Creation time plus terminal ID remains a deterministic fallback for legacy or test state that lacks an explicit ordered-ID slice.

Adding the `terminal.move` request changes the private daemon request surface, so the exact-match daemon protocol advances from 3 to 4. The method requires input ownership like other terminal mutations.

### Positive Consequences

* Operators can arrange tabs without closing and reopening terminal runtimes.
* The active terminal remains stable while its visible position changes.
* Reordered state is shared by list, snapshot, persistence, restart, and handoff boundaries.
* Existing session files remain compatible because array order was already preserved.

### Negative Consequences

* The daemon maintains an ordered slice in addition to its runtime map.
* Moving a tab writes the recoverable session file.
* Ordinary clients and daemons from protocol 3 and protocol 4 cannot connect without an update.

## Pros and Cons of the Options

### Reorder only in the current TUI

* Good, because it is a small client-only change.
* Bad, because redraw, restart, another client, or daemon handoff can restore the old order.

### Rewrite creation timestamps

* Good, because the previous daemon ordering code could remain unchanged.
* Bad, because creation time would stop describing creation and equal timestamp handling would remain awkward.

### Keep explicit daemon-owned order

* Good, because one owner controls every order boundary.
* Good, because the persisted terminal array can carry the order without a schema migration.
* Bad, because map and ordered-slice membership must be maintained together.

## Links

* Refines [ADR 88](preserve_daemon_tab_order_at_state_boundaries_88.md).
* Refines [ADR 66](version_daemon_session_persistence_66.md).
* Preserves the scoped tab model from [ADR 52](scope_tui_terminal_tabs_to_focused_target_with_explicit_option_controls_52.md).
