# Treat TUI sessions as terminal tabs

## Context and Problem Statement

CodeLima already keeps one live embedded terminal session for each opened project or node target, but users had no keyboard-level way to manage those sessions as a set of terminal tabs. The TUI needs commands to open the focused target's terminal, switch among open terminals, and close the active terminal without losing the tree model.

## Decision Drivers

* Reuse the existing one-session-per-project-or-node terminal model.
* Keep terminal tab commands available while terminal focus is active.
* Preserve selection, session state, and host-terminal override behavior.

## Considered Options

* Add multi-session tabs per node.
* Open external host terminal tabs.
* Treat opened project and node sessions as the TUI terminal tabs.

## Decision Outcome

Chosen option: "Treat opened project and node sessions as the TUI terminal tabs", because it gives users tab management over the sessions CodeLima already owns without introducing another terminal lifecycle model.

### Positive Consequences

* `Alt+t` opens or focuses the selected project or node terminal.
* `Alt+Left` and `Alt+Right` switch among open terminal sessions in stable open order.
* `Alt+w` closes the active terminal session and activates the next open one when available.

### Negative Consequences

* A target still has at most one CodeLima-owned embedded session.
* These are in-TUI tabs, not native host-terminal tabs.
* The footer carries more shortcut text in terminal focus.

## Pros and Cons of the Options

### Add multi-session tabs per node

Allow several independent terminal sessions for the same project or node.

* Good, because it matches full terminal-emulator tab semantics.
* Bad, because it requires new target identities, close handling, and session restore rules.

### Open external host terminal tabs

Ask the host terminal emulator to open native tabs for the current target.

* Good, because it uses native terminal UX.
* Bad, because terminal-emulator automation is not portable and conflicts with the embedded TUI model.

### Treat opened project and node sessions as the TUI terminal tabs

Keep one session per target and add keyboard management over the open-session set.

* Good, because it builds directly on the existing session store.
* Good, because it avoids duplicating node shell lifecycle behavior.
* Bad, because users cannot open two independent embedded shells for the same node yet.

## Links

* Extends [TUI Session Reuse](../PATTERNS.MD)
* Refined by [Render Visible TUI Terminal Tabs And Accept Meta Modifier](render_visible_tui_terminal_tabs_and_accept_meta_modifier_51.md)
