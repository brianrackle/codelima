# Frame the opt-in embedded shell with shared viewport geometry

## Context and Problem Statement

The embedded-shell artboard requires node context, session tabs, a bordered
terminal, focus ownership and observed forwarding status. The old unframed
surface exposed a Shell detail whenever a session existed, including sessions
opened through raw attachment. Initial sizing also retained the old split-pane
geometry. Decoration must not overwrite terminal cells or shift mouse input.

## Decision Drivers

* Keep embedded mode explicitly enabled through `shell.mode`.
* Preserve daemon session identity, terminal key routing and raw attachment.
* Use one geometry for launch, resize, rendering and pointer translation.
* Show observed session/route state without copying sample design data.

## Considered Options

* Decorate the existing session owner with an explicitly bounded viewport.
* Paint focus and tabs over terminal content.
* Introduce a second terminal/session owner for the redesigned view.

## Decision Outcome

Choose decoration around the existing session owner. Shell detail is present
only in embedded mode, including with no sessions. Changing to another mode
returns to Overview and TUI focus without closing sessions. Initial and later
sizes reserve four columns and thirteen rows for manager chrome. The terminal
body rectangle comes from the actual bordered child window. The focus control
sits on the border, outside the guest drawing area.

The session strip keeps the active identity visible as width changes. Labels
share existing metadata and bell acknowledgement behavior; host accents remain
coral. Mouse tab targets carry node, session and terminal identities and reject
closed or replaced sessions. Detail navigation and the breadcrumb are clickable.
New guest/host controls use existing launch paths and reject known daemon
disconnection before issuing calls. F6 remains the only intercepted shell key.

The footer uses the existing freshness, readiness and route authorization
checks. A cached or absent route is not advertised as live. This change does not
alter daemon, terminal, renderer or attachment protocol versions.

### Positive Consequences

* Borders and focus indications cannot erase terminal output.
* Startup and resize share dimensions; focus changes preserve geometry.
* Existing sessions remain available across mode changes.
* Raw attachment continues to own the physical terminal independently.

### Negative Consequences

* At the minimum supported height the terminal has only one content row.
* Compact session strips show fewer titles and require tab navigation.
* Attached-mode presentation and full native qualification remain separate
  requirements tracked in TODO #57.

## Pros and Cons of the Options

### Decorate the existing owner

* Good: preserves process lifetime, selection, search and renderer ownership.
* Good: permits identity validation and a single viewport contract.
* Bad: manager chrome reduces available terminal cells.

### Paint over terminal content

* Good: would resemble the mockup's overlaid focus label literally.
* Bad: overwrites shell output and conflicts with selection/mouse coordinates.

### Add another owner

* Good: allows isolated layout code.
* Bad: duplicates attachment, focus, lifetime and resize responsibilities.

## Links

* [Shell chooser identity](bind_shell_choices_to_captured_sessions_194.md)
* [Source](../plans/tui-vnext/reference/ShellEmbedded.dc.html)
* [Verification and remaining scope](../plans/tui-vnext/README.md)
