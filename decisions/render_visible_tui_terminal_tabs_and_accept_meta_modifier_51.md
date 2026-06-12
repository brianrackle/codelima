# Render Visible TUI Terminal Tabs And Accept Option Shortcuts

## Context and Problem Statement

The TUI treated opened project and node sessions as terminal tabs, but the pane border only showed the info-versus-terminal mode tabs. Users could press the tab shortcuts without seeing which project or node sessions were open, and macOS terminals report Option shortcuts inconsistently: some report `ModAlt`, some report `ModMeta`, some emit Option glyphs such as `†` and `∑`, and some encode Option-arrow as `Alt+b` and `Alt+f`.

## Decision Drivers

* Make opened terminal sessions visible where users expect tab state.
* Keep the tab strip within existing chrome so terminal geometry does not change.
* Provide a tab-control path that does not depend on terminal-emulator Option handling.
* Keep macOS Option shortcut variants as secondary bindings when the terminal passes them through.
* Preserve the existing one-session-per-project-or-node model.

## Considered Options

* Keep terminal sessions invisible and document shortcuts only.
* Add a separate terminal tab bar row.
* Render session tabs in the pane border and accept function-key tab controls plus common Option shortcut encodings.

## Decision Outcome

Chosen option: "Render session tabs in the pane border and accept function-key tab controls plus common Option shortcut encodings", because it makes tab state visible without taking more terminal space, gives users a path when the host terminal reserves Option combinations, and still handles common macOS Option event differences.

### Positive Consequences

* Open project and node sessions are visible in the terminal pane border.
* The active terminal tab is bracketed.
* Tree-focus `t`, plus `F7`, `F8`, `Shift+F8`, and `F9`, provide terminal-tab controls that do not depend on Option handling or force fullscreen terminal focus.
* `Option+t`, `Option+Left`, `Option+Right`, and `Option+w` still work when the terminal reports Option as Alt, Meta, macOS Option glyphs, or common Option-arrow word-navigation sequences.

### Negative Consequences

* Very long session lists can crowd the pane border.
* Option-generated glyphs that are claimed as tab shortcuts are no longer available as literal terminal input through those specific key combinations while the TUI is focused.

## Pros and Cons of the Options

### Keep terminal sessions invisible and document shortcuts only

Continue relying on active terminal state without rendering open sessions.

* Good, because it adds no rendering complexity.
* Bad, because users cannot see whether a tab opened.
* Bad, because failed Option-as-Meta setup looks like a missing feature.

### Add a separate terminal tab bar row

Render project and node session tabs on their own row.

* Good, because there is more room for labels.
* Bad, because it consumes terminal space and changes geometry.
* Bad, because it adds another TUI chrome row.

### Render session tabs in the pane border and accept function-key tab controls plus common Option shortcut encodings

Use the existing border line for session tabs and broaden shortcut matching to include tree-focus `t`, F7/F8/Shift+F8/F9, Alt/Meta, Option+t/Option+w glyphs, and common Option-arrow encodings.

* Good, because tab state becomes visible without resizing the terminal body.
* Good, because users have tab controls even when the host terminal reserves Option keys.
* Good, because tree-focus `t` works even when function keys are mapped to media controls.
* Good, because it covers common macOS Option forms.
* Bad, because the border has limited horizontal space.

## Links

* Refines [Treat TUI Sessions as Terminal Tabs](treat_tui_sessions_as_terminal_tabs_45.md)
* Refines [Keep TUI Terminal Tabs Single-Pane](keep_tui_terminal_tabs_single_pane_49.md)
* Superseded by [Scope TUI Terminal Tabs To The Focused Target With Explicit Option Controls](scope_tui_terminal_tabs_to_focused_target_with_explicit_option_controls_52.md)
