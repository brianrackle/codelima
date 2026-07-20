# Move terminal runtime ownership into a per-home daemon

Status: Accepted

## Context and Problem Statement

TUI-owned PTYs terminate when the TUI exits and cannot be reached by another CLI or TUI. CodeLima needs terminal sessions to outlive front ends while preserving the existing Service and actor seams.

## Decision Outcome

`codelima daemon run` is the single owner of PTYs and Ghostty emulator actors for one `CODELIMA_HOME`. Front ends connect over private Unix sockets; the TUI renders daemon snapshots client-side and detaches its views on exit, while an explicit tab close asks the daemon to terminate the actor. A nonblocking flock, stale-socket cleanup, PID and identity files prevent two owners.

### Consequences

* TUI exit and client crashes no longer terminate agents.
* `terminal list/read/send` makes live terminals automatable from another process.
* Ghostty is required by the daemon for managed terminal sessions; the old in-process fallback remains only for test/local compatibility.
* Normal daemon restart respawns persisted shells; live PTY survival is handled separately by ADR 67.

## Links

* Refined by [ADR 65](exact_version_json_lines_daemon_protocol_65.md)
* Refined by [ADR 66](version_daemon_session_persistence_66.md)
* Extended by [ADR 67](authenticated_scm_rights_daemon_handoff_67.md)
* Refined by [ADR 68](edge_trigger_daemon_terminal_geometry_68.md)
* Refined by [ADR 81](preserve_bracketed_paste_across_daemon_terminals_81.md)
