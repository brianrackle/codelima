# Bounded local node logs and bootstrap capture

## Context and Problem Statement

The node Logs design needs Events, Bootstrap, Lima and Serial sources with
follow, search, severity, wrap, copy and external paging. The previous detail
view read only structured events, and bootstrap command output was transient.
Diagnostics must remain readable when a guest is stopped or unavailable.

## Considered Options

* Read bounded local files and capture provisioning output alongside events.
* Stream every diagnostic through new daemon subscriptions.
* Keep structured events only and send users to external files.

## Decision Outcome

Read the existing metadata event file and Lima instance `ha.stderr.log` and
`serial.log` directly, without runtime preparation. Capture bootstrap and
validation command stdout/stderr to `nodes/<id>/bootstrap.log`, alongside
timestamped command, completion/failure and elapsed-time records. Existing
progress output is preserved. Credential-import commands remain outside this
capture path. Files are private (0600), capped at 4 MiB per generation with one
previous generation. Large writes are chunked before rotation, and capture
fails explicitly if rotation cannot maintain the cap.

The frontend reads at most the current file's final 2 MiB and retains at most
5,000 parsed lines. It labels clipped history as a recent tail. Reads open
nonblocking and reject nonregular files; partial structured records are skipped.
Terminal control sequences are removed before drawing diagnostic text.
Original files remain available through `$PAGER` (default `less`), using a
quoted argument list and a literal path rather than a shell expression. The
TUI suspends for the pager and restores its terminal afterwards.

Reads run outside the event loop and use cancellation, a request serial and
navigation generation. Only the visible, following log view polls, once per
second; source changes load once immediately. Scrolling and search pause
follow. Search input owns numbers, paste and other key-shaped text before
global navigation. Wrapping uses grapheme boundaries and host cell widths;
continuation rows retain the original record identity for copy and selection.
Search is literal, case-insensitive, and navigates matching lines.

### Positive Consequences

* Failed or stopped guests retain useful local diagnostic output.
* Memory, disk retention and polling work have explicit bounds.
* Log controls do not create guests, terminals or extra daemon subscriptions.

### Negative Consequences

* Reading an older rotated generation requires opening it externally.
* Capture failures can fail a provisioning step; command failures remain
  available through the returned error and structured lifecycle event.
* Stored commands and output can contain user-provided secrets, so private
  permissions matter and log exports require the same care as existing logs.

## Pros and Cons of the Options

Daemon streaming would support remote frontends but add subscription and
replay state for files already local to the frontend. Events alone are simpler
but cannot explain a failed install's output. Bounded local reads satisfy the
current local-manager contract without another protocol revision.

## Links

* [Design reference](../plans/tui-vnext/reference/NodeLogs.dc.html)
* Reuses the rotating sink introduced in ADR 59.
