# Activate tab opening and closing only on key press

## Context and Problem Statement

Opening and closing terminal tabs matched key identity without checking the
event type. In terminals reporting Kitty keyboard events, one physical tap
opened a tab on press and another on release; holding the key opened more.
Closing also ran on repeats and release, removing the newly active neighbor.
The existing focus-toggle guard did not cover tab actions.

## Decision Drivers

* Each physical press must open or close one tab.
* Followup events must not reach a different shell after the active tab changes.
* Rapid separate presses, legacy shortcuts and guest input must keep working.

## Considered Options

* Add a time-based debounce to tab actions.
* Reject non-press events in shortcut matchers.
* Declare press-only activation in the shared binding table.

## Decision Outcome

Chosen option: "declare press-only activation in the shared binding table",
because identity matching must still consume the full shortcut lifecycle.
Guest-tab open, host-tab open, close and focus toggle declare `pressOnly`.
After paste handling, dispatch recognizes each shortcut before suppressing its
repeat/release action. The terminal payload fast path uses the same matchers.
Other actions retain their current behavior pending the separate shortcut audit.

Decoded-input tests cover Alt/Meta combinations, Option glyphs, both key/event
dispatch routes, tree/terminal focus, orphan followups, taps, holds, successive
presses, adjacent-tab protection and last-tab focus changes. Ordinary shell
repeats/releases and pasted shortcut-shaped text remain payload.

### Positive Consequences

* Tab count changes once per reported press, without a timing threshold.
* A close release cannot close the adjacent tab or produce a last-tab error.
* Focus and tab actions share one explicit activation policy.

### Negative Consequences

* Legacy terminals encode autorepeat as indistinguishable presses.
* Other shortcut actions still require an explicit activation-policy audit.

## Pros and Cons of the Options

### Time-based debounce

* Good, because it can hide duplicate input.
* Bad, because it drops legitimate rapid presses and depends on timing.

### Press-only identity matching

* Good, because it prevents duplicate action dispatch.
* Bad, because repeats/releases become payload for the newly active shell.

### Activation policy in the binding table

* Good, because the matcher consumes followups while activation happens once.
* Good, because it preserves guest keyboard events and paste handling.
* Bad, because each action still needs a deliberate policy.

## Links

* Refines [Consume focus-toggle repeats and releases](consume_focus_toggle_repeat_and_release_events_134.md).
* Complements [Open node host shells as ordinary tabs](open_node_host_shells_as_ordinary_terminal_tabs_77.md).
