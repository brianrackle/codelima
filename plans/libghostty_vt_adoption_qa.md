# libghostty-vt adoption QA — 2026-09-07

Verification ran on Linux arm64 inside the development environment on
2026-09-08 UTC (September 7 Pacific). All 703 original lines of `QA.md` were
read before execution. The original flow numbering is retained below; Flow 10
now documents the new adoption-specific regression procedure.

## Environment and scope

- Branch: `feat/libghostty-vt-adoption`, working implementation based on
  `44a3122`; no existing user daemon or VM was used.
- Isolated root: `tmp/q.hXuYft`; home, Lima home, workspaces, old/new binaries,
  release archives and diagnostic capture were inside it.
- Both QA executable pairs used version `0.0.0-qa-vt` and native identity
  `2e205255ce6d39bbfdf6f314824a31f12c8de3bbf32fac535a3d01e13289626c`.
- Ghostty `82232ecde55405559dec29c5466cb9e39938cb41`, Zig 0.16.0, static
  feature profile and all four reviewed patches were installed.
- `limactl`, QEMU and `cmatrix` were unavailable on `PATH`. `/dev/kvm` existed
  but opening it was denied. No native macOS host, real terminal windows,
  system clipboard or graphical image display was available.
- Two explicit host-only node metadata fixtures were seeded through the real
  Store API solely to exercise host shells without Lima. Fixture seeding is
  not counted as successful VM/node creation or cloning.

## Per-flow results

| Flow | Result | Observed evidence / remaining boundary |
| --- | --- | --- |
| Setup | Pass, isolated | Unique private project root and separate CodeLima/Lima homes; no existing runtime resources touched. |
| 1 — schema/surface | Pass for local surface; runtime doctor checks blocked | Real CLI help showed settings/environment/configuration/node and no project group. Schema 4, seed 7; five presets in required order with exact documented resources/image/profile/environments. Schema-v3 command failed with fresh-home/no-migration guidance; marker SHA-256 unchanged. Doctor repair succeeded for metadata and reported missing Lima/inaccessible KVM honestly. |
| 2 — configurations/frozen values | Partial | Real environment creation/show, small/qa-large resource/environment updates and default delete/rename protections passed. Referenced configuration deletion failed after adding explicit host fixtures. Fixture node file retained 3/5120/24576 after configuration changed to 4/6144/28672. Actual node create failed `DependencyUnavailable` before any VM was created, so VM-backed freeze behavior is not manually qualified. |
| 3 — directory nodes/cloning | Blocked; argument checks pass | Create and clone without slug returned `InvalidArgument`. Multiple real VM instances, copy-mode seeding and clone ancestry require Lima; node list/show runtime observations also report missing Lima. |
| 4 — lifecycle/bootstrap/SDK | Blocked | Real node start reported missing `limactl`. Guest Node/npm/agent ownership, shell login identity, mounted/copy workspace writes and native virtualization require unavailable VM hosts. No substitute fake-runtime pass is claimed. |
| 5 — terminals/handoff/containment | Host-only portion pass after fixing a discovered bug | Session-v1 quarantine produced exactly one backup, recovery warning and version-2 empty session. Two real host PTYs opened in the scoped workspace. Large-output marker and >900 KiB journal survived corrected live update; terminal IDs and both shell PIDs survived. A stopped renderer was killed/reaped and replaced while its shell, daemon and second terminal stayed usable. Guest-shell and native macOS handoff portions remain blocked. |
| 5b — two-window seats | Blocked | Requires two physical terminal windows/focus reporting; no visual/input-seat result inferred from transport unit tests. |
| 6 — forwarding | Blocked | No running Lima guests; generic/node-qualified/IPv6/1455 routing and claimant transfer were not manually exercised. |
| 7 — interactive TUI | Blocked, with daemon-idle subset checked | No real terminal windows or guests: visual navigation, paste/editing, focus, cmatrix, live reconnect, tab order and guest usage displays remain unqualified. Over a 39-second no-input interval the two idle renderer processes used 0 CPU ticks; daemon used 42 ticks at 100 Hz (~1.1% of one core), not a pinned core. This is not a substitute for two-TUI idle testing. |
| 8 — VirtioFS | Linux branch pass | Snapshot reported `enabled:true, supported:false`. Operator comment and `future_qa_key:7` survived configuration refresh through live update. Native macOS reclaim cadence and guest visibility remain blocked. |
| 9 — diagnostics | Pass for Linux/read-only branch | Capture at `2026-09-08T00:47:38Z` returned status/list/read exit 0, summary, metadata, bounded logs and available `/proc` status/limits. Daemon PID, terminal IDs and shell PIDs were unchanged by capture. `/proc/*/stack` was permission denied; no native stack diagnosis or macOS sampling pass is claimed. |
| 10 — adoption regression | Automated subset pass; physical-host checks blocked | Installer/profile/boundary tests, strict C bridge, ABI schema, focused adapter checks, full retained Vaxis fork suite and encoded-upload race tests passed during implementation. Real static package smoke passed with empty PATH and unavailable legacy library paths. Full unfiltered upstream Ghostty Debug unit/build compilation was killed by the environment; a filtered clipboard run passed but is not full qualification. Interactive selection/search/colors/graphics/clipboard-seat checks require real terminals. |

## Bug found and retested

The initial matched QA pair rejected live handoff with:

`adopt renderer recovery: renderer recovery bundle identity or version mismatch`

The adopter passed the presentation `TabID` (`target#terminalID`) instead of
the native recovery envelope's `TerminalID`. A focused regression failed
before the fix. After correcting adoption and rebuilding a separate candidate,
the original daemon successfully handed off to the new candidate without
restarting either shell:

- Daemon PID: `2316435` → `2326162`; `live_handoff:true`, two terminals.
- Shell PIDs: `2316475` and `2316511`, unchanged.
- Retained journal: `1,047,586` bytes; `large-handoff-ready` remained visible;
  `partial_recovery:false` and both renderers ready.
- Renderer containment: stopped PID `2326171` → replacement `2329318`,
  generation 1 → 2, restart count 1; shell `2316475` unchanged. The other
  renderer stayed PID `2326178`, generation 1. `renderer-recovered` appeared,
  daemon status remained responsive, and the stopped process no longer existed.
- The complete containment probe took 8.5 seconds; the old Flow 5 five-second
  polling example is tighter than this observation and needs qualification
  against the configured renderer deadlines.

The diagnostic skill was used only for read-only capture. Its control/list/read
results showed a healthy rolled-back daemon, not a native terminal freeze; the
narrow failed boundary was recovery identity validation.

## Cleanup and qualification

Both shells exited normally after disabling their history-file writes; the
application removed its two temporary INPUTRC files, and terminal list became
empty. The isolated daemon was stopped. All recorded QA daemon, shell and
renderer PIDs were gone before the unique root was removed. The entire QA
root (homes, seeded fixture metadata, binaries, release archives and diagnostic
capture), plus the temporary fixture helpers, was removed. No Lima instance or
Lima home was created. No verification artifacts or process state remain;
the summarized evidence in this report is retained, not the raw capture.

## Build-bootstrap follow-up: missing macOS pkg-config

The user's macOS arm64 build subsequently compiled the native archive and main
executable but failed cgo worker compilation because `pkg-config` was not on
PATH. This was a setup omission, not a failed Ghostty compilation. ADR 133 adds
checksum-pinned pkgconf 2.5.1, built with managed Zig and selected explicitly by
Make, native checks and direct packaging. Matching Ghostty artifacts remain
unchanged and reusable.

Follow-up Linux verification passed: `make verify`, `make test-race`,
`make test-integration`, `make test-pkgconf`, native bridge, compression
benchmark and package smoke.
The package command was deliberately given an unavailable inherited
`PKG_CONFIG`. Installer regressions cover an explicit pkg-config-free PATH,
real source hashing/extraction, corrupt cache, failed replacement retaining the
old tool, concurrent installs, offline reuse and compiler paths with spaces.
A separate real upstream build with space-containing tools/Zig paths also
produced a working 2.5.1 executable; its disposable tree was removed afterwards.

This build-only follow-up did not repeat the unrelated VM/TUI manual flows;
their per-flow results above and unavailable native gates remain unchanged.
The original Mac's `make build` rerun remains explicitly pending in TODO #41.

Unavailable native/interactive checks remain release blockers, not completed
work. Parent verification owns the final whole-tree lint/test/race/integration
results; those results are not inferred from the focused checks above.
