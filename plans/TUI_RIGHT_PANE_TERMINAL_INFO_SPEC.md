# TUI Running-Node Default Pane Specification

Status: Implemented v2 (Go / Vaxis / Ghostty VT)
Purpose: Define the conditional startup pane default for schema-v4 nodes while preserving explicit, sticky pane selection after initialization.

## Assumptions

- The schema-v4 TUI contains a flat node list and no user-facing project rows.
- Guest terminals are available only when `nodeAutoStartsSession` reports a node as running.
- Daemon-backed terminal tabs may already exist when a TUI starts.

## 1. Problem Statement

The TUI opens or reconnects one guest terminal tab for its initially selected running node but historically kept the right pane on info. This hides a live shell behind an extra toggle. A non-running node must not imply that a guest shell is available or create one as a side effect of rendering.

## 2. Goals and Non-Goals

### 2.1 Goals

- Default the initially selected running node to terminal pane mode.
- Default an initially selected non-running node to info pane mode.
- Keep keyboard focus in the node list at startup.
- Open at most one startup guest tab, reusing a restored daemon tab when present.
- Preserve `i` as a sticky explicit pane-mode override after startup.

### 2.2 Non-Goals

- Recompute pane mode on every selection, refresh, or runtime-status transition.
- Persist pane mode across TUI processes.
- Change fullscreen terminal focus, tab switching, tab closing, or host-tab behavior.
- Open a guest shell for a stopped, created, failed, or unavailable node.

## 3. State and Invariants

- `focus` has values `tree` and `terminal`.
- `treePaneMode` has values `info` and `terminal`.
- The initial selection is resolved before the initial `treePaneMode`.
- Startup defaults are:
  - selected running node: `focus = tree`, `treePaneMode = terminal`
  - selected non-running node: `focus = tree`, `treePaneMode = info`
  - empty selection: `focus = tree`, `treePaneMode = info`
- Initialization must not set fullscreen terminal focus.
- Once initialization completes, selection and refresh must not alter `treePaneMode`.

## 4. Startup Contract

1. Load and index the node tree.
2. Select the first node according to the existing initial-selection rules.
3. Resolve the default pane mode from that selected entry exactly once.
4. Initialize Vaxis and calculate the preferred embedded-terminal geometry.
5. For a running selected node:
   - reuse its first restored tab when one exists;
   - otherwise open one guest tab at the preferred geometry;
   - render `Info [Terminal]` while retaining tree focus.
6. For a non-running selected node:
   - open no guest tab;
   - render `[Info] Terminal`.

A startup tab failure must leave the TUI usable, retain tree focus and terminal pane mode, and surface the existing terminal error state.

## 5. Interaction Contract

- `i` in tree focus toggles `treePaneMode`.
- The result of `i` is sticky across node selection and data refresh.
- `Alt-Backtick` or `F6` changes tree/fullscreen focus without changing `treePaneMode`.
- Explicit tab-open commands may reveal terminal pane mode under their existing contract.
- A node-start completion does not override the current pane mode merely because the node became running.
- Closing or exiting a terminal leaves existing fallback and error behavior unchanged.

## 6. Rendering and Observability

- Terminal mode highlights `Terminal` as `Info [Terminal]`.
- Info mode highlights `Info` as `[Info] Terminal`.
- Footer hints must describe the action reachable from the current mode: `[i] info` from terminal and `[i] terminal` from info.
- Logs and persisted node metadata gain no new fields.

## 7. Test and Validation Matrix

### 7.1 Automated

- A running initial node resolves terminal pane mode.
- Startup opens exactly one guest tab for the running initial node.
- Repeating startup-tab initialization creates no replacement tab.
- A stopped initial node resolves info pane mode and opens no tab.
- The pane border highlights terminal for a running initial node.
- `i` toggles to info, remains sticky across selection, and toggles back to terminal.
- Existing focus, tab, resize, paste, daemon detach, and terminal-session tests remain green.

### 7.2 Manual

- Launch the path-scoped TUI with a running initial node and verify `Info [Terminal]` appears while Up/Down still controls the node list.
- Toggle to info, move between nodes, and verify info remains selected until toggled again.
- Reopen with a stopped initial node and verify `[Info] Terminal` appears without creating a replacement guest shell.
- Confirm fullscreen focus and return preserve the prior split-pane mode.

## 8. Implementation Checklist

- Resolve `treePaneMode` after initial selection.
- Add running and stopped startup regression tests.
- Update the TUI user guide and QA flow.
- Record the superseding architecture decision.
- Mark roadmap priority 5 complete locally and Track 6.2 partially complete.
