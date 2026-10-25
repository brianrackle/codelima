# Use identity-scoped Overview panels over shared observations

## Context and Problem Statement

The Overview artboard combines identity/resources with selectable sessions,
routes and recent events. The initial detail screen had aggregate labels and
loaded a complete event log on navigation. Row positions cannot safely identify
a session or route when asynchronous snapshots reorder or remove rows.

## Considered Options

* Give Overview its own inventories and polling loops.
* Render existing observations, retaining selections by canonical identity.
* Keep aggregate labels and send every action to another screen.

## Decision Outcome

Use existing session ownership, Ports snapshots and selected fleet evidence.
Overview owns only node-scoped focus, scroll positions, selected session key,
selected route ID and rendered mouse regions. Reordering preserves selection.
A removed selection remains unavailable until explicit row navigation; an
action never substitutes another row. End-session confirmation captures the
session key, target and terminal ID and rechecks all three on submission.
Closing uses the existing session store; status says an end was requested.

Ctrl+Up/Down cycles Sessions, Ports, Recent events, Identity and Resources.
Up/Down moves within that panel. Tab retains its detail-tab meaning. Wide
screens show all five panels; compact screens keep Identity (or focused
Resources) beside the last focused right-hand panel. Links open the existing
Spec, Metrics, Ports and Logs owners. Starting/Failed screens keep their
existing lifecycle controls.

Enter attaches the selected daemon terminal by ID; t/T explicitly creates a
fresh guest/host shell and attaches here. Session age uses TerminalState's
existing CreatedAt, retained on adoption and resynchronization. Kind remains
shell/host; terminal titles do not establish which agent is running.

Tab-change pushes now request a coalesced, asynchronous terminal inventory
read instead of merely redrawing the old order. At most one read is in flight;
a newer mutation or full synchronization fences its reply. Apply order only
to known tabs and retain new tabs absent from the reply. This does not adopt
or remove session membership, which remains owned by created/closed events
and full synchronization. A failed read retains order and reports the failure.
Explicit shell-open completion selects the returned terminal only when its
node still owns the visible Overview.

Opening a route shares the fleet action's current-observation, authorization,
readiness, node, HTTP and live-route checks. Copying a retained URL is permitted
while the panel labels stale data. The selected recent projection is bounded
to six records; the dashboard still renders at most three. This changes no
wire shape: ordinary/session/renderer/handoff versions remain 27/2/6/7.
Overview no longer calls NodeLogs. Unknown runtime facts remain unknown.

### Positive Consequences

* The design's controls operate on stable, reviewable identities.
* Rendering and navigation introduce no independent guest or filesystem reads.
* Narrow windows preserve access to every panel and the recorded agent checks.

### Negative Consequences

* Compact windows require panel switching and long identity/resource panels
  can require scrolling.
* The session store's close request remains asynchronous; disappearance of a
  local row alone does not prove the daemon accepted the close.

## Pros and Cons of the Options

Independent polling could tailor every field, but would duplicate owners and
introduce conflicting stale-state and lifecycle rules. Shared observations
reuse those contracts and keep bounded work, at the cost of showing explicit
unknown fields where the runtime has no evidence. Aggregate labels are simple
but cannot fulfill the artboard's selected-session and selected-port controls.

## Links

* [Overview reference](../plans/tui-vnext/reference/NodeOverview.dc.html)
* [Verification flows](../QA.md)
