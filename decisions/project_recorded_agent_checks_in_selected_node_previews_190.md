# Project recorded agent checks in selected-node previews

## Context and Problem Statement

The design's dashboard and Overview show results for both installed agents.
The preview previously labeled the launch profile “checks passed” from an
overall successful start receipt. That cannot establish which agents ran,
whether their version checks used the login user, or whether a clone inherited
the result. Frozen bootstrap steps already record the needed executions.

## Considered Options

* Project the selected node's frozen execution records.
* Probe each guest periodically for agent versions.
* Infer success from the node's start receipt and configured environment names.

## Decision Outcome

Project bounded per-agent records in `fleet.evidence`'s selected-node preview.
Ordinary protocol 27 adds this projection; session 2, renderer 6 and handoff 7
are unchanged. This does not change stored bootstrap state or execution policy.

The projection recognizes the exact built-in login-user version commands.
Other profile validation commands remain explicitly configured profile checks.
Known configured agent names without recorded checks remain unknown; an
environment name or a successful installer alone is insufficient. There are
at most two recognized built-ins and the node's single custom profile. Labels
are bounded and inert; commands, output, errors and attempt IDs are omitted.

Validate command/step coherence on a local copy with `ensureSteps`. A success
requires a recorded attempt, execution node, ordered nonfuture timestamps,
zero exit code and no error. Later checks replace earlier results, including
pending validations on subsequent starts; a pending suffix does not conceal
an earlier failed, canceled, interrupted or active check. Running checks
without a live lifecycle claim are interrupted. Different execution node IDs
remain inherited, including a parent's successful bootstrap on a clone.

The renderer uses the existing selected-node/revision and response-freshness
fences. It labels retained/stale results, shows inherited markers and does no
I/O. Only recorded local login-user version checks support the corresponding
validation label. Overall lifecycle success cannot manufacture agent checks.
Overview refreshes selected-node evidence at both widths, independent of fleet
marks and the hidden dashboard viewport. Other detail tabs cancel this loader;
late responses remain fenced by request identity and visibility.
Compact identity panels wrap complete agent/result pairs and reserve room for
each result when a custom profile name is long. Unattempted and unrecorded
plans are labeled explicitly rather than presented as historical executions.

### Positive Consequences

* Both configured agents can be represented from actual execution evidence.
* Legacy, custom, missing and inherited results have explicit meanings.
* No new guest traffic or persistent observation store is required.

### Negative Consequences

* These are historical checks, not continuous installation/health monitoring.
* Arbitrary custom shell commands cannot prove login-user agent execution.
* Older ordinary clients require an explicit daemon update.

## Pros and Cons of the Options

### Project frozen records

Preserves provenance and avoids new guest work, but cannot detect a binary
removed or changed after its recorded check.

### Periodic guest probes

Could observe later changes, but adds command execution, scheduling, failure
and freshness contracts unrelated to the existing recorded validation flow.

### Infer from overall start

Simple, but can claim success for omitted checks and loses clone provenance.

## Links

* Extends [selected preview evidence](read_selected_fleet_preview_from_retained_metrics_and_runtime_configuration_188.md).
* Validation: `make test-fleet-evidence FLEET_EVIDENCE_TEST_FLAGS=-race` and the
  per-agent validation flow in `QA.md`; final suite/live results are recorded
  in `plans/tui-vnext/README.md`.
