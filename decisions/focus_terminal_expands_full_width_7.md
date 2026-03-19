# Focused terminal expands to full width

## Context and Problem Statement

The TUI previously kept the project tree visible even after the operator focused a node terminal. That left less room for shell applications and made terminal focus feel like a minor mode change instead of a true shell-first view. We needed a focus model that gives the shell the full screen width while still making it easy to return to tree navigation.

## Decision Drivers

* shell-first terminal use should prioritize terminal space over always-visible navigation chrome
* returning from shell mode to tree mode must be fast and preserve the existing session
* the behavior needs to stay testable without relying only on fullscreen manual checks
* the tree-return shortcut should work on macOS while keeping a fallback for other terminals

## Considered Options

* keep the split layout visible in both focus modes
* hide the tree only behind a separate explicit toggle action
* hide the tree automatically whenever terminal focus is active

## Decision Outcome

Chosen option: "hide the tree automatically whenever terminal focus is active", because it matches the shell-first product direction with the least operator friction. The tree remains a navigation mode, while terminal focus becomes a full-width shell mode that can be exited with `Cmd-\`` or the existing `Alt-\`` fallback.

### Positive Consequences

* focused shell sessions get the full horizontal space for editors, prompts, and long output
* the selected node session is preserved while the UI switches between navigation and shell modes
* the layout behavior is covered by pure geometry tests instead of only manual fullscreen validation
* macOS users get the requested `Cmd-\`` restore path without dropping the older fallback

### Negative Consequences

* the project tree is no longer visible while the terminal is focused
* `Cmd-\`` depends on the host terminal forwarding the command key; some terminals may still require the `Alt-\`` fallback
* the terminal pane needs separate focus-specific help text because tree action hints are not actionable while shell input owns the keyboard

## Pros and Cons of the Options

### keep the split layout visible in both focus modes

The tree and terminal stay visible at all times.

* Good, because the operator can always see and click the tree
* Good, because it avoids any layout shift on focus changes
* Bad, because shell applications permanently lose a large amount of horizontal space
* Bad, because terminal focus still feels secondary to the navigation chrome

### hide the tree only behind a separate explicit toggle action

The operator manually switches between split and full-width modes.

* Good, because it separates focus from layout changes
* Good, because the tree can remain visible during some focused terminal sessions
* Bad, because it adds another mode toggle to remember
* Bad, because focusing the shell still does not guarantee maximum space

### hide the tree automatically whenever terminal focus is active

Tree focus is the navigation mode, and terminal focus is the shell mode.

* Good, because it makes shell focus immediately useful for editors and long output
* Good, because the restore action is simple and can keep the same node session alive
* Bad, because the tree disappears while shell focus is active
* Bad, because host terminals that swallow the command key still need a fallback binding

