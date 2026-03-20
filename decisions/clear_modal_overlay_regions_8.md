# Clear modal overlay regions before drawing dialog content

## Context and Problem Statement

The TUI draws menus, dialogs, selectors, and operation panels on top of the tree and terminal panes. After the recent full-width terminal work, modal rectangles could show stale content from the underlying panes anywhere the overlay did not explicitly print a character, which made dialogs look translucent and visually broken.

## Decision Drivers

* modal overlays must fully cover the area they occupy
* the fix should apply uniformly to menus, dialogs, selectors, and operation panels
* the behavior needs a render-level regression instead of relying only on manual fullscreen checks
* the change should not require each overlay widget to remember to clear itself

## Considered Options

* leave overlays transparent and have each dialog or menu print spaces for unused rows
* clear the overlay window centrally before invoking the overlay draw callback
* add a separate dimming or backdrop layer behind overlays

## Decision Outcome

Chosen option: "clear the overlay window centrally before invoking the overlay draw callback", because it fixes every existing overlay type in one place and keeps the overlay widgets focused on their own content instead of backdrop management.

### Positive Consequences

* menus, dialogs, selectors, and progress overlays now fully hide covered pane content
* future overlay types inherit the same behavior automatically
* the regression is covered with a render test against Vaxis screen state

### Negative Consequences

* overlays are now fully opaque instead of incidentally showing underlying content
* the fix depends on a small render helper in the shared overlay path rather than each widget being self-contained

## Pros and Cons of the Options

### leave overlays transparent and have each dialog or menu print spaces for unused rows

Each overlay widget would be responsible for clearing its own footprint.

* Good, because overlay opacity can vary per widget
* Good, because the shared overlay helper stays minimal
* Bad, because every overlay implementation can forget to clear itself
* Bad, because new overlay types would reintroduce the same bug easily

### clear the overlay window centrally before invoking the overlay draw callback

The shared overlay helper fills the overlay rectangle with spaces before the callback draws content.

* Good, because one fix covers all overlay types
* Good, because the render behavior is easy to test in one place
* Bad, because all overlays now share the same opaque behavior
* Bad, because the shared helper owns one more piece of rendering policy

### add a separate dimming or backdrop layer behind overlays

The app would render a backdrop layer and then the overlay on top of it.

* Good, because it could look more polished than a plain opaque clear
* Good, because it opens the door to future modal styling
* Bad, because it adds more rendering complexity than this bug requires
* Bad, because it still needs the overlay area itself to be managed correctly

