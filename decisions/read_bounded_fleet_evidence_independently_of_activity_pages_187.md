# 187. Read bounded fleet evidence independently of Activity pages

## Context and Problem Statement

The home artboards show failed bootstrap steps, stop age and recent events even
before opening detail. Activity can be paged into older history, and a later stop
must not conceal the failed start. Fetching full receipts or Spec for every row
would copy command/output bodies and couple navigation to guest preparation.
Marked-node warnings also need to distinguish guest sessions, host sessions and
observed work without inventing activity from a terminal title.

## Decision Drivers

* Preserve attempt, node and phase provenance across failure, retry and cloning.
* Keep rendering free of filesystem, daemon and guest I/O.
* Bound requests and response bodies; isolate unavailable sources.
* Report positive terminal integration evidence and explicit unknowns.

## Considered Options

* Add a bounded daemon projection for visible rows and selected recent history.
* Reuse whichever Activity page and selected Spec happen to be loaded.
* Fetch full receipts and Spec for every node on each redraw.

## Decision Outcome

Add query-lane `node.fleet_evidence` in ordinary protocol **24**. Session and
handoff versions remain unchanged. A request contains at most 128 canonical node
IDs and an optional recent-node ID from that set. The read uses existing retained
operation summaries, local node/bootstrap metadata and the Activity journal.
It never prepares or probes a guest. Operation/journal initialization shares the
daemon replacement barrier because opening owners may recover persisted state.

Each node receives a bounded latest-start summary, confirmed stop timestamp,
applicable bootstrap/validation step evidence, and a per-node error. Latest start
is chosen by admission time independently of completion ordering and later stop
receipts. Step kind, attempt start time and execution node must match; an older
bootstrap failure cannot explain a newer boot failure or a clone's execution.
Step totals exclude validation from bootstrap counts. Commands, output bodies
and full create inputs are excluded. Confirmed stop age is labeled **last stop**,
not inferred as the current VM's uptime. The selected recent list takes the three
newest node-scoped Activity records with no dependence on a frontend page cursor.
Corrupt bootstrap or journal metadata leaves unrelated evidence available with
explicit errors.

The frontend requests visible rows and the selected node asynchronously, at
most once per second for an unchanged projection. Tall windows split into
bounded requests under one five-second deadline. Navigation, selection, scope
and request identity fence completions; leaving the fleet, disconnecting or
closing cancels the read. Failed reads retain same-node observations as stale.
Running Activity from a newer operation takes precedence over an older failed
step. The wide preview reserves room for sessions, routes and Recent.
Recovered interrupted receipts intentionally have no finish time. A read-only
lifecycle-token check and the observed node metadata revision distinguish
unclaimed in-flight metadata from a live direct operation. The frontend only
classifies the same revision as failed when the latest retained start failed or
was interrupted and no live claim exists. Newer metadata or a newer running
operation takes precedence; a live claim suppresses older terminal evidence.

Session activity uses positive integration evidence: normal/indeterminate OSC
progress means working, explicit error/paused progress retains that state, and
a prompt marker means at prompt. Without evidence activity stays unknown;
titles do not prove busy/idle state and elapsed idle time is not invented.
Marked warnings count guest sessions that stopping interrupts, host sessions
separately, and unclassified/unknown observations, including hidden marks.

### Positive Consequences

* Opening Activity or paging its history cannot change the fleet's recent list.
* Failed-step attribution remains inspectable without serializing commands.
* Hidden marks cannot hide associated guest/host sessions or observed work.

### Negative Consequences

* Visible failed/starting nodes need bounded local bootstrap reads on polling.
* A stale or corrupt source provides less detail until it recovers.
* Prompt/progress integration is optional; unsupported applications remain
  unknown. Durable idle timing and complete native/session qualification remain
  tracked in TODO #57 along with the broader preview fidelity audit.

## Pros and Cons of the Options

### Bounded independent projection

Preserves provenance and responsiveness, at the cost of one cached-owner query
plus local metadata reads and explicit asynchronous navigation tests.

### Incidental Activity/Spec cache

Needs no new read but can label old events as recent and another attempt's
bootstrap as the cause of a new failure; rejected.

### Full per-node receipts and Spec

Contains the evidence but unnecessarily copies commands, output and template
comparisons and scales poorly with redraws; rejected.

## Links

* [Shared fleet observations](project_fleet_observations_from_shared_caches_186.md)
* [Design ledger](../plans/tui-vnext/README.md)
* [QA](../QA.md)
