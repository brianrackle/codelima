# Decouple terminal focus from expansion in the TUI

## Context and Problem Statement

The TUI previously treated terminal focus as the same state as terminal expansion. Pressing `Enter` or `Tab` focused the shell and hid the tree, while `Cmd-\`` or `Alt-\`` both restored the split layout and moved focus back to the tree. That coupling made common focus changes resize the layout unexpectedly and made it hard to add distinct "focus terminal", "toggle focus", and "expand terminal" bindings. A first attempt used `Cmd-Enter` for focus-plus-expand, but that collided with host terminal behavior on macOS. A follow-up kept `Alt-\`` for layout changes and removed bare `Enter` terminal focus entirely, because `Alt-Enter` already covered keyboard focus changes cleanly.

## Decision Drivers

* Keep focus changes predictable instead of tying them to layout changes
* Avoid host-terminal shortcut collisions on macOS
* Add explicit bindings for focus and layout that do not resize unexpectedly
* Preserve per-node shell sessions while layouts change around them

## Considered Options

* Keep focus-driven expansion and only adjust the existing bindings
* Add explicit terminal expansion state and separate it from focus
* Replace the split layout with a permanently terminal-first fullscreen layout

## Decision Outcome

Chosen option: "Add explicit terminal expansion state and separate it from focus", because it is the only option that supports distinct focus-toggle and layout-toggle bindings without surprising layout changes.

### Positive Consequences 

* `Alt-Enter` can toggle focus between the tree and terminal without changing layout
* `Alt-\`` can toggle layout without destroying or recreating the underlying shell session

### Negative Consequences

* Focus and layout now have to be managed as separate pieces of TUI state
* Footer, help, and QA flows must all describe both focus state and layout state correctly

## Pros and Cons of the Options

### Keep focus-driven expansion and only adjust the existing bindings

Continue hiding the tree whenever terminal focus is active.

* Good, because it keeps the implementation simple
* Good, because it preserves existing muscle memory
* Bad, because focus changes and layout changes remain impossible to distinguish
* Bad, because the layout still changes as a side effect of normal focus movement

### Add explicit terminal expansion state and separate it from focus

Track terminal expansion independently from which pane currently has focus.

* Good, because it supports explicit bindings for focus toggle and layout toggle
* Good, because it keeps per-node terminal sessions stable across layout changes
* Bad, because it adds one more piece of state to render and key handling
* Bad, because the binding set has to be chosen carefully to avoid host-terminal conflicts

### Replace the split layout with a permanently terminal-first fullscreen layout

Always give the terminal full width and treat the tree as an overlay or secondary mode.

* Good, because it simplifies the visible layout states
* Good, because it biases the UI toward the shell-first workflow
* Bad, because it removes the persistent project and node tree that the TUI is built around
* Bad, because project and node management becomes slower and less discoverable

## Links

* Supersedes [focus_terminal_expands_full_width_7.md](focus_terminal_expands_full_width_7.md)
