# Tmux Sidebar Plan

Status: Draft

## Goal

Provide a tmux-native sidebar experience that acts as an alternative frontend to the existing CodeLima TUI while preserving the same operational semantics:

- create projects
- create nodes
- start and stop nodes
- delete and clone nodes
- manage environment-config-backed flows as needed for parity
- guarantee that managed terminals enter the VM through CodeLima

The tmux sidebar should feel like a different shell around the same product, not a second implementation with different behavior.

## Current CodeLima Baseline

- `internal/codelima/service.go` is already the control plane for project and node operations.
- `internal/codelima/tui_vaxis.go` currently owns the full-screen vaxis frontend, session launching, and operation orchestration.
- Managed terminals are created by spawning `codelima shell <node>`, which delegates through `Service.Shell`.

That means the most important invariant already exists: CodeLima knows how to guarantee a VM shell. The tmux integration should reuse that exact path.

## Design Principles

- tmux is a frontend, not the control plane
- `Service` remains authoritative for operations and validation
- managed node terminals must always be launched with `codelima shell <node>`
- sidebar state must reflect the same read surfaces as the full-screen TUI
- UI logic should be shared where possible so the two frontends do not drift

## What To Reuse From opensessions

Useful patterns to reuse:

- a toggleable sidebar pane inside tmux
- pane metadata to track managed sessions
- lightweight live status refresh for the sidebar

Patterns not to copy directly in the first pass:

- a generic mux abstraction for many providers
- a separate local server as a hard prerequisite
- session identity derived primarily from cwd scanning

CodeLima already has a node identity model that is stronger than cwd-based inference.

Relevant references:

- https://github.com/Ataraxy-Labs/opensessions/blob/main/README.md
- https://github.com/Ataraxy-Labs/opensessions/blob/main/docs/explanation/architecture.md

## Recommended Architecture

### 1. A Dedicated Sidebar Command

Add a command dedicated to the narrow-pane experience, for example:

- `codelima sidebar`

This command should render:

- the project tree
- node VM status
- agent activity badges from the agent runtime plan
- a compact details/action area

It should call the same service methods as the full-screen TUI.

### 2. A Small tmux Integration Layer

Add tmux-specific helper commands and scripts, for example:

- `codelima tmux toggle-sidebar`
- `codelima tmux focus-sidebar`
- `codelima tmux open-node <node>`

Responsibilities:

- create or toggle the sidebar pane
- create or focus a managed pane for a node
- tag panes with metadata such as:
  - `@codelima_role=sidebar`
  - `@codelima_node_id=<node-id>`
  - `@codelima_home=<metadata-root>`

These helpers should use plain `tmux` commands while delegating all product operations to the main CodeLima binary.

### 3. Managed Node Panes

Every managed node terminal must launch exactly:

`codelima --home <home> shell <node-id>`

Not:

- `bash`
- `zsh`
- `limactl shell ...`
- any direct VM shell command that bypasses CodeLima

This preserves:

- workspace path selection
- shell initialization behavior
- future monitoring hooks
- a single place to evolve shell semantics

### 4. Shared Controller Layer

The current full-screen TUI mixes frontend rendering with action orchestration. Before the tmux sidebar reaches full parity, extract a shared controller layer that owns:

- tree selection and entry identity
- action availability per project or node
- dialog and selector schemas
- long-running operation lifecycle
- status and notification reduction

Frontends then become:

- full-screen vaxis TUI
- narrow sidebar TUI

Both should render the same controller state and invoke the same service-backed actions.

## Sidebar UX Shape

Recommended narrow-pane layout:

- header with current project or node focus
- scrollable project tree
- compact footer with hotkeys and active notifications

Recommended interactions:

- move selection through projects and nodes
- open action menu for the selected entry
- create project or node without leaving tmux
- open or focus the managed node pane for the selected node
- clear unseen notifications when the node is focused

The node pane itself remains a normal tmux pane running the managed VM shell.

## Session and Focus Model

Recommended mapping:

- one sidebar pane per tmux session
- zero or more managed node panes per tmux session
- panes are matched to nodes through tmux metadata, not cwd guessing

When a user selects a node and requests a shell:

- if a managed pane already exists for that node, focus it
- otherwise create a new managed pane running `codelima shell <node>`

When the sidebar refreshes:

- it should verify that a pane tagged as managed is still running the expected command shape
- if not, it should drop the managed association rather than assuming the pane is still valid

## Do We Need a Separate Local Server?

Not in the first pass.

The initial implementation can stay inside the main Go binary and use:

- direct service calls for operations
- periodic refresh for project tree and runtime badges
- direct `tmux` commands for pane management

A long-lived local server becomes worth considering only if later requirements demand:

- multiple frontends subscribed at once
- push-driven updates instead of refresh
- remote or cross-process coordination that is awkward inside a CLI process

## Implementation Phases

### Phase 1

- add tmux helper commands for toggling the sidebar and opening node panes
- guarantee every managed pane uses `codelima shell <node>`

### Phase 2

- add the narrow `codelima sidebar` frontend with read-only project tree and focus actions

### Phase 3

- extract shared controller logic from the current vaxis app
- move create, start, stop, clone, and delete flows onto shared actions

### Phase 4

- reach feature parity with the full-screen TUI
- integrate agent runtime badges and unseen notification markers

## Risks

- If the sidebar reimplements action flows independently, it will drift from the full-screen TUI.
- If tmux pane identity is inferred from cwd instead of explicit metadata, pane ownership will become ambiguous.
- If node panes bypass `codelima shell`, future shell-related fixes and monitoring hooks will split across multiple paths.

## Recommendation

Build the tmux sidebar as a second frontend over the existing CodeLima service layer, not as a tmux-first fork of product logic. The immediate engineering priority is sharing controller logic and preserving `codelima shell <node>` as the only managed terminal entrypoint.
