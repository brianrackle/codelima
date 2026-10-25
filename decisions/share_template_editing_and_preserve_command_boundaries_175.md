# Share template editing and preserve command execution boundaries

## Context and Problem Statement

The template artboard requires resource steps, ordered environments, multiline
bootstrap editing, a resolved preview and frozen-node impact. Templates and the
create-node picker previously had separate forms with different capabilities.
Splitting a pasted script on newlines would change its shell execution boundary.

## Considered Options

* Extend each caller's generic dialog independently.
* Launch an external editor for the complete configuration file.
* Share a domain editor and retain one multiline buffer per command.

## Decision Outcome

Use one full-screen template overlay from Templates, Spec and the create-node
picker. Parent overlays retain their unsaved values. Generic input widgets own
text editing and the existing dialog owns dirty-discard checks. Resource fields
support presets and typed sizes; selected environments retain execution order
and stable identity while toggling or moving. Built-in restoration changes a
reviewable draft, and the default template's name remains protected.

Load definitions and global usage outside the event loop. Save through the
existing configuration service, retaining editable values on failure. Completion
events identify the exact editor. The save notice reports the current number of
existing nodes that retain their frozen values. Installed Lima images come from
the read-only, bounded `limactl create --list-templates` command; unavailable
catalogs leave direct template/path/URL input usable.

Share the environment/template bootstrap resolver with node creation. Add the
selected agent's install and validation steps to the preview, including custom
profiles. Compact windows can switch to a wrapped, scrollable preview. Metadata
is a snapshot of current definitions; a later environment edit can still change
what a future node receives.

The command editor keeps one Unicode-aware buffer per command. Newlines and
paste stay within that buffer; adding, removing or reordering command blocks is
explicit. Applying the command editor updates only the parent draft. Saving the
template remains a separate action. Wide panels and compact focus-following
scrolling use the same input model.

Bracketed paste recovers Vaxis's control-key events before normalizing complete
CRLF pairs, so line breaks and tabs survive real terminal parsing. The vendored
text input adds an opt-in `PreserveTabs` mode used only for command buffers;
literal tabs remain editable as one character displayed as ⇥. Other fields keep
the upstream default expansion. Preview rows mark line breaks with ↵.

### Positive Consequences

* Every template entry point exposes the same controls and validation.
* Preview and node creation use the same environment ordering rules.
* Script editing preserves shell state across lines of a command.
* Metadata locks and catalog subprocesses cannot block terminal input.

### Negative Consequences

* Nested editors need completion fencing and explicit draft ownership.
* The command editor adds cursor/wrapping behavior that needs Unicode coverage.
* Definition snapshots cannot predict edits made before a future node creation.

## Validation

`make test-template-editor` covers frozen-node isolation, preview parity with
new nodes, ordering/clearing, resource steps, protected defaults, dirty discard,
held-lock responsiveness, nested picker return, multiline paste, Unicode cursor
movement, compact rendering and catalog cancellation. Full and manual checks
are recorded in the design delivery ledger.

## Links

* [Template editor reference](../plans/tui-vnext/reference/TemplateEdit.dc.html)
* [Create form ownership](validate_create_forms_outside_the_ui_loop_174.md)
