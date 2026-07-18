# Version daemon session persistence from its first schema

Status: Accepted

## Context and Problem Statement

Daemon crashes and normal restarts lose in-memory tab structure even though PTYs cannot survive a process crash. Restoration needs an explicit ceiling and a schema that can evolve.

## Decision Outcome

Persist `_daemon/session.json` atomically as schema version 1. It records durable terminal IDs, target/tab identity, kind, label, working directory, argv, dimensions, and creation time. `daemon.restore: respawn` recreates shells after restart; `forget` discards them. It never claims to resurrect a PTY after a host reboot or daemon crash.

### Consequences

* Tab identity and launch intent survive crashes.
* Respawn is visible process replacement; uninterrupted processes require ADR 67's live handoff.

## Links

* Refined by [ADR 74](quarantine_incompatible_daemon_sessions_74.md), which
  prevents unsupported or malformed session caches from blocking daemon
  startup.
