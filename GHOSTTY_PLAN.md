# Ghostty Integration Plan

## Summary

CodeLima should keep moving toward the same ownership boundary Ghostling (https://github.com/ghostty-org/ghostling) uses:
Ghostty should own terminal semantics, while CodeLima owns TUI layout, focus management, host integration, and product-specific UX.

The keyboard path already moved in that direction.
The runtime-loaded bridge and packaged Ghostty API surface have now been widened to a Ghostling-era upstream baseline.
Mouse encoding now follows that same model too.
Viewport scrolling and scrollback ownership now follow that same model too.
Render-time default-color semantics now follow that same model too.
Terminal-driven query responses now follow that same model too.
Terminal transport now follows that same model too.
The current Ghostling-inspired gap list is complete.

## Current Baseline

- Ghostty already owns VT parsing, screen state, cursor state, hyperlink metadata, and terminal mode tracking.
- CodeLima now uses Ghostty's key encoder for supported keys, with fallback to the legacy Go encoder for unsupported keys or older Ghostty libraries.
- CodeLima now uses Ghostty's mouse encoder for terminal mouse reporting, with fallback to the legacy Go encoder for unavailable APIs or unsupported mouse events.
- CodeLima now uses Ghostty's viewport scrolling and scrollbar state as the source of truth for embedded-terminal scrollback position.
- CodeLima now uses Ghostty's explicit-versus-default cell color semantics instead of inferring terminal defaults by RGB equality.
- CodeLima now packages a newer upstream `libghostty-vt` commit and adapts it through a local compatibility bridge instead of the older inline reduced API surface.
- CodeLima now answers color-scheme, XTWINOPS size, device-attributes, and XTVERSION terminal queries through Ghostty callback registration instead of local handcrafted response strings.
- CodeLima now routes embedded-terminal input and Ghostty-generated PTY responses through a dedicated nonblocking PTY write loop that handles partial writes and temporary backpressure explicitly.
- The Ghostty integration still uses the runtime-loaded `libghostty-vt` bridge rather than direct linking.

## Remaining Gaps

None in the current Ghostling-inspired implementation list.

## Recommended Order

The tracked Ghostling-inspired gap list is complete.

## Non-Goals

- Do not replace runtime loading with direct linking just to mimic Ghostling.
- Do not copy Ghostling's Raylib-specific windowing or literal scrollbar UI.
- Do not move CodeLima-specific host UX, focus handling, or tree interactions into Ghostty.

## Related Tracking

- `TODO.md` item 1 tracks the narrower host-theme/default-color sync follow-up if Ghostty exposes configurable terminal defaults later.
- `TODO.md` item 9 tracks the remaining full human `TUI Verification` run on a real terminal session.
