# Roadmap

Status: Draft

This roadmap tracks the current prioritized plan documents for CodeLima. Every linked plan below is a draft. Completed, superseded, and cancelled items have been removed; their history lives in git and the ADRs under `decisions/`. Each renumbered item keeps a `(was N)` breadcrumb because IMPROVEMENT_PLAN.md and PROGRESS.md reference the old numbers.

## Priorities

1: Fix bug where host CPU is pinned at 100% per CodeLima process.
2 (was 0.4): Fix issue: resizing window often causes terminal contents to clear. option + ` with codex or raw terminal causes whole terminal to clear so the previous content is no longer visible and only a fresh prompt is shown.
3 (was 0.10): support kitty graphics protocol so I can get those sweet codex pets.
4 (was 0.11) [partially complete]: bring in and wire up the latest libghostty improvements as demonstrated in https://github.com/ghostty-org/ghostling — Ghostling-pinned libghostty-vt and focus encoder are wired; full interactive QA remains in TODO.md
5 (was 0.12): support CodeLima node renaming through the command line, including an explicit microsandbox rename/recreate policy
6 (was 0.15) [complete locally]: terminal is the default node view when the initially selected VM is running; stopped nodes remain info-first, and `i` remains sticky after startup (ADR 87; native interactive QA remains in TODO #0)
7 (was 0.22): retain open terminal tabs and their live daemon terminal sessions when the user quits and reopens the TUI; reconnect surviving terminal IDs, restore per-node tab order and the active tab, and prune sessions that exited while the TUI was detached instead of creating replacement shells
8 (was 1): Configurable key bindings
   Plan: [KEY_BINDINGS_PLAN.md](/Users/brianrackle/personal/codelima/KEY_BINDINGS_PLAN.md)
9 (was 3): Unmanaged localhost or SSH nodes [needs a new node-scoped design]
10 (was 4): Configuration overall for worktree support
   Plan: [WORKTREE_SUPPORT_PLAN.md](/Users/brianrackle/personal/codelima/WORKTREE_SUPPORT_PLAN.md)
11 (was 6): Configuration-level remote node support [needs a schema-v3 design]

## Improvement Plan Tracks

The active engineering effort. Plan: [plans/IMPROVEMENT_PLAN.md](plans/IMPROVEMENT_PLAN.md); per-item status with commits and ADRs: [plans/PROGRESS.md](plans/PROGRESS.md); backend swap: [plans/MICROSANDBOX_MIGRATION_PLAN.md](plans/MICROSANDBOX_MIGRATION_PLAN.md). Track numbers are stable identifiers into IMPROVEMENT_PLAN.md and are not renumbered; completed tracks (0–4) are recorded in PROGRESS.md.

- Backend swap [complete locally]: microsandbox 0.6.6 is the sole runtime through the official Go SDK with no CLI fallback; schema-v3 nodes use images, typed VM resources, optional explicit ports and network policy, plus daemon-owned generic `localhost`/`127.0.0.1` and explicit `{node}.localhost` HTTP/WebSocket forwarding through a CodeLima SDK helper (ADRs 55, 70, 71, 72, and 79; native release qualification remains in TODO)
- Track 5: agent awareness — screen-snapshot detection engine, tree badges, wait primitives, agent SKILL.md (5.1 prototypable against Track 2's read seam)
- Track 6: experience — keybindings/prefix mode, terminal-default node view (priority 6 complete locally), goto picker, theme fidelity, kitty graphics (priority 3), split panes, first-run polish
- Track 7 [in progress via practice]: engineering system — characterization-test policy, integration test tier, visual verification harness, self-update, vendor patch discipline, docs versioning

## Related Draft Plans

- Codebase cleanup: [plans/CLEANUP_PLAN.md](plans/CLEANUP_PLAN.md)
- Agent monitoring: [AGENT_MONITORING_PLAN.md](/Users/brianrackle/personal/codelima/AGENT_MONITORING_PLAN.md)
- tmux sidebar frontend: [TMUX_PLAN.md](/Users/brianrackle/personal/codelima/TMUX_PLAN.md)
