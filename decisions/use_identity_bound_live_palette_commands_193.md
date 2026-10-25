# Use identity-bound live palette commands

## Context and Problem Statement

The Palette artboard requires grouped fuzzy results, matching spans, context,
recent commands and per-node actions. A palette can remain open across fleet
refreshes; using the current row or selection after that refresh can target a
different node. Directory starts also need to distinguish Start from Retry.

## Considered Options

* Keep a static flat list that invokes current-selection keyboard handlers.
* Rebuild descriptive commands from snapshots and retain canonical identities.
* Introduce a separate palette execution service and durable history store.

## Decision Outcome

Build NODES, ACTIONS and GO TO commands from frontend snapshots. Search labels,
context and shortcuts with one Unicode-aware matcher whose rune positions also
drive mint underlined spans. Use the existing modal host, theme and text input;
compact terminals place context on a second line. Size to the result count up
to a bounded height. Count results, scroll selected
rows into view and rebuild mouse regions with command IDs on every draw.

Preserve the selected ID when refreshing. If it disappears, retain an explicitly
unavailable result until the user selects another command or changes the query.
Revalidate a clicked or entered identity before invocation and again before a
lifecycle confirmation submits. Per-node commands call the shared action owner
with an explicit entry; selection changes and marks cannot redirect them.
Go-to commands and shell chooser callbacks likewise retain the node ID.

Directory starts capture eligible members and their Start/Retry action in a
review, then validate every member before submitting the first action. They use
the existing sequential service dispatch, preserve unrelated marks and stop at
the first error. Later arrivals cannot enlarge an accepted review. This does
not add a daemon-owned batch queue; that contract remains in TODO #57.

Keep the latest 20 unique accepted command IDs in the frontend session. An empty
query shows still-available recents; without any, show the current catalogue and
say so. Opening a confirmation is an accepted command, not evidence that the
operation later succeeded. History is intentionally not persisted. Cached memory
allocation warnings require complete observations and never claim OS free RAM;
session-impact hints distinguish guest, unclassified and disconnected evidence.

Paste is input, repeats move/edit, and Enter/Tab/Esc activate only on a press.
The existing overlay ownership layer remains responsible for inherited events.
No daemon, storage or protocol changes are made; ordinary/session/renderer/
handoff protocols remain 27/2/6/7.

### Positive Consequences

* The palette follows the artboard while showing actual scoped node context.
* Refresh, stale clicks and confirmation delays cannot silently retarget actions.
* Existing lifecycle, error, confirmation and session owners remain shared.

### Negative Consequences

* Rebuilding the catalogue adds work on each refresh and grows with fleet size.
* Context and long labels may be truncated; Tab completes the full command.
* Recent history ends with the frontend, and batch continuation remains limited
  by the existing sequential frontend queue.

## Pros and Cons of the Options

The static list is simple but lacks per-node context and can redirect commands
through mutable selection. Snapshot commands require identity checks and
rendering adapters but keep execution with proven owners. A separate execution
service and durable history would add compatibility and storage work without
being required for this overlay; durable batch ownership is tracked separately.

## Links

* [Palette artboard](../plans/tui-vnext/reference/Palette.dc.html)
* [Delivery ledger](../plans/tui-vnext/README.md)
