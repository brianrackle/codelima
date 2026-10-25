# Own node creation and first startup in one daemon receipt

## Context and Problem Statement

The create-node design offers “start now” and optional attachment after readiness.
Starting from a frontend callback after creation would lose that intent if the
frontend quit or disconnected between the two operations. Creation also reserves
a slug before the node has an identity that existing-node operations can target.

## Considered Options

* Chain independent create and start calls in the TUI.
* Persist a separate workflow scheduler for two child receipts.
* Extend the existing create admission to own the optional first startup.

## Decision Outcome

Add an optional `start` field to `NodeCreateInput`. The CLI exposes `node create
--start`; ordinary CLI and SDK creation keep their existing create-only default.
The daemon executes creation and startup under one admission and cancellation
context. Its phase list contains Create VM followed by the existing seven
startup phases. After creation releases its service liveness token, publish the
created UUID in the same durable receipt before beginning startup side effects.

Existing-node admissions also reserve their node's slug. This excludes competing
operations during the gap between metadata publication and receipt identity
publication. Once bound, the create operation retains both slug and UUID keys.
Cancellation after creation uses the existing independent, bounded stop cleanup;
a stop failure cannot be reported as successful cancellation. Failed first boot
retains the created identity so node detail can show the failure.

Include create-and-start receipts in latest-start queries and the existing TUI
readiness/attachment gate. Summary lists preserve only the startup flag, omitting
the full create input. Activity describes the combined intent explicitly.
Ordinary daemon protocol becomes 18; renderer protocol 5 and handoff version 6
are unchanged. Active lifecycle operations continue to exclude live replacement.

### Positive Consequences

* Frontend exit cannot drop an accepted first-start request.
* Cancellation, progress, retries and inspection use one stable receipt.
* No second workflow store or scheduling mechanism is needed.
* Existing create-only behavior remains available.

### Negative Consequences

* A combined request stays active until readiness, failure or cancellation.
* Clients must distinguish create-only from create-and-start summary records.
* The TUI still needs its own transient navigation/attachment intent; leaving
  the view must cancel attachment without canceling daemon-owned work.

## Validation

The create-node suite exercises detached admission, real-socket frontend
disconnect, identity publication before boot, conflicts before identity binding,
duplicate delivery, creation/boot failures, cancellation cleanup, CLI defaults
and lifecycle attachment gating. The new live QA flow covers a disposable real
guest and must be completed alongside the create-form integration.

Full verify, race and integration suites passed. Live CLI/TUI inspection at
150×38 and 80×24 confirmed rejected-input receipt history, filtering and copy;
all scratch state for that check was removed. Real first-boot success and
cancellation remain unqualified until the form's guest verification.

## Links

* [Create-node design](../plans/tui-vnext/reference/CreateNode.dc.html)
* [Daemon operation ownership](daemon_owned_node_operations_and_retained_history_161.md)
