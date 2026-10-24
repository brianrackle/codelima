# Bind lifecycle detail and queued attachment to receipts

## Context and Problem Statement

Starting and Failed views need the current node's execution evidence even when
Activity is displaying older history. Attachment requested during startup must
not follow an unrelated operation or open after the user navigates away.

## Decision Drivers

- Preserve observed phases, output and command provenance across frontends.
- Cancel the admitted startup rather than submit a conflicting stop.
- Require successful execution and confirmed readiness before queued attachment.
- Keep late asynchronous responses from changing the current selection.

## Considered Options

- Derive detail from the current Activity page and attach on status alone.
- Query the latest admitted start and retain a local, identity-bound intent.
- Persist attachment requests as daemon operations.

## Decision Outcome

Protocol 13 adds `operation.latest`, resolving node ID/slug and returning the
latest retained start/retry receipt by admission time. Later stop receipts and
history pagination cannot hide it. A missing receipt leaves legacy evidence
explicitly unavailable. Existing bounded retention applies.

Node detail polls this query asynchronously, fencing responses with node ID,
navigation generation and request serial. Starting shows observed phases and
bounded output, skips Metrics navigation, and sends cancellation to the exact
operation ID. Failure pairs recorded command evidence with separately observed
VM state. A newer failure outside bootstrap/validation takes precedence over
older command failures. Source edits use frozen source identities and affect
future nodes; retained attempt IDs select the exact output.
Retained failed/interrupted receipts remain visible after runtime setup rolls
back the node's coarse status. The view keeps that status and VM observation
separate; a recovered interruption cannot masquerade as an active start merely
because the durable node still says provisioning.

Queued attachment belongs to one frontend and one node/operation pair. Escape,
leaving the detail view, failure, cancellation or superseding work revokes it.
A successful matching receipt is necessary but insufficient: the selected node
must also be running, bootstrap-complete and observed running. An overlay delays
attachment. The intent is consumed before opening the configured shell mode,
preventing duplicate opens on subsequent refreshes.

### Positive Consequences

- Reconnecting detail views recover real progress without Activity coupling.
- Navigating away cannot unexpectedly attach a terminal on completion.
- Failed VMs remain available for inspection without claiming startup succeeded.

### Negative Consequences

- Exact-match clients require a protocol upgrade.
- Detail adds a bounded polling request and independent freshness fences.
- Attachment intent is deliberately lost when the frontend exits.
- Static Lima mappings and dynamic routes still require a separate readiness
  design; the failure UI does not claim ports are held before that exists.

## Pros and Cons of the Options

### Activity/status inference

Avoids a query but loses evidence outside a history page and cannot distinguish
completion of the requested startup from unrelated work.

### Latest receipt and local intent

Reuses durable execution while retaining the user's current navigation intent;
requires independent refresh and identity checks.

### Durable attachment operation

Could survive frontend exit but cannot safely choose which later terminal to
attach without a separate ownership protocol and renewed user intent.

## Links

- Extends [ADR 161](daemon_owned_node_operations_and_retained_history_161.md).
- Uses [ADR 163](retain_observed_lifecycle_phases_and_boot_output_163.md).
