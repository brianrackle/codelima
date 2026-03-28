# Rely on Terminal-Emulator Bypass Gestures for Embedded Terminal Selection

## Context and Problem Statement

CodeLima's embedded terminal already forwards mouse events to guest applications when the terminal reports mouse capture, which is required for tools such as `htop`, `vim`, and other mouse-aware TUI apps. At the same time, the TUI also maintained its own local drag-to-copy selection path in the terminal pane, which competed with guest mouse input and created a second clipboard behavior inside a surface that is still ultimately rendered inside the host terminal. The question was how to simplify terminal mouse behavior now that the shell pane can take the full width of the UI.

## Decision Drivers

* Keep guest mouse-aware terminal apps working
* Remove CodeLima-specific clipboard behavior from the embedded terminal pane
* Preserve ordinary terminal click behavior such as focus changes and hyperlink activation
* Avoid accidental link opens or focus changes after drag gestures

## Considered Options

* Keep the existing local terminal drag-to-copy path with special-case overrides
* Remove all terminal mouse handling and rely entirely on the host terminal emulator
* Remove local terminal copy behavior while keeping guest mouse forwarding and non-copy click handling

## Decision Outcome

Chosen option: "Remove local terminal copy behavior while keeping guest mouse forwarding and non-copy click handling", because it preserves the useful terminal semantics that require in-app mouse forwarding without keeping a competing clipboard-selection model inside the TUI.

### Positive Consequences

* Guest apps that enable terminal mouse tracking continue to receive clicks, drags, and wheel input.
* CodeLima no longer owns clipboard copy behavior for the embedded terminal pane.
* Ordinary clicks can still focus the terminal or open hyperlinks when the guest is not capturing mouse input.
* Drag gestures inside a non-capturing terminal no longer trigger accidental focus or link-open behavior.

### Negative Consequences

* Host-side text selection now depends on whatever bypass gesture the operator's terminal emulator provides.
* The app can no longer provide a uniform built-in `Shift`-drag copy experience across all host terminals.
* Manual QA needs to validate the host-terminal bypass gesture on real terminals instead of relying on a fully app-owned copy path.

## Pros and Cons of the Options

### Keep the existing local terminal drag-to-copy path with special-case overrides

Continue to detect drag selection inside the terminal pane and copy terminal text through CodeLima-managed clipboard helpers.

* Good, because it gives CodeLima a built-in and consistent copy gesture.
* Good, because host-terminal clipboard integration can be abstracted behind one local path.
* Bad, because it competes with guest mouse-reporting apps and forces more gesture exceptions such as `Shift`-drag.
* Bad, because the embedded pane is still not a native host-terminal surface, so app-owned copy semantics remain inherently approximate.

### Remove all terminal mouse handling and rely entirely on the host terminal emulator

Disable terminal-pane mouse behavior in the TUI and expect the host terminal emulator to handle selection, copy, right click, and mouse-aware apps.

* Good, because it is simple.
* Good, because the host terminal regains full ownership of the pointer.
* Bad, because guest apps such as `htop` would stop receiving mouse events.
* Bad, because CodeLima would lose useful terminal-pane behaviors such as hyperlink activation and local scrollback wheel handling.

### Remove local terminal copy behavior while keeping guest mouse forwarding and non-copy click handling

Forward mouse to the guest when the embedded terminal reports mouse capture, keep ordinary click and wheel behavior for non-capturing terminals, and leave text selection/copy to the host terminal emulator's bypass gesture.

* Good, because it preserves guest mouse support where it matters.
* Good, because it removes the competing local clipboard path from the embedded terminal pane.
* Good, because click-versus-drag tracking still prevents accidental link opens or focus changes after drags.
* Bad, because host-side selection ergonomics now vary by terminal emulator.

## Links

* Refines [use_ghostty_mouse_encoder_for_embedded_terminal_input_26.md](/Users/brianrackle/Projects/codelima/decisions/use_ghostty_mouse_encoder_for_embedded_terminal_input_26.md)
