# Project observed operation progress into Activity

## Context and Problem Statement

Activity's running rows showed only the current phase ID. The design requires
current work, a progress bar and elapsed time, while full phase records and
command output are too large to copy into every history refresh.

## Considered Options

* Fetch complete receipts separately for each running row.
* Estimate a percentage from elapsed time.
* Project bounded progress from the phases already retained by the daemon.

## Decision Outcome

Protocol 15 adds an optional progress summary to operation list records. It
contains completed and total phase counts, the current phase label, and bounded
plain-text detail. Succeeded and explicitly skipped phases count as completed;
pending, running, canceled, failed and interrupted phases do not. No percentage
or ETA is inferred from time. Full receipt phase records remain authoritative,
and list responses still exclude output bodies and phase arrays.

Bootstrap detail includes the recorded step index, frozen source and command.
Wide Activity rows show node, operation, current work, phase progress and elapsed
time in columns. Compact rows use two lines, with selection scrolling and mouse
targets accounting for both. Receipts without phase evidence explicitly show
unavailable progress. Completed history includes duration only when both start
and finish were observed; interrupted receipts do not invent a finish time.

### Positive Consequences

* Activity shows real progress without per-row RPCs or UI-thread file reads.
* Skipped and unknown work retain their distinct meanings.
* Wide and compact views keep progress and commands reachable.

### Negative Consequences

* A phase fraction measures completed boundaries, not remaining wall time.
* Older receipts cannot display details they never recorded.

## Pros and Cons of the Options

Per-row receipt reads preserve full detail but amplify polling and copy command
output unnecessarily. Time estimates are cheap but misleading. A bounded
projection reuses durable evidence and fits the existing paginated API, with
an explicit protocol bump for its wire shape.

## Links

* [Observed lifecycle phases](retain_observed_lifecycle_phases_and_boot_output_163.md)
* [Shared Activity history](share_bounded_activity_messages_with_receipt_history_165.md)
