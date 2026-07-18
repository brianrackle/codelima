# Open Node Host Shells as Ordinary Terminal Tabs

## Context and Problem Statement

The node host shell used a special `Option+Shift+Backtick` override that remembered one guest return session and toggled fullscreen terminal focus. That behavior diverged from the TUI's established per-node tab model: opening, switching, closing, refreshing, and displaying a host shell followed separate state paths instead of the same paths as guest tabs.

## Decision Drivers

* Host-shell creation should align with `Option+t` guest-tab creation.
* Repeated open commands should create independent tabs.
* Host and guest tabs should share switching, closing, refresh, mouse, and focus behavior.
* The active host-machine indicator must remain visible and follow the active tab.
* The keybinding should be memorable as a shifted form of the guest-tab binding.

## Considered Options

* Keep the `Option+Shift+Backtick` host/guest toggle.
* Change the key to `Option+Shift+t` but retain override/return-session state.
* Make `Option+Shift+t` open a fresh ordinary host tab on the selected node target.

## Decision Outcome

Chosen option: "Make `Option+Shift+t` open a fresh ordinary host tab on the selected node target", because it gives both terminal kinds one coherent tab lifecycle and removes the special host-return state.

### Positive Consequences

* `Option+t` opens a guest tab and `Option+Shift+t` opens a host tab.
* Repeated host-tab commands create fresh tabs without implicitly creating a guest tab.
* `Option+Left`, `Option+Right`, and `Option+w` operate uniformly across both kinds.
* Tree focus and terminal-pane visibility behave consistently for guest and host tab creation.
* Refresh preserves the active host tab through the existing per-target active-tab map.
* The top bar becomes red exactly when the visible active node tab is a host shell.
* Host-tab creation reads the node directory from durable metadata instead of requiring a successful Microsandbox status observation.
* The old `Option+Shift+Backtick` command is removed.

### Negative Consequences

* Returning to a guest shell now uses ordinary tab switching rather than a one-key toggle.
* Existing users must learn the new shortcut.
* macOS terminals must deliver Alt/Meta modifiers or the recognized `ˇ` glyph for `Option+Shift+t`.

## Pros and Cons of the Options

### Keep the host/guest toggle

* Good, because returning to one remembered guest session takes one command.
* Bad, because host shells remain outside the ordinary tab lifecycle.
* Bad, because repeated host commands cannot naturally create multiple host tabs.

### Change the key but retain override state

* Good, because the shortcut is more consistent with tab creation.
* Bad, because its behavior would still be a toggle rather than tab creation.

### Open a fresh ordinary host tab

* Good, because one tab model covers both terminal kinds.
* Good, because existing switch, close, refresh, and rendering behavior is reused.
* Bad, because switching back requires the normal tab navigation command.

## Links

* Supersedes [Add host terminal toggle for node sessions](add_host_terminal_toggle_for_node_sessions_43.md)
* Refines [Scope TUI terminal tabs to the focused target with explicit Option controls](scope_tui_terminal_tabs_to_focused_target_with_explicit_option_controls_52.md)
* Refines [Use TUI header for host terminal override indicator](use_tui_header_for_host_terminal_override_indicator_50.md)
