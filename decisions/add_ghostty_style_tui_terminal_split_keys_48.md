# Add Ghostty-Style TUI Terminal Split Keys

Status: Superseded by [Keep TUI Terminal Tabs Single-Pane](keep_tui_terminal_tabs_single_pane_49.md).

## Context and Problem Statement

Users familiar with Ghostty expect `Command+d` and `Command+Shift+d` to create right and downward terminal panes. CodeLima's embedded terminal already runs inside the TUI and has no portable host-terminal automation layer, so the split behavior needs to fit the existing Vaxis/Ghostty embedded-terminal ownership model.

## Decision Drivers

* Provide Ghostty-style split shortcuts for the focused CodeLima terminal.
* Keep terminal input and resize ownership deterministic.
* Avoid depending on host terminal emulator automation that is not available in the codebase.
* Preserve the existing one-session-per-project-or-node session model.

## Considered Options

* Automate native Ghostty host panes.
* Create independent duplicate PTY sessions for every split.
* Split the TUI terminal surface and place the active CodeLima terminal in the new pane.

## Decision Outcome

Chosen option: "Split the TUI terminal surface and place the active CodeLima terminal in the new pane", because it delivers the requested pane workflow inside CodeLima's portable TUI while keeping the existing terminal-session contract intact.

### Positive Consequences

* `Command+d`/`Command+Shift+d` work when the host terminal forwards Super/Command key events.
* `Alt+d`/`Alt+Shift+d` provide portable fallbacks when Command keys are intercepted by the host terminal.
* The active pane owns PTY input and resize geometry, which avoids resizing one terminal backend to two competing dimensions.

### Negative Consequences

* Split panes are in-TUI panes, not native Ghostty host panes.
* The inactive pane is contextual chrome rather than an independent second shell.
* Users who want multiple independent shells for the same node still need a later multi-session design.

## Pros and Cons of the Options

### Automate native Ghostty host panes

Ask the host Ghostty application to create native panes running the current CodeLima terminal target.

* Good, because it would match Ghostty's native pane model exactly.
* Bad, because there is no existing portable host-terminal automation boundary in CodeLima.
* Bad, because it would be Ghostty-specific while the TUI also runs in other terminals.

### Create independent duplicate PTY sessions for every split

Allow a single project or node target to have multiple simultaneous embedded sessions.

* Good, because every split pane could be an independent shell.
* Bad, because it changes the established one-session-per-target model and needs new identity, close, and restore semantics.

### Split the TUI terminal surface and place the active CodeLima terminal in the new pane

Use the TUI layout to create a right or lower pane and move the active terminal drawing/input surface into that pane.

* Good, because it is portable and fits the current embedded-terminal architecture.
* Good, because active-terminal resizing remains deterministic.
* Bad, because it is not a full multi-shell split implementation.

## Links

* Refines [TUI Session Reuse](../PATTERNS.MD)
* Superseded by [Keep TUI Terminal Tabs Single-Pane](keep_tui_terminal_tabs_single_pane_49.md)
