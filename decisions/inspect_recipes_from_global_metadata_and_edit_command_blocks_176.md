# Inspect recipes from global metadata and edit command blocks

## Context and Problem Statement

The Templates and Environments artboards need usage, frozen-value drift,
ordered commands and recorded execution outcomes. The previous lists omitted
these details and selected by row number, so refreshes could change the target.
Using the scoped fleet for usage would incorrectly hide references outside the
current directory. The environment submenu also made multiline editing awkward.

## Considered Options

* Extend the scoped fleet rows with recipe information.
* Read metadata synchronously while drawing each panel.
* Load a global recipe snapshot and share the existing command-block editor.

## Decision Outcome

Load global recipe usage outside the input loop, using the manager's generation
fence. Extract the metadata portion of Node Spec so the catalog and Spec use
the same frozen/current comparison, without opening Lima files or contacting
the runtime for catalog inspection. Deleted nodes can contribute historical
bootstrap evidence but do not contribute to current template usage. A cloned
bootstrap record cannot claim that the clone executed the original's commands.

Render counted tabs, a scrollable list and details with cell-width columns and
stable recipe IDs. Wide terminals show both panels; compact terminals switch
between list and details. Keyboard and mouse actions use the rendered bounds.
Missing execution evidence is shown explicitly. Built-in markers compare the
current definition with the shipped seed values.

Environment create, clone, command edits and restoration use a draft and the
shared multiline command editor. Saving runs outside the input loop and errors
retain the draft. Existing environment names remain fixed; cloning supplies a
new name without rewriting template references. The optional EDITOR action
edits one command block in private metadata scratch, suspends/resumes the TUI,
limits returned content and removes scratch on success or failure. Its result
still requires an explicit save. No script is executed by editing it.

Deletion continues to use service locks and authoritative reference checks;
errors now name the blocking node or template. Completed metadata operations
reload the visible catalog as well as the fleet, so a deleted selection clamps
immediately. Notices replace the explanatory line above the action row instead
of obscuring clickable controls. No schema or daemon protocol
change is required for catalog inspection and editing.

### Positive Consequences

* Usage remains correct when the fleet is filtered by directory.
* Catalog drift and Node Spec cannot diverge through separate comparison rules.
* Script blocks preserve newlines, tabs and execution boundaries.
* Failed saves retain user edits, and external editing leaves no scratch files.

### Negative Consequences

* Global metadata inspection costs more than reading the scoped fleet alone.
* Historical execution can be unavailable when retained metadata is missing.
* The disposable environment test action still requires separate daemon-owned
  execution and cleanup; a frontend goroutine cannot provide that guarantee.

## Validation

The recipe checks cover global usage, drift, recorded execution, stable
selection, wide/compact layouts, mouse targets, command boundaries, save
failures, defaults, external-editor arguments and scratch cleanup. Full gates
and live verification are recorded in the design delivery ledger.

## Links

* [Templates reference](../plans/tui-vnext/reference/Templates.dc.html)
* [Environments reference](../plans/tui-vnext/reference/Environments.dc.html)
* [Shared command editor](share_template_editing_and_preserve_command_boundaries_175.md)
