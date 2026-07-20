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
| Phase 0 spike E1 — embeddability go/no-go | passed (current automated environment) | `spike-notes/MSB_SPIKE.md`; real-init exec/SSH matrix green; native release qualification and human agent observation remain |
| Phase 0 E2–E10 | passed (current automated environment) | all local hard gates green; release-platform coverage remains in TODO |
| Phases 1–5 | done | sole `msb` client, v2 schema break, image/bootstrap/ports/net UX, official validated Codex installer (ADR 69), packaging/docs; guessed ports subsequently replaced by dynamic `{node}.localhost` HTTP/WebSocket forwarding (ADR 70) |

## Track 3 — Daemon

| Item | Status | Notes |
|---|---|---|
| 3.0 Embed version + `--version` | done | build/package ldflags + CLI test |
| 3.1 Process model & sockets | done | ADR 64; lifecycle/stale recovery integration tests; kernel peer-UID enforcement |
| 3.2 Protocol (JSON-lines, exact-match version) | done | ADR 65; 1 MiB cap, deadlines, typed errors, private peer boundary |
| 3.3 Minimum API surface + CLI verbs | done | daemon configuration/node/terminal API; terminal open/close/list/read/send/takeover |
| 3.4 Client-side rendering (dirty → snapshot pull) | done | daemon Ghostty actors + remote snapshot terminal; edge-triggered/idempotent geometry (ADR 68) prevents redraw feedback |
| 3.5 Reconnect + input ownership | done | observe-only clients + explicit takeover; detach integration test |
| 3.6 session.json persistence | done | ADR 66; respawn/forget |

## Track 4 — Live update

Done. Authenticated length-prefixed Unix-stream manifest + batched SCM_RIGHTS, commit and rollback (ADRs 67 and 85); lock-authoritative bounded shutdown/startup recovery (ADR 86); bounded nonblocking PTY polling; continuity, injected-failure rollback, descriptor-transfer, delayed legacy-Darwin restart-fallback, and shutdown-lock recovery integration tests.

## Dynamic node service forwarding

Done locally. ADR 70 replaces guessed static defaults with daemon-owned discovery and HTTP/WebSocket routing at `{node}.localhost:{port}` over multiplexed Microsandbox SSH. Two same-port nodes, guest-loopback binding, Upgrade passthrough, daemon restart recovery, stopped-node removal, and bind-conflict retry passed in the available nested Linux/aarch64 environment. Native release qualification remains in `TODO.md`.

## Microsandbox Go SDK migration

Done locally. ADR 71 replaces every CodeLima `msb` CLI invocation with the official Go SDK pinned to `v0.6.6`. Typed SDK calls now own lifecycle, listing, copy, streaming exec, interactive attach, snapshots, mounts, ports, and network policy. Dynamic forwarding launches the current CodeLima binary as a hidden SDK SSH helper; there is no CLI fallback. Exact legacy built-in CLI templates migrate away, customized lifecycle templates fail explicitly, and the release matrix is limited to SDK-supported `darwin/arm64`, `linux/amd64`, and `linux/arm64`. Native release qualification remains in `TODO.md`.

## Schema v3 object model

Done locally. ADR 72 removes the public project model and introduces reusable global configurations plus directory-bound nodes. The editable protected default configuration seeds typed SDK resources; node creation and cloning freeze effective configuration values. CLI/TUI/daemon surfaces are configuration/node based, path-scoped TUI rows are flat, and schema-v2 homes require a fresh `CODELIMA_HOME`.

## Track 5 — Agent awareness

| Item | Status | Notes |
|---|---|---|
| 5.1 Detection engine | pending | prototypable now against Track 2 cmdRead/cmdSnapshot |
| 5.2 Surfacing (badges, rollup, notifications) | pending | needs Track 3 |
| 5.3 wait primitives + agent SKILL.md | pending | needs Track 3 |
| 5.4 Guest-side monitor | pending | last |

## Track 6 — Experience

| Item | Status | Notes |
|---|---|---|
| 6.1 Keybindings/prefix | pending | |
| 6.2 Roadmap 0.15 + 0.4 | partially complete | priority 5 / former 0.15 done in ADR 87; resize/focus clearing priority 1 / former 0.4 remains |
| 6.3 Goto picker | pending | |
| 6.4 Theme fidelity | pending | TODO #1 |
| 6.5 Kitty graphics | pending | |
| 6.6 Split panes | pending | last |
| 6.7 First-run polish | pending | |

## Track 7 — Engineering system

| Item | Status | Notes |
|---|---|---|
| 7.1 Characterization-test policy in AGENTS.md | pending | practiced in Tracks 1–2; policy text not yet codified |
| 7.2 Integration test tier + CODELIMA_REQUIRE_GHOSTTY | partially complete | `make test-integration` covers lifecycle, stale recovery, detach, ownership and live handoff; fail-on-skip environment policy remains |
| 7.3 TUI visual verification harness | pending | |
| 7.4 Self-update | pending | needs 3.0 |
| 7.5 scripts/patches/PATCHES.md | pending | small, anytime |
| 7.6 Docs versioning | pending | when docs outgrow README |
