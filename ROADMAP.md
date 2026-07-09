# Roadmap

Status: Draft

This roadmap tracks the current prioritized plan documents for CodeLima. Every linked plan below is a draft.

## Priorities
0.0 [complete]: shorten lima generated vm ids so they align with codelima node names, because the long names that show up in the terminal are too long (e.g. brianrackle@lima-happi-happi-node-019e4c5b should instead be brianrackle@happi-node)
0.1 [complete]: keybind to switch to host terminal and back with Option+Shift+Backtick
0.2 [complete]: Refresh project tree automatically.
0.3 [complete]: Fix issue: Pasting content with newlines removes the newlines.
0.4: Fix issue: resizing window often causes terminal contents to clear. option + ` with codex or raw terminal causes whole terminal to clear so the previous content is no longer visible and only a fresh prompt is shown.
0.5 [complete]: red host-machine indicator uses the existing TUI top bar instead of adding another bar
0.6 [complete]: support syncing vm clipboard to host system clipboard
0.7 [superseded]: ghostty cmd + d and cmd + shift + d style TUI split support was removed; terminal tabbing stays tabbing and modified terminal keys no longer create TUI splits
0.8 [complete]: explicit per-node terminal tabs — the TUI starts with one default tab for the initial project or running node, Option+t opens fresh tabs for the focused project or node, Option+Left/Right switch and Option+w close within that item's tabs with adjacent close focus, tabs are scoped to the focused tree item, and selection/visiting never creates additional sessions (F7-F9 and tree `t` fallbacks removed)
0.9 [cancelled] support a `https://github.com/apple/container` backend
0.10 support kitty graphcis protocol so I can get those sweet codex pets.
0.11 [partially complete]: bring in and wire up the latest libghostty improvements as demonstrated in https://github.com/ghostty-org/ghostling — Ghostling-pinned libghostty-vt and focus encoder are wired; full interactive QA remains in TODO.md
0.12 support codelima node renaming through command line (persisting the name to lima as defined in 0.0)
0.13 [complete]: creating a new node automatically makes the terminal pane available for that node without starting a shell session before the node is running
0.14 [complete]: when the TUI is opened by providing a path, the project tree only includes projects in that directory and its subdirectories
0.15 terminal should be the default node view (not info) if the vm is running.
1. Configurable key bindings
   Plan: [KEY_BINDINGS_PLAN.md](/Users/brianrackle/personal/codelima/KEY_BINDINGS_PLAN.md)
2. Sub-project support [cancelled]
   Plan: [SUB_PROJECT_PLAN.md](/Users/brianrackle/personal/codelima/SUB_PROJECT_PLAN.md)
3. Project support for localhost or SSH projects with unmanaged nodes and no workspace management
   Plan: [LOCALHOST_SSH_PROJECTS_PLAN.md](/Users/brianrackle/personal/codelima/LOCALHOST_SSH_PROJECTS_PLAN.md)
4. Configuration overall for worktree support
   Plan: [WORKTREE_SUPPORT_PLAN.md](/Users/brianrackle/personal/codelima/WORKTREE_SUPPORT_PLAN.md)
5. Docker and Firecracker VM or container support [cancelled]
   Plan: [RUNTIME_PROVIDER_PLAN.md](/Users/brianrackle/personal/codelima/RUNTIME_PROVIDER_PLAN.md)
6. Project-level configuration of remote node support
   Plan: [REMOTE_NODE_CONFIGURATION_PLAN.md](/Users/brianrackle/personal/codelima/REMOTE_NODE_CONFIGURATION_PLAN.md)

## Improvement Plan Tracks

The active engineering effort. Plan: [plans/IMPROVEMENT_PLAN.md](plans/IMPROVEMENT_PLAN.md); per-item status with commits and ADRs: [plans/PROGRESS.md](plans/PROGRESS.md); backend swap: [plans/MICROSANDBOX_MIGRATION_PLAN.md](plans/MICROSANDBOX_MIGRATION_PLAN.md).

- Track 0 [complete]: stabilize — group-kill process trees, signal handling, read/write split, cleanup orphan fix, observability, dead patch subsystem deletion, correctness backlog, reflection canaries (ADRs 56–60)
- Track 1 [complete]: terminal identity + runtime registry — opaque TerminalID, TargetKey, tab bookkeeping in `internal/codelima/terminal` (ADR 61)
- Track 2 [complete]: runtime actors + one shell-launch contract + TUI decomposition — cmdRead/cmdSnapshot seam, Service.TerminalLaunchSpec, tui_vaxis.go split by responsibility (ADRs 62–63)
- Backend swap [blocked on msb install]: replace Lima with microsandbox as the sole runtime (ADR 55); Phase 0 embeddability spike E1 is the go/no-go gate
- Track 3: daemon — terminal runtimes survive TUI exit; JSON-lines API, `codelima terminal list/read/send`, session persistence (3.0 version embedding is backend-independent and can run before the swap)
- Track 4: live update — daemon replaces itself via SCM_RIGHTS PTY handoff without killing terminals (after Track 3 soaks)
- Track 5: agent awareness — screen-snapshot detection engine, tree badges, wait primitives, agent SKILL.md (5.1 prototypable against Track 2's read seam)
- Track 6: experience — keybindings/prefix mode, terminal-default node view (0.15), goto picker, theme fidelity, kitty graphics (0.10), split panes, first-run polish
- Track 7 [in progress via practice]: engineering system — characterization-test policy, integration test tier, visual verification harness, self-update, vendor patch discipline, docs versioning

## Related Draft Plans

- Agent monitoring: [AGENT_MONITORING_PLAN.md](/Users/brianrackle/personal/codelima/AGENT_MONITORING_PLAN.md)
- tmux sidebar frontend: [TMUX_PLAN.md](/Users/brianrackle/personal/codelima/TMUX_PLAN.md)
