# TUI Right Pane Terminal And Info Toggle Specification

Status: Draft v1 (Go / Vaxis / Ghostty VT)
Purpose: Define consistent tree-view right-pane behavior so selected projects and nodes default to terminal preview, while `i` toggles the split-pane content between terminal and info without changing fullscreen terminal focus behavior.

## Assumptions

- A project terminal is a host-local interactive shell rooted at the selected project's workspace path.
- This iteration does not redesign the existing project or node info content beyond adding clear pane labels and updated footer hints.
- This iteration does not persist pane-mode preference across TUI restarts. Each TUI launch starts in terminal pane mode.
- Existing key binding configurability work may land separately. Until then, `i` is the default info-toggle binding.

## 1. Problem Statement

The current TUI treats project and node selections inconsistently in tree focus. Projects render info in the right pane, while running nodes render a terminal surface. That makes the split view feel mode-dependent on the selected entry rather than on an explicit operator choice. The existing focus toggle already has a clear contract: `Alt-\`` or `F6` switches between tree view and fullscreen terminal view. The remaining gap is split-view consistency.

The desired interaction is:

- tree focus uses a sticky right-pane mode
- the default right-pane mode is terminal for both projects and nodes
- `i` toggles the right pane between terminal and info while the tree is focused
- the focus keybind promotes the currently selected terminal into fullscreen focus instead of also deciding which content the split pane should show

## 2. Goals and Non-Goals

### 2.1 Goals

- Make the default split-pane surface terminal-oriented for both selected projects and selected nodes.
- Keep `Alt-\`` / `F6` as the only tree-focus to fullscreen-terminal transition.
- Add a sticky `i` toggle in tree focus that swaps the right pane between terminal and info.
- Ensure preview and fullscreen reuse the same terminal session for a given selected target.
- Preserve the current transient right-pane override model for dialogs, menus, and selectors.
- Keep the current project info content usable as the info surface behind `i`.

### 2.2 Non-Goals

- Redesign the existing project or node info view beyond title and footer clarity.
- Introduce a third pane, split terminal-and-info mode, or resizable subpanes.
- Persist right-pane mode in config or project metadata.
- Change node lifecycle behavior, node start requirements, or guest-shell semantics.
- Automatically execute bootstrap commands, environment setup commands, or other project actions when opening a project terminal.
- Solve generalized session eviction, multiplexing, or long-term terminal persistence beyond the current TUI process lifetime.

## 3. System Overview

### 3.1 Main Components

- `tuiState`
  - owns selection, tree expansion, fullscreen focus, and the new sticky tree-pane mode
- right-pane renderer
  - chooses among transient overrides, terminal preview, and info view
- terminal session store
  - must manage reusable project and node sessions under one abstraction
- terminal launcher layer
  - launches host-local project shells and existing node shells
- footer and pane chrome
  - must reflect current pane mode and reachable actions truthfully

### 3.2 Abstraction Levels

- Focus remains a top-level TUI state with two values: `tree` and `terminal`.
- Split-pane content becomes a separate tree-focus concern with two values: `terminal` and `info`.
- Right-pane overrides remain higher priority than tree-pane mode while active.

### 3.3 External Dependencies

- PTY-backed embedded terminal backend using Ghostty VT when available, with the existing fallback terminal implementation retained
- host-local shell resolution for project terminals
- project workspace paths from persisted project metadata
- existing node shell entry flow for node terminals

## 4. Core Domain Model

### 4.1 Entities

- `TUIFocus`
  - `tree`
  - `terminal`
- `TreePaneMode`
  - `terminal`
  - `info`
- `SelectedEntry`
  - `project`
  - `node`
  - empty selection
- `RightPaneOverride`
  - none
  - dialog
  - menu
  - selector
- `TerminalTarget`
  - `project:<project-id>`
  - `node:<node-id>`
- `TerminalSessionState`
  - `absent`
  - `starting`
  - `ready`
  - `failed`
  - `closed`

### 4.2 Stable Identifiers and Normalization Rules

- Project terminal sessions must be keyed by `project:<project-id>`.
- Node terminal sessions must continue to be keyed by `node:<node-id>`.
- The same `TerminalTarget` identifier must be used for preview rendering, focus promotion, close handling, and status reporting.
- `TreePaneMode` is session-local UI state only. It must not be written to config, metadata, or the store.

## 5. Workflow and Interaction Contract

### 5.1 Entry and Invocation

- Starting the TUI must set:
  - `focus = tree`
  - `treePaneMode = terminal`
- Existing initial tree-selection rules may remain unchanged.
- When no right-pane override is active, the right pane must render according to `treePaneMode` while tree-focused.

### 5.2 Input Rules

- `Alt-\`` or `F6`
  - in tree focus:
    - must promote the selected target's terminal into fullscreen terminal focus
    - must not change `treePaneMode`
  - in terminal focus:
    - must return to tree focus
    - must preserve `treePaneMode`
- `i`
  - is valid only while `focus = tree` and no right-pane override is active
  - toggles `treePaneMode` between `terminal` and `info`
  - is sticky across tree-selection changes until toggled again or the TUI exits
- Dialog, menu, and selector bindings keep priority while those surfaces are active.

### 5.3 Right-Pane Rendering Rules

- When a right-pane override is active, it must replace both terminal preview and info rendering.
- When `treePaneMode = info`, the right pane must render the info screen for the current tree selection.
- When `treePaneMode = terminal`, the right pane must render the terminal surface for the current tree selection.
- The right-pane top border must render the mode toggle as inline tabs without adding another row:
  - info mode: `[Info] Terminal`
  - terminal mode: `Info [Terminal]`

### 5.4 Project Terminal Contract

- Selecting a project while `treePaneMode = terminal` must make the right pane show that project's local workspace terminal surface.
- If no live project session exists yet for the selected project, the TUI must begin creating one automatically.
- The project terminal must launch a host-local interactive shell with working directory set to the project's `workspace_path`.
- Opening a project terminal must not:
  - start a node
  - run bootstrap commands
  - edit metadata
  - implicitly change environment-config assignments
- The fullscreen focus action from a selected project must focus the same project terminal session already used for preview, not create a second session unless recovery is required after exit or startup failure.

### 5.5 Node Terminal Contract

- Selecting a running node while `treePaneMode = terminal` must continue to show that node's reusable terminal session.
- Selecting a stopped or otherwise non-shell-ready node while `treePaneMode = terminal` must keep the pane in terminal mode and render a terminal-oriented placeholder instead of switching to info automatically.
- The node placeholder should include:
  - node status
  - the existing actionable guidance such as `[s] start`
  - the `i` hint for switching to info
- Fullscreen focus on a node continues to require a runnable node session. Existing validation may remain in place.

### 5.6 Session Reuse and Selection Changes

- Terminal sessions must remain attached to their own project or node target until the TUI exits or the session closes.
- Changing tree selection in terminal pane mode must swap the visible emulator to the newly selected target.
- Changing tree selection in info pane mode must not destroy hidden terminal sessions.
- Toggling `i` must only change rendering mode. It must not close, recreate, blur, or reset the underlying session.
- Returning from fullscreen terminal focus to tree focus must restore the previously active `treePaneMode`.

### 5.7 Footer Contract

- In tree focus with terminal pane mode:
  - the footer must advertise `[i] info`
  - the footer must keep existing tree navigation and action hints
  - if the selected target can be focused fullscreen, the footer must still advertise `Alt-\`` / `F6`
- In tree focus with info pane mode:
  - the footer must advertise `[i] terminal`
  - the footer must keep existing tree navigation and action hints
- In fullscreen terminal focus:
  - the footer remains terminal-focus specific and should not advertise the `i` toggle because the tree is not focused

## 6. Configuration Specification

### 6.1 Source Precedence and Resolution

- No new persisted user configuration is required in this iteration.
- The default info-toggle binding is `i`.
- If the configurable key-binding system lands concurrently, the stable action identifier for this toggle must be `pane.toggle_info`.

### 6.2 Change Semantics

- `treePaneMode` is initialized at TUI startup and exists only for the current process lifetime.
- `treePaneMode` defaults to `info` for both project and node selections.
- Opening or closing transient right-pane overrides must not mutate `treePaneMode`.

### 6.3 Preflight Validation

- Project terminal startup must validate:
  - the selected project has a non-empty workspace path
  - the workspace path exists
  - the workspace path is a directory
- Failing validation must produce a terminal-surface error state for that project target and a status message.

## 7. State Machine and Lifecycle

### 7.1 Internal States

- Top-level focus:
  - `tree`
  - `terminal`
- Tree pane mode:
  - `terminal`
  - `info`
- Right-pane override:
  - none
  - dialog
  - menu
  - selector
- Per-target terminal session:
  - `absent`
  - `starting`
  - `ready`
  - `failed`
  - `closed`

### 7.2 Session Lifecycle

- Project session lifecycle
  - created lazily when a project is selected in terminal pane mode or when fullscreen focus is requested from tree focus
  - becomes `ready` after the host-local shell is attached to a terminal backend
  - becomes `closed` if the shell exits
  - may transition from `failed` or `closed` to a fresh `starting` attempt on explicit reuse
- Node session lifecycle
  - remains governed by the existing node-shell session rules
  - must continue to reuse the same session across preview and fullscreen focus

### 7.3 Transition Triggers

- TUI startup
  - sets `focus = tree`
  - sets `treePaneMode = terminal`
- `i` in tree focus with no override
  - toggles `treePaneMode`
- `Alt-\`` / `F6` in tree focus
  - resolves current terminal target
  - ensures the target session exists or reports a validation failure
  - sets `focus = terminal` on success
- `Alt-\`` / `F6` in terminal focus
  - sets `focus = tree`
- selection change in tree focus
  - updates rendered target according to `treePaneMode`
- opening a dialog, menu, or selector
  - activates override rendering
  - keeps `treePaneMode` unchanged
- closing a dialog, menu, or selector
  - restores rendering for the current `treePaneMode`
- terminal exit for the active fullscreen target
  - returns focus to `tree`
  - keeps `treePaneMode` unchanged

### 7.4 Idempotency and Recovery Rules

- Re-selecting a target with an already-ready session must reuse it without spawning another shell.
- Repeated `i` toggles must never create or destroy sessions.
- Repeated fullscreen focus requests for the same ready session must only change focus state.
- If a project terminal startup attempt fails, the UI must preserve selection and allow retry by:
  - reselecting the project
  - toggling away and back to terminal pane mode
  - pressing the fullscreen focus key again
- If a session closes unexpectedly, the pane must remain on the same selected target and render a recoverable terminal placeholder rather than silently switching to info.

## 8. Integration Contract

### 8.1 Required Operations

- Add a sticky `TreePaneMode` field to TUI state.
- Generalize the terminal session store to support project and node targets.
- Add a host-local project terminal launcher with PTY-backed shell startup in the project workspace.
- Reuse the existing embedded terminal rendering, resize, hyperlink, mouse, and scrollback machinery for project terminals where applicable.

### 8.2 Normalization Rules

- Project terminals and node terminals must expose a common terminal interface to the renderer and focus synchronizer.
- Preferred terminal size must be applied consistently regardless of whether the visible session is project-scoped or node-scoped.
- Fullscreen focus and split-pane preview must route to the same backing session object for a target.

### 8.3 Error Handling Contract

- Project terminal startup failure must surface:
  - a user-visible footer status message
  - a right-pane terminal placeholder containing the failure summary
- Missing or invalid project workspace paths must be reported without crashing the TUI.
- Node validation failures continue to use the existing error path, but the terminal pane should remain selected conceptually.

### 8.4 Boundary Notes

- A project terminal is a host-local shell boundary, not a guest VM boundary.
- Environment-config rendering may remain informational in this iteration. Whether project terminals inject those configs is implementation-defined for now and must not block the pane-mode work.
- Node shells remain guest-backed and continue to follow current runtime prerequisites.

## 9. Logging, Status, and Observability

- Footer status text must report terminal startup failures and unexpected terminal exits.
- Pane labels must make it obvious whether the operator is looking at terminal or info content.
- Existing background-task summaries must continue to append to the footer in tree focus regardless of pane mode.
- Session close events must remain attributable to their project or node target.

## 10. Failure Model and Recovery Strategy

- Invalid project workspace path
  - render project terminal placeholder with failure text
  - preserve selection
  - allow retry after the path is corrected or selection is retried
- Host shell launch or PTY failure
  - render failure placeholder
  - preserve `treePaneMode`
  - do not switch to info automatically
- Unexpected terminal exit
  - if fullscreen focused, return to tree focus
  - otherwise keep tree focus and show the closed-session placeholder
- Right-pane override activation while a terminal is visible
  - hide terminal rendering temporarily
  - preserve the backing session and restore it after override dismissal

## 11. Security and Operational Safety

- Project terminal startup must only create an interactive local shell at the selected workspace path.
- The TUI must not execute project bootstrap commands or any other implicit command sequence when creating a project terminal.
- The project terminal inherits host-local access and must therefore be clearly identified as local, not guest-backed.
- Workspace-path validation must happen before launching the shell to avoid ambiguous failures.

## 12. Reference Algorithms

```text
onTUIStart():
  state.focus = tree
  state.treePaneMode = terminal

onTreeSelectionChanged(entry):
  if overrideActive():
    return
  if state.treePaneMode == info:
    renderInfo(entry)
    return
  renderTerminalSurface(entry)
  ensureSessionForPreview(entry)

onInfoToggle():
  if state.focus != tree or overrideActive():
    return
  state.treePaneMode = opposite(state.treePaneMode)

onFocusToggle():
  if state.focus == terminal:
    state.focus = tree
    return
  target = terminalTargetForSelectedEntry()
  ensureSessionForFocus(target)
  state.focus = terminal

renderTerminalSurface(entry):
  if entry.kind == project:
    showProjectTerminalOrPlaceholder(entry.project)
  else if entry.kind == node:
    showNodeTerminalOrPlaceholder(entry.node)
  else:
    showEmptyTerminalPlaceholder()
```

## 13. Test and Validation Matrix

### 13.1 Automated Tests

- state initialization
  - TUI starts in tree focus
  - `treePaneMode` defaults to `terminal`
- input handling
  - `i` toggles tree pane mode only in tree focus
  - `i` is ignored while dialog, menu, or selector override is active
  - fullscreen focus toggles preserve `treePaneMode`
- project terminal behavior
  - selecting a project in terminal pane mode creates or reuses a project session
  - fullscreen focus on a selected project uses the same project session as preview
  - invalid project workspace path renders a failure placeholder instead of info
- node terminal behavior
  - running nodes reuse the existing preview/fullscreen session path
  - stopped nodes render a terminal placeholder in terminal pane mode
- sticky mode behavior
  - `treePaneMode = info` remains active while moving across multiple tree entries
  - toggling back to terminal restores the already-running session preview
- footer and labels
  - info pane mode shows `[i] terminal`
  - terminal pane mode shows `[i] info`
  - pane titles match project-versus-node and terminal-versus-info mode
- session close behavior
  - closing the active fullscreen session returns focus to tree
  - hidden sessions survive selection changes and override lifecycles

### 13.2 Manual Validation Additions

- extend the existing TUI verification flow to cover:
  - selected project shows its info pane in the right pane by default
  - `Alt-\`` / `F6` still focuses that same project's terminal fullscreen from the info-first split view
  - `i` switches the split pane from info to the existing project terminal preview
  - `i` remains sticky while moving between projects and nodes in either direction
  - returning from fullscreen terminal focus restores the prior split-pane mode
  - project terminal input remains preserved when toggling `i` away and back

## 14. Implementation Checklist

- Add `TreePaneMode` to TUI state with default `info`.
- Add a stable internal action for the info toggle and bind it to `i`.
- Generalize terminal session management to support `project:<project-id>` and `node:<node-id>` targets.
- Implement host-local project terminal startup in the selected workspace path.
- Update right-pane rendering to choose among override, terminal preview, and info using the new state model.
- Add pane labels for project versus node and terminal versus info mode.
- Update footer rendering to advertise `[i] info` or `[i] terminal` in tree focus.
- Add automated tests for pane-mode state, project terminals, placeholders, focus transitions, and footer text.
- Update `QA.md`, `README.md`, `PATTERNS.MD`, and the relevant ADRs when implementation lands.
