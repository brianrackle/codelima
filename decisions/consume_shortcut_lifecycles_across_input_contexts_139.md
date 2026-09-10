# Consume shortcut lifecycles across input contexts

## Context and Problem Statement

After tab and focus actions became press-only, search, Info, node/menu
commands, dialogs and selectors still acted on repeats and releases. A
selector's Enter press restored its parent dialog, then Enter release
submitted that dialog. Escape could dismiss both layers. Repeats of a
menu-opening letter became input in the newly opened form. Navigation also
took an extra step on release.

## Decision Drivers

* One-shot actions execute once per press, including across overlay changes.
* Navigation and text editing retain intentional repeats.
* Shortcut followups must not become input in a newly exposed shell or form.
* Paste and ordinary terminal keyboard events retain their semantics.
* Fresh presses and legacy input remain responsive without a debounce timer.

## Considered Options

* Drop all repeats and releases in the application event loop.
* Add event-type guards to each action independently.
* Combine action-specific activation with shared key-lifecycle dispatch.

## Decision Outcome

Chosen option: "combine action-specific activation with shared key-lifecycle
dispatch", because per-action guards alone cannot recognize a repeat whose
press changed its input destination.

Every key passes through `handleKey`, including overlay/search input and the
terminal payload path. `tuiKeyActivates` admits only presses for one-shot
actions and press/repeat for navigation. Widgets receive pasted text and
paste boundaries before shortcut interpretation. Search paste boundaries go
to its own input, while selector-only dialog fields safely ignore text.

The event-loop-owned claim map retains one-shot global shortcut keys and any
press that changed overlay, search or focus. Their repeats and release are
consumed before dispatch, even when a modifier was released first. Release
removes the claim; a fresh press clears it before routing. Ordinary typing
and navigation need no claim, so legacy input does not accumulate arbitrary
text in this map. Letter shortcuts also match decoded keycodes when CSI-u
input has no associated text field.

Terminal payload still waits for fresh daemon snapshots before redraw.
Claims are local UI state and do not change daemon or persistence protocols.

### Positive Consequences

* Selector confirmation/cancellation cannot act on the restored parent.
* Search, Info and command actions stay stable through holds and releases.
* Held navigation repeats without taking an extra release step.
* Ordinary text, shell input and paste keep their existing input semantics.
* One dispatch path covers both direct key calls and application events.

### Negative Consequences

* Context-changing controls require a small per-window claim map.
* Legacy terminals cannot distinguish autorepeat encoded as fresh presses.
* New actions must choose whether repeating is meaningful.

## Pros and Cons of the Options

### Drop all non-press events

* Good, because it is simple.
* Bad, because it removes useful navigation/text repeats and shell releases.

### Independent action guards

* Good, because each action can specify an activation policy.
* Bad, because repeats can still type into a replacement form or shell.

### Shared lifecycle dispatch with action policies

* Good, because it consumes followups across context changes without timers.
* Good, because it retains guest input and repeated navigation.
* Bad, because dispatch must continue to distinguish UI actions from payload.

## Links

* Refines [Press-only tab opening and closing](activate_tab_open_and_close_only_on_key_press_138.md).
* Refines [Consume focus-toggle repeats and releases](consume_focus_toggle_repeat_and_release_events_134.md).
