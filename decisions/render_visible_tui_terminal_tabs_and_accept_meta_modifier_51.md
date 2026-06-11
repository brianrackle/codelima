# Render Visible TUI Terminal Tabs And Accept Meta Modifier

## Context and Problem Statement

The TUI treated opened project and node sessions as terminal tabs, but the pane border only showed the info-versus-terminal mode tabs. Users could press the tab shortcuts without seeing which project or node sessions were open, and some macOS terminals report Option-as-Meta as `ModMeta` instead of `ModAlt`.

## Decision Drivers

* Make opened terminal sessions visible where users expect tab state.
* Keep the tab strip within existing chrome so terminal geometry does not change.
* Support macOS terminal modifier variants without treating plain Option-generated text as a shortcut.
* Preserve the existing one-session-per-project-or-node model.

## Considered Options

* Keep terminal sessions invisible and document shortcuts only.
* Add a separate terminal tab bar row.
* Render session tabs in the pane border and accept both Alt and Meta modifiers.

## Decision Outcome

Chosen option: "Render session tabs in the pane border and accept both Alt and Meta modifiers", because it makes tab state visible without taking more terminal space and handles common macOS Option-as-Meta reporting differences.

### Positive Consequences

* Open project and node sessions are visible in the terminal pane border.
* The active terminal tab is bracketed.
* `Option+t`, `Option+Left`, `Option+Right`, and `Option+w` work when the terminal reports Option as either Alt or Meta.

### Negative Consequences

* Very long session lists can crowd the pane border.
* Terminals that emit plain Option text, such as `†` for Option+t, still need Option-as-Meta configuration because there is no modifier to match safely.

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

### Render session tabs in the pane border and accept both Alt and Meta modifiers

Use the existing border line for session tabs and broaden shortcut matching.

* Good, because tab state becomes visible without resizing the terminal body.
* Good, because it covers common macOS Option-as-Meta forms.
* Bad, because the border has limited horizontal space.

## Links

* Refines [Treat TUI Sessions as Terminal Tabs](treat_tui_sessions_as_terminal_tabs_45.md)
* Refines [Keep TUI Terminal Tabs Single-Pane](keep_tui_terminal_tabs_single_pane_49.md)
