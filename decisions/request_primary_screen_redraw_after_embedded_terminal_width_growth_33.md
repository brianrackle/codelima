# Request Primary-Screen Redraw After Embedded Terminal Width Growth

## Context and Problem Statement

The embedded Ghostty terminal reflowed primary-screen content immediately when the host pane grew wider. Interactive shell prompts then received `SIGWINCH` and redrew against the new width, but readline-style cleanup sequences still assumed the old wrapped prompt layout. The result was duplicated prompt fragments left behind in the visible viewport after horizontal growth.

## Decision Drivers

* Restore clean prompt rendering after host width growth
* Keep the existing Ghostty-backed terminal path for richer rendering features
* Avoid a larger terminal-backend replacement while the issue is isolated to shell-like primary-screen redraws

## Considered Options

* Keep the current immediate-resize behavior
* Try to defer or split Ghostty reflow around `SIGWINCH` handling
* Request a shell-side redraw after width growth for shell-like primary-screen sessions

## Decision Outcome

Chosen option: "Request a shell-side redraw after width growth for shell-like primary-screen sessions", because it fixes the real prompt corruption reproduced with interactive `bash` while keeping the existing Ghostty backend and avoiding a more invasive terminal-model workaround in this change.

### Positive Consequences

* Shell prompts redraw cleanly after widening the host terminal
* The fix stays localized to the embedded terminal resize path
* Alternate-screen apps and mouse-capturing terminal apps are excluded from the workaround

### Negative Consequences

* The fix is a workaround that relies on `Ctrl-L` semantics in shell-like primary-screen applications
* A future Ghostty-side or resize-sequencing fix could make the redraw shim unnecessary
* Primary-screen applications that are not readline-based but still match the guard may receive an unsolicited redraw request

## Pros and Cons of the Options

### Keep the current immediate-resize behavior

Leave the embedded Ghostty terminal resize path unchanged.

* Good, because it keeps the resize path simple
* Bad, because shell prompts visibly corrupt after horizontal growth
* Bad, because the regression is easy to reproduce during normal interactive use

### Try to defer or split Ghostty reflow around `SIGWINCH` handling

Delay Ghostty reflow or try to process cleanup sequences against the old layout before adopting the new width.

* Good, because it targets the root mismatch between old-layout cleanup and new-width redraw
* Good, because it avoids sending extra input to guest applications
* Bad, because it requires more invasive terminal-model coordination and was not resolved cleanly in this change

### Request a shell-side redraw after width growth for shell-like primary-screen sessions

After resizing the PTY, send `Ctrl-L` when the terminal is on the primary screen, at the bottom of the viewport, and not in a mouse-capturing app.

* Good, because interactive shells already use `Ctrl-L` to clear and redraw prompts safely
* Good, because the guard keeps the workaround away from alternate-screen terminal apps
* Bad, because it is behaviorally narrower than a true terminal reflow fix

## Links

* Pattern [PATTERNS.MD](/Users/brianrackle/Projects/codelima/PATTERNS.MD)
* Template [ADR_TEMPLATE.md](/Users/brianrackle/Projects/codelima/ADR_TEMPLATE.md)
