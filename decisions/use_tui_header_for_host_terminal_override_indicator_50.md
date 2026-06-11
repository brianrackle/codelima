# Use TUI Header For Host Terminal Override Indicator

## Context and Problem Statement

The host-terminal override needs a persistent warning while input is going to the host-local project shell instead of the node VM. A dedicated red `HOST TERMINAL` row made that visible, but it consumed terminal space and looked like an extra top bar rather than using the TUI chrome that already exists.

## Decision Drivers

* Keep the host-versus-node warning visible for the whole override session.
* Avoid adding a second red bar above the terminal surface.
* Keep terminal body geometry stable when toggling host mode.
* Preserve the selected node and host return target across automatic tree refreshes.

## Considered Options

* Keep the separate red host-terminal row.
* Show only a status-line message.
* Turn the existing top bar red while host override is active.

## Decision Outcome

Chosen option: "Turn the existing top bar red while host override is active", because it keeps the warning persistent without adding another row or changing terminal size.

### Positive Consequences

* Host mode is visible without reducing the terminal body height.
* The terminal does not resize solely because the host override toggled.
* Automatic tree refresh can preserve the host project terminal while keeping the node selected.

### Negative Consequences

* The indicator is subtler than a dedicated `HOST TERMINAL` text row.
* Users must understand that the red top bar means host-local terminal input.

## Pros and Cons of the Options

### Keep the separate red host-terminal row

Draw an additional red row below the header whenever host override is active.

* Good, because the extra text is very explicit.
* Bad, because it consumes a row and changes terminal geometry.
* Bad, because it duplicates top chrome.

### Show only a status-line message

Use transient status text after switching to the host terminal.

* Good, because it adds no top chrome.
* Bad, because later status updates can overwrite it while host mode is still active.

### Turn the existing top bar red while host override is active

Reuse the normal TUI header row as the visual warning.

* Good, because it keeps terminal geometry stable.
* Good, because it avoids a second red bar.
* Bad, because it relies on color rather than additional warning text.

## Links

* Refines [Show Host Terminal Override Indicator](show_host_terminal_override_indicator_47.md)
* Refines [Add Host Terminal Toggle For Node Sessions](add_host_terminal_toggle_for_node_sessions_43.md)
