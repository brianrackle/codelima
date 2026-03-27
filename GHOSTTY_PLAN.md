# Ghostty Integration Plan

## Summary

CodeLima should keep moving toward the same ownership boundary Ghostling (https://github.com/ghostty-org/ghostling) uses:
Ghostty should own terminal semantics, while CodeLima owns TUI layout, focus management, host integration, and product-specific UX.

The keyboard path already moved in that direction.
The runtime-loaded bridge and packaged Ghostty API surface have now been widened to a Ghostling-era upstream baseline.
Mouse encoding now follows that same model too.
Viewport scrolling and scrollback ownership now follow that same model too.
Render-time default-color semantics now follow that same model too.
The remaining work is mostly about letting Ghostty own more of terminal-side effects and transport behavior.

## Current Baseline

- Ghostty already owns VT parsing, screen state, cursor state, hyperlink metadata, and terminal mode tracking.
- CodeLima now uses Ghostty's key encoder for supported keys, with fallback to the legacy Go encoder for unsupported keys or older Ghostty libraries.
- CodeLima now uses Ghostty's mouse encoder for terminal mouse reporting, with fallback to the legacy Go encoder for unavailable APIs or unsupported mouse events.
- CodeLima now uses Ghostty's viewport scrolling and scrollbar state as the source of truth for embedded-terminal scrollback position.
- CodeLima now uses Ghostty's explicit-versus-default cell color semantics instead of inferring terminal defaults by RGB equality.
- CodeLima now packages a newer upstream `libghostty-vt` commit and adapts it through a local compatibility bridge instead of the older inline reduced API surface.
- The Ghostty integration still uses the runtime-loaded `libghostty-vt` bridge rather than direct linking.

## Remaining Gaps

### 5. Prefer Ghostty terminal effects and callbacks over local response polling

CodeLima still polls terminal responses manually in several places.
Ghostling registers Ghostty effects for terminal-driven actions.

- Shift more terminal response handling onto Ghostty callbacks where the API supports it.
- Use Ghostty-provided hooks for PTY writes, title changes, and other terminal-originated responses where useful.
- Leave only CodeLima-specific event routing in the TUI layer.

Why it matters:

- Simplifies response plumbing.
- Keeps terminal-side behavior closer to Ghostty's model instead of local polling loops.

### 6. Make PTY writes backpressure-aware

CodeLima still uses the simpler synchronous PTY write path.
Ghostling handles nonblocking PTY behavior and partial writes more explicitly.

- Make PTY writes resilient to partial-write and temporary-blocking cases.
- Avoid doing large or repeated writes in a way that can stall the UI path.
- Keep the implementation narrow and focused on the terminal transport.

Why it matters:

- Reduces the risk of UI stalls under heavy terminal output or input bursts.
- Makes the embedded terminal path more robust.

## Recommended Order

1. Shift more terminal effects to Ghostty callbacks.
2. Make PTY writes backpressure-aware.

## Non-Goals

- Do not replace runtime loading with direct linking just to mimic Ghostling.
- Do not copy Ghostling's Raylib-specific windowing or literal scrollbar UI.
- Do not move CodeLima-specific host UX, focus handling, or tree interactions into Ghostty.

## Related Tracking

- `TODO.md` item 1 tracks the narrower host-theme/default-color sync follow-up if Ghostty exposes configurable terminal defaults later.
- `TODO.md` item 10 tracks the next Ghostty follow-up: preferring Ghostty terminal effects and callbacks over local polling.
