# Quarantine incompatible daemon sessions instead of blocking startup

Status: Accepted

## Context and Problem Statement

`_daemon/session.json` records terminal launch intent so a restarted daemon can
respawn tabs. A session written with schema version 1 caused a version-2 daemon
to exit before binding its socket, leaving the entire CodeLima control plane
unavailable until the operator manually moved the file. How should daemon
startup handle persisted terminal state it cannot restore?

## Decision Drivers

* Node metadata, VM disks, and mounted workspaces must remain untouched.
* Recoverable terminal restoration state must not prevent daemon startup.
* Unsupported or malformed input should remain available for diagnosis.
* `daemon.restore: forget` must honor its name without interpreting old state.
* Recovery must be atomic, observable, and safe across repeated starts.

## Considered Options

* Reject incompatible or malformed sessions and require manual intervention.
* Implement an in-place migration for every historical session version.
* Quarantine unrestorable sessions, initialize the current schema, and continue.

## Decision Outcome

Chosen option: "quarantine unrestorable sessions, initialize the current
schema, and continue", because daemon session state is a recoverable cache of
terminal launch intent rather than authoritative node or VM state.

With `restore: respawn`, malformed JSON is renamed beside the live path with a
`corrupt` suffix, and an unsupported schema is renamed with an
`unsupported-v<version>` suffix. The name also includes a UTC timestamp and a
unique component so recovery never intentionally overwrites prior evidence.
CodeLima then atomically writes an empty session using the current schema and
logs the original and quarantine paths. A rename or replacement-write failure
remains fatal because proceeding would either lose the evidence or leave no
durable current session.

With `restore: forget`, CodeLima atomically replaces `session.json` before
reading or validating it. No quarantine is created because explicit discard is
the configured behavior.

### Positive Consequences

* Old or malformed terminal state no longer bricks the daemon or TUI.
* The rejected file remains available for diagnosis under `restore: respawn`.
* Authoritative CodeLima metadata retains its strict corruption and schema
  checks; this recovery policy is limited to disposable daemon session state.
* A single session-version constant is used by persistence, snapshots, and
  live handoff validation.

### Negative Consequences

* Tabs from an unsupported or malformed session are not respawned.
* Quarantine files accumulate until an operator removes them.
* Operators must inspect the daemon log to learn that recovery occurred.

## Pros and Cons of the Options

### Reject and require manual intervention

* Good, because no state is changed automatically.
* Bad, because a disposable cache prevents all daemon-backed functionality.
* Bad, because recovery requires knowledge of CodeLima's private storage.

### Migrate every historical version

* Good, because compatible terminal launch intent could be retained.
* Bad, because not every future schema change will have an unambiguous mapping.
* Bad, because migrations expand the permanent compatibility and test surface
  for ephemeral state.

### Quarantine, reset, and continue

* Good, because startup availability is restored without destroying evidence.
* Good, because it works for both unknown future versions and malformed JSON.
* Bad, because automatic recovery gives up terminal restoration for that file.

## Links

* Refines [ADR 66](version_daemon_session_persistence_66.md).
* Preserves daemon ownership from [ADR 64](daemon_owned_terminal_runtimes_64.md).
