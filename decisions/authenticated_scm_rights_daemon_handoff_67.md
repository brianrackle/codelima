# Transfer live PTYs with authenticated SCM_RIGHTS handoff

Status: Accepted

## Context and Problem Statement

Replacing the daemon normally closes PTY masters and terminates live agents. An update must transfer ownership without creating two readers, losing boundary bytes, or killing children when import fails.

## Decision Outcome

`daemon update` quiesces each actor, drains writes, captures an 8 KiB replay tail, duplicates its PTY master, and stops the old read pump. The old daemon authenticates a new binary over a private `unixpacket` socket using both a random token and the kernel-reported peer UID, then sends a versioned manifest plus PTY descriptors in batches via `SCM_RIGHTS`. The peer check preserves the same-user boundary on shared filesystems that ignore socket chmod. The import process rebuilds actors without reading until commit, then takes the public socket/lock and acknowledges. The old daemon remains alive until that acknowledgement. Any pre-commit failure kills the importer, restores the old descriptors and actors, and reports `daemon.update_failed`.

### Consequences

* Live shells and terminal IDs survive an update; a resize forces repaint.
* The mechanism is Unix-only. Platforms without descriptor passing fall back to the session respawn contract.
* PTY masters are nonblocking so quiesce can stop reads deterministically and preserve boundary output.
