# Use an identity-based fleet projection for the manager TUI

## Context and Problem Statement

The major-version design replaces a shell-first sidebar with a VM manager.
Directory headings, filtering, sorting, marks and responsive layouts make a
rendered row index unsuitable as node identity. Selecting or refreshing a node
must not create a terminal. Embedded terminals must receive Option/Alt arrows
and every other key except the explicit F6 focus escape.

The reference is version `1792822420-f77f`, preserved under
`plans/tui-vnext/reference/`. This ADR covers the frontend foundation; the
delivery ledger and TODO #57 track the remaining runtime and UI contracts.

## Decision Drivers

* Preserve node IDs, daemon-owned terminal IDs and per-node tab order.
* Keep filtering and layout deterministic and independently testable.
* Keep service calls and runtime probes outside the event loop.
* Share rendering geometry with mouse hit testing and terminal resizing.
* Show unavailable observations explicitly and never substitute mock readings.

## Considered Options

* Extend the seven-line sidebar model with additional modes.
* Introduce a fleet projection and explicit global/detail navigation.
* Replace vaxis and the terminal/session infrastructure.

## Decision Outcome

Chosen option: introduce a fleet projection and explicit navigation while
retaining vaxis, existing service methods, forms and daemon session adapters.
The fleet stores selection and marks by node ID. Its projection derives
selectable node rows and nonselectable directory headings. Missing/nonfinite
metric values sort last in either direction. Filtering does not discard
hidden marks; authoritative record removal prunes them. Metric sorting
temporarily suppresses grouping without changing the grouping preference.

The manager launches without opening a shell. Numbers choose global views,
Enter opens detail, and the detail tabs own their local navigation. Forms,
filters and embedded shell payload have priority over global actions.
Embedded shell focus intercepts only F6. The existing press/repeat/release
claim mechanism prevents a focus-changing key's remaining events from
leaking into its destination. Other terminal input remains protocol payload.

Read results carry the navigation generation that requested them and are
applied only on the event loop. Chrome uses the design's truecolor tokens
when supported, with a 16-color ANSI and reverse-selection fallback. Chrome
colors remain independent from guest terminal palette negotiation.

### Positive Consequences

* Refreshes and sorting preserve selection; headers cannot become action targets.
* Browsing nodes has no shell startup side effects.
* Embedded CLI word navigation no longer conflicts with application shortcuts.
* Existing service/session failure containment remains reusable.

### Negative Consequences

* Tests and manual QA that assert sidebar geometry or automatic shell opening
  must migrate to the new interaction contract.
* The design also needs new daemon contracts (raw attachment, history,
  operation progress and route pins). This frontend change does not complete
  those contracts or the full redesign.

## Pros and Cons of the Options

### Extend the sidebar

* Good: fewer initial changes.
* Bad: couples selection to presentation and retains conflicting shell-first behavior.

### Fleet projection and explicit navigation

* Good: models selection, batch targets and navigation independently of screen width.
* Bad: requires migration of all interactive entry points and their tests.

### Replace the rendering/session infrastructure

* Good: allows a new UI framework.
* Bad: discards mature daemon/renderer reliability work without a design requirement.

## Links

* [Implementation ledger](../plans/tui-vnext/README.md)
* [Deferred and incomplete work](../TODO.md)
