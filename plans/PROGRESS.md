# Improvement Plan — Progress Tracker

Status board for `plans/IMPROVEMENT_PLAN.md` and `plans/MICROSANDBOX_MIGRATION_PLAN.md`.
Update this file in the same commit that completes an item. One line per work item;
link the commit and any ADRs. Statuses: `done` / `in progress` / `pending` / `blocked (<on what>)`.

Branch: `track-0-1-stabilization-and-identity` (off `main`).

## Track 0 — Stabilize

| Item | Status | Commit | ADRs / notes |
|---|---|---|---|
| 0.1 Kill process groups, not PIDs | done | 6062728 | ADR 56; TODO #24 (job-control groups) filed |
| 0.2 Handle signals | done | 6062728 | |
| 0.3 Reads must not write | done | 6062728 | ADR 57; TODO #20 resolved |
| 0.4 node delete / cleanup orphan (TODO #10) | done | 6062728 | ADR 58 (teardown-first cleanup); TODO #10 resolved |
| 0.5 Real observability | done | 6062728 | ADR 59 (slog sinks, libghostty capture, messages view) |
| 0.6 Delete dead Patch subsystem | done | 6062728 | ADR 60 |
| 0.7 Correctness backlog (dup stdout, rollback logs, fsync) | done | 6062728 | TODO #6/#8 resolved |
| 0.8 Vaxis reflection canaries | done | 6062728 | TODO #25 (CapturesMouse race) filed |

## Track 1 — Terminal identity + registry

| Item | Status | Commit | ADRs / notes |
|---|---|---|---|
| PR1 TargetKey | done | 6062728 | ADR 61; characterization suite (7 tests) written first |
| PR2 TerminalID + registry | done | 6062728 | TODO #26 (upstream vaxis double-Wait race) filed |
| PR3 Tab bookkeeping → TargetTerminalState | done | 6ef295f | |

## Track 2 — Actors, launch contract, decomposition

| Item | Status | Commit | ADRs / notes |
|---|---|---|---|
| 2.1 Runtime actor (cmdRead/cmdSnapshot seam) | done | 714c479 | ADR 63; busy-spin fixed; TODO #27 (bounded POLLOUT wait) filed |
| 2.2 One shell-launch contract | done | 260a938 | ADR 62 — signature is `TerminalLaunchSpec(target, kind, workspacePath)` (documented divergence); TODO #18 resolved |
| 2.3 Decompose tui_vaxis.go | done | e363412 | 176/176 byte-identical declaration moves; no file > ~1,030 lines |

Track 2 definition-of-done met at e363412: `make verify` + `-race` green, 0 Ghostty skips,
characterization suite unmodified.

## Backend swap (slot 4.5)

| Item | Status | Notes |
|---|---|---|
| Phase 0 spike E1 — embeddability go/no-go | blocked (msb not installed) | ADR 55 accepted; gate before all later phases |
| Phases 1–5 | pending | after E1 passes |

## Track 3 — Daemon

| Item | Status | Notes |
|---|---|---|
| 3.0 Embed version + `--version` | pending | backend-independent; can run before the swap |
| 3.1 Process model & sockets | pending | after swap |
| 3.2 Protocol (JSON-lines, exact-match version) | pending | |
| 3.3 Minimum API surface + CLI verbs | pending | |
| 3.4 Client-side rendering (dirty → snapshot pull) | pending | decision fixed — do not relitigate |
| 3.5 Reconnect + input ownership | pending | |
| 3.6 session.json persistence | pending | |

## Track 4 — Live update

Pending; hard-gated on Track 3 soaked in daily use.

## Track 5 — Agent awareness

| Item | Status | Notes |
|---|---|---|
| 5.1 Detection engine | pending | prototypable now against Track 2 cmdRead/cmdSnapshot |
| 5.2 Surfacing (badges, rollup, notifications) | pending | needs Track 3 |
| 5.3 wait primitives + agent SKILL.md | pending | needs Track 3 |
| 5.4 Guest-side monitor | pending | last |

## Track 6 — Experience

All pending: 6.1 keybindings/prefix, 6.2 roadmap 0.15+0.4, 6.3 goto picker,
6.4 theme fidelity (TODO #1), 6.5 kitty graphics, 6.6 split panes (last), 6.7 first-run polish.

## Track 7 — Engineering system

| Item | Status | Notes |
|---|---|---|
| 7.1 Characterization-test policy in AGENTS.md | pending | practiced in Tracks 1–2; policy text not yet codified |
| 7.2 Integration test tier + CODELIMA_REQUIRE_GHOSTTY | pending | wanted before Track 3 (daemon lifecycle tests live there) |
| 7.3 TUI visual verification harness | pending | |
| 7.4 Self-update | pending | needs 3.0 |
| 7.5 scripts/patches/PATCHES.md | pending | small, anytime |
| 7.6 Docs versioning | pending | when docs outgrow README |
