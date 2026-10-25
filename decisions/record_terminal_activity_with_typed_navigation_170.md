# Record terminal Activity with typed navigation

## Context and Problem Statement

The design retains terminal bells and notices when no TUI is open and opens the
specific session from history. Frontend-only notices duplicate across windows.
Replayed metadata during handoff must not announce old output as new work.

## Considered Options

* Continue deriving history in each frontend.
* Infer actions and deduplication from display text.
* Observe daemon snapshots with explicit baselines, counts and typed targets.

## Decision Outcome

Each daemon terminal publisher compares fresh metadata with its previous fresh
snapshot. Bell and notification counters produce bounded summaries; multiple
events between snapshots report their count and the latest notice text. Native
notifications gain a cumulative counter, so repeated identical text is visible.
Stale snapshots are ignored. Lower recovered counters establish a new baseline.
Legacy metadata without a notification counter uses visible-text transitions.

Adoption waits up to five seconds for a fresh renderer snapshot before starting
new PTY reads. Its metadata seeds the publisher; old bells and notices are not
replayed. An unavailable baseline fails adoption through normal rollback. New
sessions start from zero. Snapshot callbacks enqueue immutable messages without
journal I/O. Shutdown stops publishers and closes producer admission before
draining the owned queue. Frontends keep local notice production only for local
fallback runtimes.
Legacy terminal notification events also enter bounded history instead of
requesting desktop popups.

Activity messages gain optional typed actions, terminal identity and success
outcomes. Host warnings open System/Host, route records open Ports, and committed
updates show success and open System/Daemon. Terminal actions query the existing
session set, match both node and terminal identity, and attach directly without
creating or substituting a session. Navigation, Escape and changed selection
revoke pending attachment. Retained records for ended sessions remain readable.
Older records without actions retain their existing node navigation.

Daemon protocol 17 and renderer protocol 5 carry the extended metadata and
Activity shapes. Existing journal/checkpoint fields remain readable; new fields
are optional. The exact client/worker handshake continues to reject mismatches.

### Positive Consequences

* Terminal history is shared and independent of open frontend windows.
* Repeated notices, restored state and coalesced output have explicit semantics.
* Navigation uses durable identities instead of parsing titles or row positions.
* No notification text can automatically execute commands or open host content.

### Negative Consequences

* Coalesced frames retain counts and latest notice text, not every intermediate body.
* Counter resets and legacy text-only checkpoints cannot recover unobserved history.
* Handoff now requires a fresh metadata baseline within a bounded wait.
* The native queue retains its bounded-overflow and pre-persistence crash limits.
  Pending memory-only events were not transferred by this decision alone;
  [ADR 171](transfer_native_activity_at_daemon_handoff_171.md) adds the bounded
  queue and observation-baseline transfer during live replacement.

## Pros and Cons of the Options

Frontend inference is inexpensive but loses unattended events and duplicates
notices across windows. Parsing display text avoids wire fields but confuses
user-controlled titles with identity. Typed targets and snapshot baselines add
state and protocol fields while preserving exact session ownership and replay
boundaries.

## Links

* [Owned native Activity queue](record_native_route_and_capacity_activity_168.md)
* [Committed update history](record_committed_daemon_updates_in_activity_169.md)
