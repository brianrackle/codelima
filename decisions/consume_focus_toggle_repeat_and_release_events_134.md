# Consume focus-toggle repeats and releases without toggling again

## Context and Problem Statement

Option+Backtick visibly flickered back to the original focus in terminals
reporting Kitty keyboard press and release events. Vaxis `Key.Matches` checks
key identity and modifiers independently of event type. Both events reached
the toggle action; holding the key could also toggle repeatedly. F6 followed
the same routing path.

## Decision Drivers

- One physical press must change focus exactly once.
- Rapid separate presses and legacy terminal input must remain responsive.
- Shortcut events must not leak into the shell after focus changes.
- Ordinary guest input, including releases, repeats and paste, must survive.

## Considered Options

- Debounce focus changes with a timer.
- Match focus shortcuts only for press events.
- Match every focus-shortcut event, but activate only on press.

## Decision Outcome

Chosen option: match every focus-shortcut event, but activate only on press.
After paste handling, `handleKey` consumes non-press focus shortcuts before
action dispatch. The matcher and `isTUITerminalPayloadKey` still recognize
their identity on repeats and releases. This covers both direct key handling
and the event loop's terminal payload fast path without changing guest input.

Decoded CSI-u and F6 input tests exercise press/release, press/repeat/release,
unmatched followup events, rapid successive toggles, Alt/Meta variants, legacy
input and payload forwarding through both dispatch paths.

### Positive Consequences

- Focus remains stable after a tap or while holding the shortcut.
- No time threshold, timer, or tracked key state is required.
- The change is limited to the focus-toggle action.

### Negative Consequences

- Legacy protocols that encode autorepeat as indistinguishable presses cannot
  suppress repeat separately; each reported press still toggles.
- Other TUI actions need their own explicit activation policy.

## Pros and Cons of the Options

### Timer debounce

- Can hide duplicate events without inspecting their types.
- Drops legitimate rapid presses and depends on release timing.

### Press-only shortcut matching

- Prevents duplicate action dispatch.
- Misclassifies repeats/releases as payload after terminal focus activates.

### Identity matching with press-only activation

- Consumes the whole shortcut lifecycle and preserves real press ordering.
- Requires the routing and activation distinction to remain explicit.
