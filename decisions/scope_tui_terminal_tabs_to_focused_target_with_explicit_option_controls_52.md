# Scope TUI Terminal Tabs To The Focused Target With Explicit Option Controls

## Context and Problem Statement

The TUI previously treated every opened project or node session as one global terminal tab strip, created sessions implicitly while visiting tree entries in terminal pane mode, and advertised function-key plus tree-focus `t` fallbacks for tab control. Manual use showed this is the wrong model: visiting an item must not create tabs, tabs for unrelated items must not be visible, and tab management should be Option-keybind driven. Users also want more than one embedded shell per node, one terminal tab available immediately on TUI startup, and close behavior that moves focus to the adjacent numbered tab instead of wrapping across the strip.

## Decision Drivers

* TUI startup should make one terminal tab available for the initial project or running node without taking tree focus or leaving info mode.
* Selecting or visiting a tree entry after startup must never create or activate a terminal session.
* A fresh terminal after startup must open only on the explicit open-tab command for the focused item.
* Visible tabs must be scoped to the item currently focused in the tree (or fullscreen-focused), not shown globally.
* Tab management must use Option keybindings; the `F7`/`F8`/`Shift+F8`/`F9` and tree-focus `t` fallbacks must be removed.
* Multiple independent embedded shells per project or node should be possible ("tabs per node").
* Closing a tab should focus the adjacent higher-numbered tab when one exists, otherwise the adjacent lower-numbered tab.
* `Option+`\` / `F6` stays the separate fullscreen-focus toggle, and `Option+Shift+`\` stays the host-terminal switch.

## Considered Options

* Keep the global one-session-per-target tab strip and only fix keybindings.
* Scope the existing single sessions per target to the focused item without multi-tab support.
* Give each target its own ordered tab list with explicit Option-driven open/switch/close and per-target active-tab memory.

## Decision Outcome

Chosen option: "Give each target its own ordered tab list with a startup default tab, explicit Option-driven open/switch/close, and per-target active-tab memory", because it is the only option that matches the stated product intent: one immediately available shell, explicit manual tab management per node after startup, no session creation while browsing, scoped tab visibility, and adjacent close focus.

### Positive Consequences

* Session identity is split from target identity: tabs are keyed `<target>#<n>` (`node:<id>#1`, `project:<id>#2`, …) and each `tuiSession` records its owning target.
* TUI startup opens one default tab for the initial project or running node after recording the embedded terminal size, then leaves tree focus and the info pane intact.
* `Option+t` always opens a fresh tab for the focused project or running node, keeps tree focus, and switches the right pane to terminal mode; repeated presses create additional tabs for the same item.
* `Option+Left`/`Option+Right` cycle and `Option+w` closes within the focused item's tabs only; close focus moves to the next higher-numbered adjacent tab when available, otherwise the previous lower-numbered tab.
* Each item remembers its active tab across focus changes.
* The pane-border tab strip renders only the focused item's tabs, numbered when more than one is open, with `host:` labels for project shells and the active tab bracketed.
* Tree selection, the `i` pane-mode toggle, and data refreshes no longer create sessions after startup; `Option+`\` / `F6` fullscreen focus and the host toggle reuse the item's active tab and open its first tab only as part of that explicit command.
* macOS Option key variants stay accepted (Alt/Meta modifiers, `†`, `∑`, `Esc f`/`Esc b`), so the bindings work whether or not the emulator remaps Option, while `F7`–`F9` and tree `t` are gone.

### Negative Consequences

* Terminals whose Option key neither sets Alt/Meta nor produces the recognized glyph sequences cannot drive tab management until the emulator is configured (for example Ghostty's `macos-option-as-alt = true`).
* Stopping a node or pruning an orphaned target now closes a list of sessions instead of one, and the session store carries per-target tab counters.
* Sessions are no longer reachable by target key alone; all lookups go through the target's tab list or the state's active-tab resolution.

## Pros and Cons of the Options

### Keep the global one-session-per-target tab strip and only fix keybindings

* Good, because it is the smallest change.
* Bad, because visiting entries in terminal pane mode would still create sessions.
* Bad, because tabs for unrelated nodes would still appear while another node is focused.

### Scope the existing single sessions per target to the focused item without multi-tab support

* Good, because it fixes implicit creation and scoping with moderate change.
* Bad, because switch/close tab commands are meaningless with at most one tab per item.
* Bad, because it does not deliver "open and manage terminal tabs per node".

### Give each target its own ordered tab list with explicit Option-driven open/switch/close and per-target active-tab memory

* Good, because tab semantics finally match a terminal emulator's per-context tabs.
* Good, because users start with one available tab while further tab creation remains explicit.
* Bad, because it touches session identity everywhere (store, draw, mouse, resize, close events).

## Links

* Supersedes [Treat TUI Sessions as Terminal Tabs](treat_tui_sessions_as_terminal_tabs_45.md)
* Refines [Keep TUI Terminal Tabs Single-Pane](keep_tui_terminal_tabs_single_pane_49.md)
* Supersedes [Render Visible TUI Terminal Tabs And Accept Option Shortcuts](render_visible_tui_terminal_tabs_and_accept_meta_modifier_51.md)
