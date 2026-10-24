# Attach physical terminals to daemon-owned sessions

## Context and Problem Statement

The manager design makes raw shell attachment the default: `x` suspends the
TUI, Ctrl+] returns to it, and the shell remains owned by the daemon. Existing
terminal RPCs expose rendered cells and semantic input; they cannot carry a
physical terminal's unchanged byte stream. Replaying historical PTY output
would repeat terminal queries and clipboard effects. Running both the native
renderer and the physical terminal as query responders would duplicate replies.

## Decision Drivers

* Keep PTY ownership, process lifetime, bounded journals and renderer isolation.
* Preserve actual key, mouse, paste and terminal query bytes while attached.
* Resume existing screen and input modes at an exact output watermark.
* Bound retention and detect missing output instead of silently corrupting it.
* Restore the outer terminal and release query ownership on every exit path.

## Considered Options

* Launch an independent SSH process outside the daemon.
* Replay the retained raw journal into the physical terminal.
* Export current ANSI state, then stream an acknowledged journal tail.

## Decision Outcome

Use current-state export plus an acknowledged raw tail. Daemon protocol 8 adds
`terminal.attach`, `terminal.attach_read` and `terminal.detach`. A lease belongs
to one physical connection and terminal; a competing attachment is rejected.
It ends on detach, physical disconnect, or a 15-second idle timeout. Attachment
and output reads require the current geometry seat. Every read carries the
previous output cursor; gaps in bounded retention terminate the attachment.
Retired terminal objects remain reachable by their attachment until final
output drains, even after `terminal.list` removes the exited shell.

Renderer protocol 4 adds current-screen ANSI export at a contiguous applied
journal watermark. The native formatter includes cursor, character sets,
scroll regions, tab stops, keyboard modes and current style. A constant-size
VT/UTF-8 boundary tracker rejects captures inside unfinished sequences.
Checkpoint envelope version 2 preserves that tracker; older checkpoints fall
back to the existing journal recovery path.

During capture, native query replies and clipboard effects are held until the
watermark is known. Effects through that cut remain native; live output after
the cut belongs to the physical terminal. Acknowledgements discard held effects
for delivered output. Detach restores native handling for undelivered output.
The held effect budget is 256 KiB; overflow explicitly ends the lease. Initial
export emits no historical clipboard effects or terminal queries.

The frontend suspends Vaxis, opens its own nonblocking `/dev/tty` descriptor,
saves termios, streams bytes, and restores termios and guest-modifiable modes.
Ctrl+] is recognized in both legacy and Kitty encodings, except inside
bracketed paste. Other input remains unchanged. An interactive `codelima shell`
uses the same managed session; commands supplied after the node continue to
use the existing command execution path. `terminal attach ID` resumes a
specific session. Embedded mode remains an explicit shell choice.

Daemon-created guest shells use the internal `terminal run-node-shell` entry
point, which invokes the direct transport. Calling the public interactive
`shell` command there would recursively attach the session to itself. Created
session pushes and local open replies share identity-based tab installation;
either order updates the manager exactly once without stealing focus.

Physical writes use bounded, cancellable nonblocking polls. Backpressure must
not prevent Ctrl+] or context cancellation from reaching terminal restoration;
cleanup has its own short deadline and always restores termios.

### Positive Consequences

* Closing the frontend or detaching preserves the process and daemon identity.
* Physical terminals handle live rendering, input protocols and query replies.
* The same stream works for TUI suspension and CLI attachment.
* No historical side effects are replayed to the host.

### Negative Consequences

* Raw attachment adds a small bounded polling stream (20 ms while attached).
* A slow client that exceeds retention must reattach from a new state capture.
* ANSI export is a presentation reconstruction, not the native checkpoint;
  retained image state and unusual terminal modes need separate qualification.
* Only one physical terminal owns a session's raw query handling at a time.

## Pros and Cons of the Options

Independent SSH is simpler but loses daemon session identity and detach
semantics. Raw replay preserves bytes but repeats side effects and can begin
inside a truncated escape. State export and a contiguous tail require an
explicit ownership contract but preserve existing process and isolation rules.

## Links

* [Fleet manager navigation](use_a_fleet_projection_for_the_manager_tui_150.md)
* [Design delivery ledger](../plans/tui-vnext/README.md)
* [Remaining implementation and qualification](../TODO.md)
