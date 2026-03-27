# Ghostty Integration Plan

## Summary

CodeLima should keep moving toward the same ownership boundary Ghostling (https://github.com/ghostty-org/ghostling) uses:
Ghostty should own terminal semantics, while CodeLima owns TUI layout, focus management, host integration, and product-specific UX.

The keyboard path already moved in that direction.
The runtime-loaded bridge and packaged Ghostty API surface have now been widened to a Ghostling-era upstream baseline.
The remaining work is mostly about letting Ghostty own more of mouse handling, viewport state, render semantics, and terminal-side effects.

## Current Baseline

- Ghostty already owns VT parsing, screen state, cursor state, hyperlink metadata, and terminal mode tracking.
- CodeLima now uses Ghostty's key encoder for supported keys, with fallback to the legacy Go encoder for unsupported keys or older Ghostty libraries.
- CodeLima now packages a newer upstream `libghostty-vt` commit and adapts it through a local compatibility bridge instead of the older inline reduced API surface.
- The Ghostty integration still uses the runtime-loaded `libghostty-vt` bridge rather than direct linking.

## Remaining Gaps

### 2. Move mouse encoding into Ghostty

CodeLima still hand-encodes mouse events.
Ghostling uses Ghostty's mouse encoder and syncs it from terminal state.

- Replace the local mouse-escape generation with Ghostty mouse encoding.
- Let Ghostty decide the active mouse protocol from terminal mode.
- Preserve CodeLima-specific host behaviors such as copy gestures and focus changes above that layer.

Why it matters:

- Reduces local escape-sequence logic.
- Improves correctness for mouse mode changes and protocol differences.

### 3. Move viewport scrolling and scrollback ownership into Ghostty

CodeLima still keeps a parallel local `scrollOffset` model.
Ghostling treats viewport state as terminal-owned data.

- Replace local scrollback math with Ghostty viewport scrolling APIs.
- Use Ghostty as the source of truth for scroll position and visible history.
- Keep only the TUI presentation and input routing logic locally.

Why it matters:

- Removes duplicated scroll state.
- Reduces the chance of local scrollback drifting from Ghostty's real terminal state.

### 4. Improve render semantics for default background and transparency

CodeLima still has a known ambiguity around Ghostty default background versus explicit cell background.

- Feed the host terminal background into the Ghostty terminal configuration.
- Prefer Ghostty render-state semantics that can distinguish "default background" from "explicit background equal to default."
- Keep transparent-default behavior only when the terminal state actually means default background.

Why it matters:

- Improves visual correctness on non-black themes.
- Avoids treating explicit app backgrounds as transparent by accident.

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

1. Move mouse encoding into Ghostty.
2. Move viewport scrolling into Ghostty.
3. Improve render/background semantics.
4. Shift more terminal effects to Ghostty callbacks.
5. Make PTY writes backpressure-aware.

## Non-Goals

- Do not replace runtime loading with direct linking just to mimic Ghostling.
- Do not copy Ghostling's Raylib-specific windowing or literal scrollbar UI.
- Do not move CodeLima-specific host UX, focus handling, or tree interactions into Ghostty.

## Related Tracking

- `TODO.md` item 1 tracks the host-terminal background follow-up.
- `TODO.md` item 10 tracks the next Ghostty follow-up: moving mouse encoding into Ghostty.
