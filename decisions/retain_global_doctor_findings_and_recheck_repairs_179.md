# Retain global Doctor findings and re-check repairs

## Context and Problem Statement

The System reference counts open Doctor warnings in the global header, shows
check time and selectable results, and exposes re-check and repair controls.
The previous view-local snapshot disappeared on navigation. Repair reports
also mixed completed quarantine messages with unresolved warnings, and a
long-lived service validated its startup settings instead of the current file.

## Considered Options

* Count the current view's transient Doctor report.
* Run Doctor synchronously while drawing the header.
* Retain an independent asynchronous snapshot and re-check after repair.

## Decision Outcome

Retain Doctor state independently of the active manager view. Start a read-only
check at startup. Periodic checks use a one-minute interval; entering System
or pressing C requests an immediate re-check.
One worker runs at a time. Read checks have a 15-second context deadline;
repairs have a one-minute deadline. A repair requested during a read check is
coalesced into one queued repair. Periodic checks never request repair.
Completions use the existing guaranteed event path and a generation fence.
Navigation preserves results; application shutdown cancels outstanding work.

The header counts unresolved report warnings, non-ok checks and observed host
allocation warnings. Capacity comes from the existing global runtime/host
snapshot, so filtering or directory scope cannot hide overcommit. Missing
initial results and failed refreshes use a question-mark badge. Failed refreshes
retain the last successful report with its check time and an explicit stale
notice instead of claiming there are zero findings.

R uses the existing Doctor repair service and locks. Unreadable records retain
the existing non-destructive quarantine behavior; built-in metadata and stale
claims use the existing repair semantics. A fresh read-only check follows even
a partially failed repair. Completed repair messages go to Activity; they do
not become open findings merely because Doctor returned them in Warnings.
A failed re-check reports that the remaining count is unknown. Refresh the
fleet after repair so moved records stop appearing there.

Doctor validates a fresh SettingsRead snapshot on every pass without mutating
shared service configuration or rewriting malformed authored settings during
read-only checks. Existing CLI result types and daemon/storage protocols are
unchanged.

Selectable check rows preserve identity on refresh, use severity colors and
expose keyboard/mouse details, re-check and repair actions. A reusable immutable
text view wraps and scrolls full messages without I/O. Compact layouts keep
these controls reachable while the existing panel selector switches surfaces.

### Positive Consequences

* Global warning counts remain meaningful across navigation and repair.
* Slow checks cannot block rendering or multiply on repeated refreshes.
* Quarantine outcomes remain inspectable without being counted as unresolved.
* Long paths and messages can be read at both wide and compact sizes.

### Negative Consequences

* Background findings can be up to one minute old; C forces a fresh check.
* Doctor and host allocation snapshots have separate observation times.
* An interrupted repair may have completed some atomic steps; re-checking
  exposes remaining findings, and existing repair operations remain repeatable.

## Validation

Use make test-system-doctor for global/stale counts, single-flight/deadline/
shutdown behavior, queued repair, failed re-checks, current settings, exact
quarantine bytes and keyboard/mouse detail reachability. Full verify, race,
integration and live wide/compact qualification are recorded in the design
ledger and QA.md. Complete System daemon controls and final artboard/native
qualification remain part of the redesign.

## Links

* [System reference](../plans/tui-vnext/reference/System.dc.html)
* [Host capacity](observe_global_host_capacity_outside_manager_rendering_153.md)
