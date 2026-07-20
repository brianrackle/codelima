# Default a Running Node's Split Pane to Terminal

## Context and Problem Statement

ADR 40 made the TUI split pane info-first for every initial selection. The schema-v3 TUI is now a shell-first, flat node list, and it already opens or reconnects one guest terminal tab for the initially selected running node. Showing node metadata while that live tab sits hidden makes startup feel inconsistent and adds an unnecessary `i` press. The default must still avoid presenting a guest-terminal surface for a node that is not running.

## Decision Drivers

* make the initial running-node experience match CodeLima's shell-first purpose
* preserve tree keyboard focus so navigation remains immediately available
* avoid opening a guest shell for stopped, created, or unavailable nodes
* preserve `i` as a sticky explicit operator choice after initialization
* reuse a surviving daemon terminal instead of creating a replacement shell

## Considered Options

* keep every initial node info-first
* default every initial node to terminal mode and show a placeholder for non-running nodes
* choose the initial pane mode from the selected node's runtime readiness, then keep it sticky

## Decision Outcome

Chosen option: "choose the initial pane mode from the selected node's runtime readiness, then keep it sticky", because it makes a live shell visible immediately without weakening the no-shell behavior for non-running nodes.

At TUI state initialization, the selected node uses terminal pane mode when `nodeAutoStartsSession` reports it running; otherwise it uses info pane mode. After preferred terminal geometry is known, startup opens one guest tab only for the running initial node, reusing an already restored daemon tab when present. Focus remains on the node list. Selection changes, refreshes, and later status changes do not recompute the pane mode; `i` remains the operator-controlled sticky override.

### Positive Consequences

* a running initial node renders its terminal immediately
* stopped nodes remain metadata-first and do not spawn guest shells
* terminal preview and fullscreen focus continue to share one tab
* navigation never overrides an explicit `i` choice

### Negative Consequences

* initial pane mode is conditional rather than one global constant
* starting a node after TUI initialization does not silently replace an explicit info selection
* documentation and QA must cover both startup branches

## Pros and Cons of the Options

### keep every initial node info-first

* Good, because every startup uses one constant mode.
* Bad, because a running node's already-open terminal remains hidden.
* Bad, because the shell-first path requires an extra input on every launch.

### default every initial node to terminal mode

* Good, because the initial tab selection is uniform.
* Bad, because stopped nodes open on a terminal placeholder instead of useful metadata.
* Bad, because terminal mode could imply that a guest shell is available when it is not.

### choose from initial runtime readiness, then keep the mode sticky

* Good, because the visible default matches whether a guest terminal can exist.
* Good, because the decision is made once and does not fight later operator input.
* Bad, because tests must cover running and non-running initial selections separately.

## Links

* Supersedes [prefer_info_first_split_pane_tabs_40.md](prefer_info_first_split_pane_tabs_40.md) for the initially selected running node
* Refines [scope_tui_terminal_tabs_to_focused_target_with_explicit_option_controls_52.md](scope_tui_terminal_tabs_to_focused_target_with_explicit_option_controls_52.md)
