# Use contextual read-only key help

## Context and Problem Statement

The Help artboard presents grouped bindings with the opening context first.
The initial frontend reused the command palette, so help was a flat list,
omitted newer controls and could not close with `?`. Compact terminals must
retain access to every binding without dispatching the shortcuts being read.

## Considered Options

* Extend the executable command palette with help-specific modes and layout.
* Give help a read-only catalogue and a dedicated overlay.
* Generate help by restructuring all manager dispatchers into an action registry.

## Decision Outcome

Use a dedicated overlay with descriptive groups and sections. Capture the
opening context when help opens and put that group first. Wide terminals show
four columns; medium terminals show two; compact terminals show one. Tab or
Left/Right visits all groups, while arrows, page keys and the mouse wheel scroll
within a column. Focus and per-group scroll positions survive resize. Mouse
regions are rebuilt from each rendered layout.

`/` searches binding keys, descriptions, groups and sections using all entered
case-insensitive terms. Search results are a scrollable list with context
headings. Tab returns to columns; Esc or `?` closes. Help owns its input,
including bracketed paste, and has no executable callbacks. Repeated navigation
is permitted; actions require a press. The existing shortcut ownership layer
suppresses inherited repeat/release events after opening or closing the overlay.

Use the existing theme, modal host, text input and grapheme wrapping. Highlight
the focused column and dim the underlying manager. All observations, daemon
owners and ordinary/session/renderer/handoff protocols remain unchanged
(27/2/6/7). The catalogue describes implemented keys; it does not claim the
artboard's `settings.yaml › keys` remapping, which remains an explicit roadmap
and TODO item.

### Positive Consequences

* Help follows the design and leads with the user's current view.
* Compact terminals, search and scrolling expose the complete catalogue.
* Reading or pasting shortcut-shaped text cannot execute a manager action.

### Negative Consequences

* Catalogue descriptions must be maintained alongside the dispatchers.
* Dense contexts require scrolling, and later groups require navigation.
* Configurable bindings and native platform qualification remain separate work.

## Pros and Cons of the Options

Extending the palette would reuse filtering, but retain executable state in a
reader and entangle two different interaction models. The dedicated overlay
keeps ownership explicit and reuses existing rendering/input primitives.
A unified action registry could prevent documentation drift and support
remapping, but changes shortcut precedence across forms, shells and views; it
needs its own implementation and regression work.

## Links

* [Help artboard](../plans/tui-vnext/reference/Help.dc.html)
* [Key bindings plan](../plans/KEY_BINDINGS_PLAN.md)
* [Delivery ledger](../plans/tui-vnext/README.md)
