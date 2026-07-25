# Roadmap

Status: Draft

This roadmap tracks the current prioritized plan documents for CodeLima. Every linked plan below is a draft. Completed, superseded, and cancelled items have been removed; their history lives in git and the ADRs under `decisions/`. Each renumbered item keeps a `(was N)` breadcrumb because IMPROVEMENT_PLAN.md and PROGRESS.md reference the old numbers.

## Priorities

1 [complete locally]: eliminate per-tab idle snapshot polling and stop permanently closed daemon event streams after one reported error; active snapshots are dirty-event driven and hidden tabs defer full-grid pulls until visible (ADR 90; native Activity Monitor verification remains in TODO #0)
2 (was 0.4) [complete locally]: terminal width growth uses a supplemental `SIGWINCH` redraw instead of injected `Ctrl-L`, preserving prompt rendering without `^L` text or history clearing; disconnected clients retain their daemon-owned tabs (ADRs 91 and 95; native interactive verification remains in TODO #0)
3 (was 0.10): support kitty graphics protocol so I can get those sweet codex pets.
4 (was 0.11) [partially complete]: bring in and wire up the latest libghostty improvements as demonstrated in https://github.com/ghostty-org/ghostling — Ghostling-pinned libghostty-vt and focus encoder are wired; full interactive QA remains in TODO.md
5 (was 0.12): support CodeLima node renaming through the command line, including an explicit Lima clone/rename policy
6 (was 0.15) [complete locally]: terminal is the default view whenever the selected VM is running and its first guest tab is ensured; stopped nodes remain info-first, while `i` remains explicit for the current node (ADRs 87 and 106; native interactive QA remains in TODO #0)
7 (was 0.22) [partially complete]: open daemon terminal sessions and surviving terminal IDs reconnect after the TUI reopens, exited sessions are absent, per-node operator-defined order is preserved across TUI restart, daemon persistence, and live update, and disjoint path-scoped TUI processes no longer close one another's restored tabs (ADRs 88, 89, 91, and 104); restoring the previously active tab remains in TODO #34
8 (was 1): Configurable key bindings
   Plan: [KEY_BINDINGS_PLAN.md](/Users/brianrackle/personal/codelima/KEY_BINDINGS_PLAN.md)
9 (was 3): Unmanaged localhost or SSH nodes [needs a new node-scoped design]
10 (was 4): Configuration overall for worktree support
   Plan: [WORKTREE_SUPPORT_PLAN.md](/Users/brianrackle/personal/codelima/WORKTREE_SUPPORT_PLAN.md)
11 (was 6): Configuration-level remote node support [needs a schema-v4 design]

## Improvement Plan Tracks

The active engineering effort. Plan: [plans/IMPROVEMENT_PLAN.md](plans/IMPROVEMENT_PLAN.md); per-item status with commits and ADRs: [plans/PROGRESS.md](plans/PROGRESS.md); current backend plan: [LIMA_PLAN.md](LIMA_PLAN.md). Track numbers are stable identifiers into IMPROVEMENT_PLAN.md and are not renumbered; completed tracks (0–4) are recorded in PROGRESS.md.

- Backend return [partially complete]: Lima 2.x is the sole runtime; schema-v4 retains `image` and `sandbox_name`, renders private instance templates, automatically enables nested virtualization on capable macOS arm64 hosts, uses daemon-cached watch observations, preserves generic/node-qualified HTTP/WebSocket forwarding through persistent Go SSH, and keeps interactive SSH in the terminal foreground process group (ADRs 92–93 and 101). Automated and Linux/aarch64 QEMU/KVM lifecycle/clone/forwarding checks pass; macOS VZ and the full native release matrix remain in TODO.
- Track 5: agent awareness — screen-snapshot detection engine, tree badges, wait primitives, agent SKILL.md (5.1 prototypable against Track 2's read seam)
- Track 6: experience — live node CPU, memory, and guest root-disk usage refreshed once per second and terminal-default node view are complete locally (ADRs 110–111 and priority 6); keybindings/prefix mode, goto picker, theme fidelity, kitty graphics (priority 3), split panes, and first-run polish remain
- Track 7 [in progress via practice]: engineering system — characterization-test policy, integration test tier, visual verification harness, self-update, vendor patch discipline, docs versioning
- Terminal stability [complete locally]: resumable TUI connections, authoritative epoch/sequence synchronization, idle-safe multiplexed request delivery, bounded per-client daemon pumps, application heartbeats, visual-invalidation-driven immutable snapshots, chunked renderer replay handoff, ordered sustained-output backpressure, nonblocking single-shot tab close, and one Ghostty renderer process per terminal preserve shells while containing a real non-returning cgo call (ADRs 107–108 and 112–115; native macOS sleep/resume and interactive qualification remain in TODO).

## Related Draft Plans

- Codebase cleanup: [plans/CLEANUP_PLAN.md](plans/CLEANUP_PLAN.md)
- Agent monitoring: [AGENT_MONITORING_PLAN.md](/Users/brianrackle/personal/codelima/AGENT_MONITORING_PLAN.md)
- tmux sidebar frontend: [TMUX_PLAN.md](/Users/brianrackle/personal/codelima/TMUX_PLAN.md)
