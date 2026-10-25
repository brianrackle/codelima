# Render Activity in cell-aligned columns with shared input actions

## Context and Problem Statement

The Activity reference uses aligned history fields, separately colored status
symbols, full-row selection and actions beside their records. The existing
renderer concatenated fields and colored whole lines, so long names displaced
messages and selections ended early. Keyboard hints lacked mouse targets.

## Considered Options

* Keep concatenated text and add padding at a few widths.
* Allocate cell columns and derive controls from the rendered windows.
* Build a second general-purpose table and input framework.

## Decision Outcome

Use the existing vaxis windows and theme to allocate columns before drawing.
At wide widths reserve a labeled action column; compact history uses a one-cell
Enter hint and running operations retain two-line measured progress. Request
symbols use the existing canonical `operation ID + :requested` identity contract;
no wire or journal schema changes. Dates appear when a page spans earlier local
days, and a separate range identifies the visible filtered page rows.

Fill selected rows across the panel, applying their background or ANSI reverse
attribute to each field while preserving semantic foregrounds. Put titles and
severity choices on panel borders and highlight the selected panel. Build mouse
regions from window origins during each draw. Mouse controls reuse key dispatch
or typed row navigation/cancellation; row bodies only select. Overlays retain
input priority, and filter footers use the same Enter/Escape ownership as keys.
Color the running header count amber independently of the active view label.

### Positive Consequences

* History remains scannable across long and Unicode node names.
* Visible actions and their targets share layout and existing navigation logic.
* ANSI and truecolor preserve selection and status distinctions.
* Retained older records and scroll positions are explicit.

### Negative Consequences

* Compact row actions need the footer or Enter to explain their destination.
* Width-dependent layouts and input regions require regression and live checks.
* Other views still need their own footer-control qualification.

## Validation

`make test-activity` covers column alignment, symbols, both color modes,
full-row selection, date/range context, compact/wide mouse controls, typed row
actions and header counts, alongside existing progress, pagination and shared
history tests. QA.md specifies live checks at 150×38 and 80×24.

## Links

* [Activity reference](../plans/tui-vnext/reference/Activity.dc.html)
* [Typed navigation](record_terminal_activity_with_typed_navigation_170.md)
