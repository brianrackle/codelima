# Roadmap

Status: Draft

This roadmap tracks the current prioritized plan documents for CodeLima. Every linked plan below is a draft.

## Priorities
0.0 [complete]: runtime instance identity aligns with the CodeLima node slug; the microsandbox swap carries this forward as persisted `sandbox_name`
0.1 [superseded by 0.19]: keybind to switch to host terminal and back with Option+Shift+Backtick
0.2 [complete]: Refresh the directory-scoped node list automatically.
0.3 [complete]: Fix issue: Pasting content with newlines removes the newlines.
0.4: Fix issue: resizing window often causes terminal contents to clear. option + ` with codex or raw terminal causes whole terminal to clear so the previous content is no longer visible and only a fresh prompt is shown.
0.5 [complete]: red host-machine indicator uses the existing TUI top bar instead of adding another bar
0.6 [complete]: support syncing vm clipboard to host system clipboard
0.7 [superseded]: ghostty cmd + d and cmd + shift + d style TUI split support was removed; terminal tabbing stays tabbing and modified terminal keys no longer create TUI splits
0.8 [complete]: explicit per-node terminal tabs — the TUI starts with one default tab for the initial running node, Option+t opens fresh tabs for the focused node, Option+Left/Right switch and Option+w close within that node's tabs with adjacent close focus, and selection/refresh never creates additional sessions
0.9 [cancelled] support a `https://github.com/apple/container` backend
0.10 support kitty graphcis protocol so I can get those sweet codex pets.
0.11 [partially complete]: bring in and wire up the latest libghostty improvements as demonstrated in https://github.com/ghostty-org/ghostling — Ghostling-pinned libghostty-vt and focus encoder are wired; full interactive QA remains in TODO.md
0.12 support CodeLima node renaming through the command line, including an explicit microsandbox rename/recreate policy
0.13 [complete]: creating a new node automatically makes the terminal pane available for that node without starting a shell session before the node is running
0.14 [complete]: when the TUI is opened with a path, its flat list includes only nodes bound to that directory or a descendant
0.15 terminal should be the default node view (not info) if the vm is running.
0.16 [complete]: schema v3 replaces projects with reusable global configurations and directory-bound nodes; configuration values freeze into new nodes, the reserved default is editable/protected, and guest/host terminals are node-scoped (ADR 72)
0.17 [complete]: macOS daemon pressure monitoring protects live mounted VirtioFS workspaces by reclaiming clean guest dentry/inode caches at 20% host file-table utilization with a 30-second attempt cooldown (ADR 73)
0.18 [complete]: fresh nodes default to live mounted workspaces, and fresh default configurations install both Codex and Claude Code while preserving explicit copy mode and existing-home configuration choices (ADR 76)
0.19 [complete]: Option+Shift+t opens ordinary node-scoped host tabs that share guest-tab switching, closing, refresh, and active-host indication behavior (ADR 77)
0.20 [complete]: every interactive TUI claims daemon input ownership at connection time, so a delayed terminal open cannot fail because the TUI began observe-only (ADR 78)
1. Configurable key bindings
   Plan: [KEY_BINDINGS_PLAN.md](/Users/brianrackle/personal/codelima/KEY_BINDINGS_PLAN.md)
2. Sub-project support [cancelled by schema v3]
3. Unmanaged localhost or SSH nodes [needs a new node-scoped design]
4. Configuration overall for worktree support
   Plan: [WORKTREE_SUPPORT_PLAN.md](/Users/brianrackle/personal/codelima/WORKTREE_SUPPORT_PLAN.md)
5. Docker and Firecracker VM or container support [cancelled]
   Plan: [RUNTIME_PROVIDER_PLAN.md](/Users/brianrackle/personal/codelima/RUNTIME_PROVIDER_PLAN.md)
6. Configuration-level remote node support [needs a schema-v3 design]

## Improvement Plan Tracks

The active engineering effort. Plan: [plans/IMPROVEMENT_PLAN.md](plans/IMPROVEMENT_PLAN.md); per-item status with commits and ADRs: [plans/PROGRESS.md](plans/PROGRESS.md); backend swap: [plans/MICROSANDBOX_MIGRATION_PLAN.md](plans/MICROSANDBOX_MIGRATION_PLAN.md).

- Track 0 [complete]: stabilize — group-kill process trees, signal handling, read/write split, cleanup orphan fix, observability, dead patch subsystem deletion, correctness backlog, reflection canaries (ADRs 56–60)
- Track 1 [complete]: terminal identity + runtime registry — opaque TerminalID, TargetKey, tab bookkeeping in `internal/codelima/terminal` (ADR 61)
- Track 2 [complete]: runtime actors + one shell-launch contract + TUI decomposition — cmdRead/cmdSnapshot seam, Service.TerminalLaunchSpec, tui_vaxis.go split by responsibility (ADRs 62–63)
- Backend swap [complete locally]: microsandbox 0.6.6 is the sole runtime through the official Go SDK with no CLI fallback; schema-v3 nodes use images, typed VM resources, optional explicit ports and network policy, plus daemon-owned `{node}.localhost` HTTP/WebSocket forwarding through a CodeLima SDK helper (ADRs 55, 70, 71, and 72; native release qualification remains in TODO)
- Track 3 [complete]: daemon-owned terminal runtimes survive TUI exit; exact-version JSON-lines API, terminal automation, client-side snapshots, ownership and session persistence, with edge-triggered terminal geometry, persistent authenticated connections, and automatic quarantine/recovery for incompatible terminal-session caches (ADRs 64–66, 68, 74, and 75)
- Track 4 [complete]: authenticated SCM_RIGHTS live update preserves PTYs and rolls back failed imports (ADR 67)
- Track 5: agent awareness — screen-snapshot detection engine, tree badges, wait primitives, agent SKILL.md (5.1 prototypable against Track 2's read seam)
- Track 6: experience — keybindings/prefix mode, terminal-default node view (0.15), goto picker, theme fidelity, kitty graphics (0.10), split panes, first-run polish
- Track 7 [in progress via practice]: engineering system — characterization-test policy, integration test tier, visual verification harness, self-update, vendor patch discipline, docs versioning

## Related Draft Plans

- Agent monitoring: [AGENT_MONITORING_PLAN.md](/Users/brianrackle/personal/codelima/AGENT_MONITORING_PLAN.md)
- tmux sidebar frontend: [TMUX_PLAN.md](/Users/brianrackle/personal/codelima/TMUX_PLAN.md)
