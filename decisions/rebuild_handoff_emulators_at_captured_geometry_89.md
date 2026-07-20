# Rebuild handoff emulators at captured geometry

Status: Accepted

## Context and Problem Statement

Live daemon update transfers a PTY plus an output replay tail, then constructs a replacement Ghostty emulator. The constructor initializes Ghostty at 80 columns by 24 rows; overwriting only the Go wrapper's dimensions before replay left the emulator grid at 80x24, so non-default-width snapshots used the wrong row stride and rendered offset or combined lines.

## Decision Drivers

* Wrapper, emulator, and PTY geometry must describe the same cell grid.
* Replay wrapping must match the geometry captured while the old daemon was quiesced.
* An unchanged client resize is correctly deduplicated and cannot be relied upon to repair imported state later.

## Considered Options

* Keep overwriting only the wrapper dimensions and wait for a later resize.
* Force every reattached TUI to send two different resize requests.
* Resize the replacement emulator to the captured geometry before attaching the PTY and replaying output.

## Decision Outcome

Chosen option: "resize the replacement emulator before attaching the PTY and replaying output", because it establishes the geometry invariant at the handoff construction boundary and avoids sending an artificial resize or redraw request to the live child.

Imported geometry must be positive. The replacement actor applies its ordinary resize command while no PTY is attached, updating both the Go wrapper and Ghostty grid without emitting `SIGWINCH` or the primary-screen redraw shim. Only then does import attach the transferred PTY and ingest the replay tail.

### Positive Consequences

* Wrapped content keeps correct row boundaries after daemon update.
* Snapshot dimensions and Ghostty cell strides agree immediately.
* Reattachment remains compatible with idempotent resize handling.

### Negative Consequences

* Handoff import performs one additional actor command before replay.
* Invalid imported dimensions now fail the handoff instead of constructing unusable state.

## Pros and Cons of the Options

### Wait for a later resize

* Good, because import remains minimal.
* Bad, because equal-size requests are intentionally no-ops and corruption can persist indefinitely.

### Force client resize churn

* Good, because it eventually resizes Ghostty.
* Bad, because it leaks an internal repair requirement into every client and sends avoidable `SIGWINCH` events.

### Resize before replay

* Good, because replay uses the captured wrapping width.
* Good, because the PTY and child are not disturbed during emulator initialization.
* Bad, because import validates and applies another state transition.

## Links

* Refines [ADR 67](authenticated_scm_rights_daemon_handoff_67.md).
* Preserves the idempotent resize contract from [ADR 68](edge_trigger_daemon_terminal_geometry_68.md).
