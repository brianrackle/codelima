# Ensure a Terminal for Every Selected Running Node

## Context and Problem Statement

ADR 87 made Terminal the default only for the initially selected running node and deliberately kept that pane choice sticky across later navigation. As a result, moving from an Info view to another running node could show metadata or an empty terminal placeholder even though the VM was ready for a shell. A running node's default view should be its usable terminal whenever that node is selected.

## Decision Drivers

* Every selected running node should immediately present a usable terminal.
* An existing guest or host tab must be reused instead of creating duplicates.
* Stopped, created, failed, and terminated nodes must remain Info-first and must not start guest shells.
* Tree navigation must retain keyboard focus.
* An explicit `i` choice should remain stable while the operator stays on the same node.
* Runtime refreshes must react when the selected VM starts or stops.

## Considered Options

* Keep the startup-only default from ADR 87.
* Make pane mode status-aware but leave terminal creation explicit.
* Make selection status-aware and ensure the running node's first terminal.

## Decision Outcome

Chosen option: "make selection status-aware and ensure the running node's first terminal", because Terminal is useful as a default only when it contains a live shell.

When tree selection changes, a running node selects Terminal mode and reuses its active tab, falls back to another existing tab, or opens one guest tab. A non-running node selects Info mode and opens nothing. Tree focus does not change. The same synchronization runs after node reload so a selected VM crossing from stopped to running gains a terminal, while a running-to-stopped transition returns to Info.

An `i` toggle remains explicit while the same selected node retains the same running readiness. Selecting another node restores that node's status-derived default. Automatic refresh with no selection or readiness change does not overwrite the current node's explicit choice.

### Positive Consequences

* Navigating to a running node always produces a usable terminal view.
* Existing and restored tabs are reused without duplicate shells.
* Starting the selected VM promotes it to Terminal automatically.
* Stopped nodes never imply that a guest shell exists.

### Negative Consequences

* Tree navigation can now initiate a guest terminal RPC.
* An open failure is surfaced during selection rather than after an explicit terminal action.
* Pane mode is no longer sticky across different nodes.

## Pros and Cons of the Options

### Keep the startup-only default

* Good, because navigation never has side effects.
* Bad, because later running-node selections can show the wrong default or an empty terminal.

### Make pane mode status-aware without ensuring a terminal

* Good, because navigation remains cheap.
* Bad, because Terminal can still be selected with no usable shell behind it.

### Make selection status-aware and ensure the first terminal

* Good, because the visible default and available session remain consistent.
* Good, because existing tabs prevent unnecessary shell creation.
* Bad, because selection can report a terminal-open error.

## Links

* Supersedes [ADR 87](default_running_node_split_pane_to_terminal_87.md) for post-startup selection and sticky cross-node pane mode.
* Refines [ADR 52](scope_tui_terminal_tabs_to_focused_target_with_explicit_option_controls_52.md).
