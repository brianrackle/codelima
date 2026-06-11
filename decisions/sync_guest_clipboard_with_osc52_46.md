# Sync guest clipboard with OSC 52

## Context and Problem Statement

Applications inside the embedded VM terminal can emit OSC 52 clipboard sequences, but the Ghostty-backed embedded terminal did not forward those clipboard writes to the host clipboard. CodeLima needs a VM-to-host clipboard sync path that works without reintroducing mouse-selection ownership inside the terminal pane.

## Decision Drivers

* Support guest applications that use standard terminal clipboard sequences.
* Keep mouse selection and copy gestures owned by the host terminal emulator.
* Avoid depending on a Ghostty terminal clipboard callback that is not exposed by the current bridge.

## Considered Options

* Reintroduce CodeLima-owned drag-to-copy selection.
* Add a manual clipboard-copy keybind only.
* Scan the Ghostty PTY stream for OSC 52 and push decoded payloads to the host clipboard.

## Decision Outcome

Chosen option: "Scan the Ghostty PTY stream for OSC 52 and push decoded payloads to the host clipboard", because OSC 52 is the terminal-native contract guest tools already use and it does not compete with mouse-aware guest applications.

### Positive Consequences

* Guest clipboard writes can update the host clipboard while the TUI is running.
* The existing host-selection bypass guidance remains unchanged.
* The Ghostty compatibility bridge does not need a new native callback surface.

### Negative Consequences

* The Ghostty backend now has a small Go-side OSC 52 scanner.
* Clipboard sync depends on host clipboard support through Vaxis or local platform commands.
* Clipboard queries are ignored; this only syncs guest writes to the host.

## Pros and Cons of the Options

### Reintroduce CodeLima-owned drag-to-copy selection

Bring back local terminal-pane selection and copy behavior.

* Good, because it gives CodeLima a uniform manual copy gesture.
* Bad, because it conflicts with guest mouse capture and reverses the simplified mouse ownership model.

### Add a manual clipboard-copy keybind only

Add a keybinding that copies selected or visible terminal content to the host clipboard.

* Good, because it is explicit.
* Bad, because it does not help guest tools that intentionally emit clipboard writes.

### Scan the Ghostty PTY stream for OSC 52 and push decoded payloads to the host clipboard

Detect OSC 52 sequences from guest output before passing the same bytes through to Ghostty.

* Good, because it follows terminal clipboard conventions.
* Good, because it keeps rendering and mouse behavior unchanged.
* Bad, because it adds local parsing until Ghostty exposes a suitable callback.

## Links

* Refines [Rely on terminal emulator bypass gestures for embedded terminal selection](rely_on_terminal_emulator_bypass_gestures_for_embedded_terminal_selection_36.md)
