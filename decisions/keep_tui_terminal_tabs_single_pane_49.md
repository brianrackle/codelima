# Keep TUI Terminal Tabs Single-Pane

## Context and Problem Statement

CodeLima briefly supported Ghostty-style split shortcuts inside the TUI, but that made terminal tabbing feel like pane management and intercepted modified keys that users expect to reach the shell. The TUI already treats opened project and node sessions as terminal tabs, so the question is whether it should also own a split-pane model.

## Decision Drivers

* Keep tab commands focused on opening, switching, and closing terminal sessions.
* Avoid resizing one PTY-backed terminal backend into synthetic split panes.
* Preserve ordinary modified terminal input when the embedded terminal is focused.
* Avoid letting modified keys fall through to plain tree action hotkeys.

## Considered Options

* Keep TUI-owned terminal splits.
* Create independent multi-session split panes.
* Keep terminal tabs single-pane and forward non-command modified terminal keys.

## Decision Outcome

Chosen option: "Keep terminal tabs single-pane and forward non-command modified terminal keys", because it keeps the TUI session model simple and makes tabbing behave like tabbing instead of splits.

### Positive Consequences

* Tree-focus `t`, `F7`, `F8`, `Shift+F8`, and `F9` remain the advertised TUI terminal tab management shortcuts.
* Modified keys such as `Alt+d` reach the focused terminal instead of creating TUI panes.
* Tree action hotkeys no longer match modified versions of the same key.

### Negative Consequences

* CodeLima no longer provides Ghostty-style split panes inside the TUI.
* Users who want independent side-by-side shells still need another terminal, tmux, or a future explicit multi-session design.

## Pros and Cons of the Options

### Keep TUI-owned terminal splits

Retain `Command+d`, `Command+Shift+d`, `Alt+d`, and `Alt+Shift+d` as TUI split shortcuts.

* Good, because it provides a familiar Ghostty-style gesture for some users.
* Bad, because it creates pane semantics without independent terminal sessions.
* Bad, because it intercepts modified keys that may be meaningful to shells and terminal applications.

### Create independent multi-session split panes

Allow multiple simultaneous terminal sessions for the same project or node.

* Good, because every split could be an independent shell.
* Bad, because it requires new session identity, close, restore, and focus semantics.
* Bad, because it is larger than the current tab-focused workflow needs.

### Keep terminal tabs single-pane and forward non-command modified terminal keys

Keep one visible terminal surface and let tab commands manage opened project and node sessions.

* Good, because it matches the existing one-session-per-target model.
* Good, because modified terminal input stays available to the guest.
* Bad, because pane layout is delegated to tools outside CodeLima.

## Links

* Supersedes [Add Ghostty-Style TUI Terminal Split Keys](add_ghostty_style_tui_terminal_split_keys_48.md)
* Refines [Treat TUI Sessions as Terminal Tabs](treat_tui_sessions_as_terminal_tabs_45.md)
* Refined by [Scope TUI Terminal Tabs To The Focused Target With Explicit Option Controls](scope_tui_terminal_tabs_to_focused_target_with_explicit_option_controls_52.md)
