# Roadmap

Status: Draft

This roadmap tracks the current prioritized plan documents for CodeLima. Every linked plan below is a draft. Completed, superseded, and cancelled items have been removed; their history lives in git and the ADRs under `decisions/`. Each renumbered item keeps a `(was N)` breadcrumb because IMPROVEMENT_PLAN.md and PROGRESS.md reference the old numbers.

## Priorities

1 [complete locally]: eliminate per-tab idle snapshot polling and stop permanently closed daemon event streams after one reported error; active snapshots are dirty-event driven and hidden tabs defer full-grid pulls until visible (ADR 90; native Activity Monitor verification remains in TODO #0)
2 (was 0.4): Fix issue: resizing window often causes terminal contents to clear. option + ` with codex or raw terminal causes whole terminal to clear so the previous content is no longer visible and only a fresh prompt is shown. Also fix the bug where a disconnected client loses its open terminal tabs.
3 (was 0.10): support kitty graphics protocol so I can get those sweet codex pets.
4 (was 0.11) [partially complete]: bring in and wire up the latest libghostty improvements as demonstrated in https://github.com/ghostty-org/ghostling — Ghostling-pinned libghostty-vt and focus encoder are wired; full interactive QA remains in TODO.md
5 (was 0.12): support CodeLima node renaming through the command line, including an explicit Lima clone/rename policy
6 (was 0.15) [complete locally]: terminal is the default node view when the initially selected VM is running; stopped nodes remain info-first, and `i` remains sticky after startup (ADR 87; native interactive QA remains in TODO #0)
7 (was 0.22) [partially complete]: open daemon terminal sessions and surviving terminal IDs reconnect after the TUI reopens, exited sessions are absent, per-node creation order is preserved across TUI restart, daemon persistence, and live update, and disjoint path-scoped TUI processes no longer close one another's restored tabs (ADRs 88, 89, and 91); restoring the previously active tab remains in TODO #34
8 (was 1): Configurable key bindings
   Plan: [KEY_BINDINGS_PLAN.md](/Users/brianrackle/personal/codelima/KEY_BINDINGS_PLAN.md)
9 (was 3): Unmanaged localhost or SSH nodes [needs a new node-scoped design]
10 (was 4): Configuration overall for worktree support
   Plan: [WORKTREE_SUPPORT_PLAN.md](/Users/brianrackle/personal/codelima/WORKTREE_SUPPORT_PLAN.md)
11 (was 6): Configuration-level remote node support [needs a schema-v4 design]

## Improvement Plan Tracks

The active engineering effort. Plan: [plans/IMPROVEMENT_PLAN.md](plans/IMPROVEMENT_PLAN.md); per-item status with commits and ADRs: [plans/PROGRESS.md](plans/PROGRESS.md); current backend plan: [LIMA_PLAN.md](LIMA_PLAN.md). Track numbers are stable identifiers into IMPROVEMENT_PLAN.md and are not renumbered; completed tracks (0–4) are recorded in PROGRESS.md.

- Backend return [partially complete]: Lima 2.x is the sole runtime; schema-v4 retains `image` and `sandbox_name`, renders private instance templates, uses daemon-cached watch observations, preserves generic/node-qualified HTTP/WebSocket forwarding through persistent Go SSH, and keeps interactive SSH in the terminal foreground process group (ADRs 92–93). Automated and Linux/aarch64 QEMU/KVM lifecycle/clone/forwarding checks pass; macOS VZ and the full native release matrix remain in TODO.
- Track 5: agent awareness — screen-snapshot detection engine, tree badges, wait primitives, agent SKILL.md (5.1 prototypable against Track 2's read seam)
- Track 6: experience — keybindings/prefix mode, terminal-default node view (priority 6 complete locally), goto picker, theme fidelity, kitty graphics (priority 3), split panes, first-run polish
- Track 7 [in progress via practice]: engineering system — characterization-test policy, integration test tier, visual verification harness, self-update, vendor patch discipline, docs versioning

## Related Draft Plans

- Codebase cleanup: [plans/CLEANUP_PLAN.md](plans/CLEANUP_PLAN.md)
- Agent monitoring: [AGENT_MONITORING_PLAN.md](/Users/brianrackle/personal/codelima/AGENT_MONITORING_PLAN.md)
- tmux sidebar frontend: [TMUX_PLAN.md](/Users/brianrackle/personal/codelima/TMUX_PLAN.md)
