# Separate tree pane mode from terminal focus in the TUI

## Context and Problem Statement

The TUI already used `Alt-\`` or `F6` as the layout and focus toggle between split tree view and fullscreen terminal view. At the same time, the split-pane content was inconsistent: projects defaulted to info while running nodes defaulted to terminal preview. That made the right pane depend on the selected entry type instead of an explicit operator choice, and it left no consistent way to inspect project info without changing the terminal focus contract.

## Decision Drivers

* keep `Alt-\`` and `F6` dedicated to tree-versus-terminal focus changes
* make the split-pane default consistent for both project and node selections
* reuse the same terminal session for preview and fullscreen focus
* preserve the existing right-pane override model for dialogs, menus, and selectors

## Considered Options

* keep selection-specific defaults where projects show info and nodes show terminal preview
* make info the split-pane default everywhere and use a separate key to reveal terminal preview
* separate tree-pane mode from terminal focus and default the tree pane to terminal for both projects and nodes

## Decision Outcome

Chosen option: "separate tree-pane mode from terminal focus and default the tree pane to terminal for both projects and nodes", because it preserves the existing fullscreen terminal focus contract while giving the split pane one explicit, consistent content model.

### Positive Consequences

* projects and nodes now share the same default right-pane behavior in tree focus
* the TUI can preview or focus a project-local shell without inventing a second fullscreen rule
* `i` becomes a simple sticky inspect toggle instead of competing with terminal focus

### Negative Consequences

* the TUI state model now has to track both top-level focus and a sticky tree-pane mode
* the session store must manage both project-local host shells and node guest shells under one target-key abstraction
* manual QA now needs to cover project-terminal preview, sticky `i` behavior, and pane-mode restoration after fullscreen focus

## Pros and Cons of the Options

### keep selection-specific defaults where projects show info and nodes show terminal preview

Retain the old behavior where the selected entry type decides what the split pane renders.

* Good, because it avoids changing the current project-info default.
* Good, because it keeps the state model simpler.
* Bad, because the split view stays inconsistent and harder to predict.
* Bad, because projects still cannot participate in the same preview-and-focus shell model as nodes.

### make info the split-pane default everywhere and use a separate key to reveal terminal preview

Normalize around info-first inspection and treat terminal preview as a secondary split-pane mode.

* Good, because it preserves the current project-info-first experience.
* Good, because it makes the info surface prominent for all selections.
* Bad, because it works against the shell-first TUI direction.
* Bad, because it weakens the connection between split-pane preview and fullscreen terminal focus.

### separate tree-pane mode from terminal focus and default the tree pane to terminal for both projects and nodes

Keep focus as `tree` versus `terminal`, add a sticky tree-pane `terminal` versus `info` mode, and reuse target-keyed sessions for both preview and fullscreen focus.

* Good, because it gives both projects and nodes one consistent default surface.
* Good, because it lets `Alt-\`` and `F6` keep their existing focus-only meaning.
* Bad, because the TUI has more state transitions to test and document.
* Bad, because stopped nodes now need a terminal-oriented placeholder instead of silently falling back to info.

## Links

* Refines [render_transient_tui_views_in_the_right_pane_34.md](render_transient_tui_views_in_the_right_pane_34.md)
* Refines [couple_tree_visibility_to_tui_focus_14.md](couple_tree_visibility_to_tui_focus_14.md)
* Refined by [prefer_info_first_split_pane_tabs_40.md](prefer_info_first_split_pane_tabs_40.md)
