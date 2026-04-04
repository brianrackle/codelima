# Prefer info-first split-pane tabs in the TUI

## Context and Problem Statement

The TUI already separates tree focus from fullscreen terminal focus, and the split pane already supports both info and terminal content for projects and nodes. The previous decision made the split pane default to terminal preview for every selection, but that meant routine tree navigation eagerly opened preview sessions and hid project or node metadata until the operator toggled back to info. We needed the split pane to surface metadata first while keeping fullscreen terminal focus fast and predictable.

## Decision Drivers

* make the default split-pane content immediately informative for both projects and nodes
* keep `Alt-\`` and `F6` dedicated to tree-versus-fullscreen-terminal focus changes
* avoid opening preview sessions just because the tree selection changed
* keep the `i` toggle simple and sticky across selection changes

## Considered Options

* keep terminal-first split-pane tabs and default selection behavior
* make info the leading split-pane tab and the default tree-pane mode
* return to selection-specific defaults where projects show info and nodes show terminal preview

## Decision Outcome

Chosen option: "make info the leading split-pane tab and the default tree-pane mode", because it exposes the most useful contextual metadata immediately while preserving the existing fullscreen terminal focus contract and the sticky `i` toggle.

### Positive Consequences

* the right pane opens on `[Info] Terminal` for both projects and nodes, so the default view matches the highlighted tab
* project and node metadata are visible immediately without requiring an extra toggle
* preview sessions are created only when the operator asks for terminal preview or fullscreen focus

### Negative Consequences

* split-pane terminal preview now takes one extra `i` press from the default tree view
* the split pane feels slightly less shell-forward even though fullscreen terminal focus is unchanged
* QA and docs must cover the inverted default and tab order explicitly

## Pros and Cons of the Options

### keep terminal-first split-pane tabs and default selection behavior

Continue defaulting the split pane to terminal preview with `Terminal` as the leading tab.

* Good, because it keeps split-pane shell preview one selection away.
* Good, because it matches the earlier shell-first bias in the split pane.
* Bad, because it hides metadata that operators often need before deciding to focus a shell.
* Bad, because selection changes start preview sessions even when the operator is only inspecting the tree.

### make info the leading split-pane tab and the default tree-pane mode

Render `[Info] Terminal` by default in tree focus and keep `i` as the sticky toggle into terminal preview.

* Good, because the default split pane becomes immediately useful for inspection and navigation.
* Good, because tab order now matches the default active view.
* Bad, because split-pane terminal preview is no longer the first thing shown for running targets.
* Bad, because the product messaging has to distinguish the info-first split pane from fullscreen terminal focus.

### return to selection-specific defaults where projects show info and nodes show terminal preview

Let the selected entry type decide whether the split pane opens on info or terminal content.

* Good, because it optimizes each entry type independently.
* Good, because it can make running nodes feel shell-centric without changing project inspection.
* Bad, because the split pane becomes inconsistent and harder to predict.
* Bad, because session creation and footer hints depend on selection type instead of one stable pane mode.

## Links

* Refines [separate_tree_pane_mode_from_terminal_focus_39.md](separate_tree_pane_mode_from_terminal_focus_39.md)
