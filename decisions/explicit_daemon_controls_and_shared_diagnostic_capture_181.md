# Explicit daemon controls and shared diagnostic capture

## Context and Problem Statement

The System design offers live update, restart, daemon logs and freeze diagnosis.
These operations can block on daemon replies or process shutdown. The existing
CLI update supports a legacy restart fallback, which cannot satisfy a TUI control
promising to keep sessions. The repository diagnostic shell script is unavailable
to installed binaries.

## Decision Drivers

* Keep navigation and evidence access responsive during lifecycle work.
* Preserve live-update guarantees and make restart impact explicit.
* Capture failures without recovery, input takeover or repository dependencies.
* Prevent a stale restart request from stopping a replacement daemon.

## Considered Options

* Shared Go lifecycle and evidence helpers with independent frontend workers.
* Invoke CLI subprocesses and retain a separate shell collector.
* Add daemon-owned durable restart and diagnostic operations.

## Decision Outcome

Chosen option: shared Go helpers with independent frontend workers.

The CLI retains its explicit legacy-update fallback policy. System update uses
the same handoff with fallback disabled and a separate client that does not
claim input ownership. System restart always confirms that live shell processes
end and saved tabs follow daemon.restore. The restart helper reads status, sends
stop with an expected epoch, waits for shutdown/ownership release using existing
identity checks, and starts the successor. Ordinary protocol 21 carries the
optional stop guard. Stop is a control mutation so lost replies produce an
unknown delivery outcome. No lifecycle mutation is automatically retried.

One three-minute lifecycle worker runs per TUI. It posts generation-fenced
results to the event loop and never redirects navigation. Logs and diagnostics
use independent workers; a closed capture view can be reopened while it runs.
Completion updates its retained view and records its path in Activity without
replacing a foreground overlay. Shutdown cancels workers and rejects late
completions. Restart is frontend-orchestrated: exiting after shutdown can leave
the daemon stopped, recoverable with daemon start.

The installed daemon diagnose command, System D and the build-free skill wrapper
share one Go collector. It uses read-only five-second RPC probes, bounded metadata
and log copies, process inspection and optional native macOS samples. No daemon
is started and no input seat is claimed. Capture has a two-minute deadline.
CLI dispatch resolves only the canonical metadata home for this command, so
malformed settings cannot block evidence gathering or trigger a rewrite.
Each failed probe retains its error/exit result while later probes continue.
Commands have deadlines and bounded stdout/stderr; cancellation kills only the
probe's owned process group. Nonregular evidence files are rejected after a
nonblocking open. Daemon JSON retains the existing frame limit. Process listings
omit arguments. Native raw samples remain within the bundle and are removed
after a bounded report is saved.

Captures use exclusive private directories and mode-0600 files. Installed
captures default to the metadata home's _diagnostics directory; the repository
wrapper keeps its project tmp default. Partial bundles survive cancellation.
Fresh-home initialization ignores this reserved evidence directory without
changing its contents; unknown files and legacy schema/layout checks still
reject adoption. An offline capture therefore cannot prevent first run.
Log views retain up to 256 KiB/2000 lines; text wrapping is cached until content
or width changes.

### Positive Consequences

* Installed users have the same diagnostic evidence path as repository users.
* Update cannot silently end sessions; restart cannot stop a successor observed
  after the confirmation's own status request.
* Failed probes remain reviewable while navigation and log access continue.

### Negative Consequences

* A frontend crash during explicit restart may require daemon start.
* Captures include local paths, logs and terminal contents, requiring review
  before sharing; a cached terminal read cannot prove live rendering is healthy.
* Native sample behavior needs macOS qualification in addition to Linux tests.

## Pros and Cons of the Options

### Shared Go helpers and frontend workers

* Good: one implementation, explicit policies, tested deadlines and bounds.
* Bad: frontend shutdown interrupts orchestration; restart is not durable.

### CLI subprocesses and a separate shell collector

* Good: simple reuse of existing command surfaces.
* Bad: duplicate collectors, shell/platform dependencies and child lifecycle
  management complicate installed TUI behavior.

### Daemon-owned durable operations

* Good: actions could outlive the frontend.
* Bad: an unhealthy daemon cannot reliably collect its own failure; restart
  would require an additional external supervisor and recovery protocol.

## Links

* Extends [cached daemon overview](observe_daemon_runtime_without_terminal_snapshots_180.md).
* Manual verification: System controls and Flow 9 in QA.md.
