# Start Embedded Terminal Sessions At Pane Size

## Context and Problem Statement

The TUI starts embedded shell sessions before the first draw so running nodes already have a live terminal when the tree appears. Both the Ghostty and Vaxis backends were starting those PTY-backed shells at a generic `80x24`, then resizing on first draw, which could drop the shell's first prompt or other early output and leave the pane looking blank.

## Decision Drivers

* Preserve initial shell output in the embedded pane
* Keep one session-start path shared by both Ghostty and Vaxis backends
* Avoid larger changes to the TUI selection and session lifecycle

## Considered Options

* Keep starting embedded terminals at a fixed default size
* Defer session creation until after the first draw
* Track the current pane size and apply it before starting the embedded PTY

## Decision Outcome

Chosen option: "Track the current pane size and apply it before starting the embedded PTY", because it fixes the blank-terminal symptom without changing the existing session reuse model or delaying shell startup until after an extra render pass.

### Positive Consequences

* Initial prompts and other early shell output are rendered at the correct size in both terminal backends
* Running-node auto-open behavior stays intact
* Future sessions created after resizes or layout changes inherit the current pane geometry

### Negative Consequences

* The TUI session store now owns one more piece of UI state: the preferred terminal pane size
* Terminal backends must support a pre-start resize hook in addition to normal draw-time resizing

## Pros and Cons of the Options

### Keep starting embedded terminals at a fixed default size

Continue launching PTY-backed shells at `80x24` and rely on draw-time resize.

* Good, because it keeps the current startup path simple
* Bad, because early shell output can be lost before the first draw
* Bad, because both terminal backends repeat the same visual failure

### Defer session creation until after the first draw

Wait until the UI knows the exact pane dimensions, then create the shell session.

* Good, because the PTY would always start at the final pane size
* Good, because it avoids adding a resize hook to terminal backends
* Bad, because it changes the current selection-driven auto-open lifecycle
* Bad, because it adds more coordination between draw timing and session startup

### Track the current pane size and apply it before starting the embedded PTY

Keep the existing auto-open timing, but let the session store remember the current pane size and resize backends before `Start`.

* Good, because it fixes the prompt-loss bug without changing session ownership
* Good, because the same approach works for Ghostty and the Vaxis fallback
* Bad, because the session store now carries layout-derived state

## Links

* Pattern [PATTERNS.MD](/Users/brianrackle/Projects/codelima/PATTERNS.MD)
