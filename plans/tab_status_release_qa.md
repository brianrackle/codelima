# CodeLima 0.3.3 tab status verification

The maintainer requested merging and releasing these fixes after local
verification results and remaining native/manual QA gaps were reported.

ADRs 143–144 cover program-reported tab titles, background metadata delivery,
bell acknowledgement and protection against stale snapshot replies. Daemon
protocol 7 adds metadata to dirty events. Native renderer dependencies,
session persistence and handoff formats are unchanged.

## Local evidence

`make verify`, `make test-race`, `make test-integration` and `make test-package`
pass on Linux arm64. Focused race checks for metadata delivery, bell visits,
publisher state and reconnect races pass. Gopls reports no new diagnostics.

Regression tests reproduce the earlier duplicate labels and permanent bell
badge before the fixes. Tests cover active/background/unfocused-window title,
spinner, progress and completion updates; alert acknowledgement; repeated
bells; hidden-grid request avoidance; delayed events/snapshots; and an old
daemon reply arriving after an authoritative synchronization.

The built TUI was verified with real host shells and Ghostty workers under an
isolated tmux server. A stopped-node metadata/list fixture enabled host tabs;
no VM was created. Both tab titles updated without the redundant prefix.
Background title/spinner changes and 10%/60% progress matched active-tab
behavior. Working state remained until the program reported completion.
`Title 🔔` appeared in the background, cleared on visit, stayed cleared after
leaving, and returned for a later bell. Active-tab bells were acknowledged
immediately. Clearing a title restored its fallback. Live daemon update and
reconnect preserved acknowledgement and both terminal IDs.

Disposable TUI, tmux, daemon, shell/renderer processes, fixtures and captures
were removed. TODO #50 records the per-flow manual scope.

## Previous-release upgrade

The published `v0.3.2` Linux arm64 archive was downloaded and checked against
its manifest SHA-256, then used to start an isolated protocol-6 daemon with
two real host terminals. One terminal emitted 1.1 MB of output; its journal
held 1,045,752 bytes. The versioned `0.3.3` candidate's no-argument
`daemon update` completed in 0.179 seconds and switched to protocol 7.
Both terminal IDs and shell PIDs survived, the final emitted `upgrade-ready`
marker remained readable, and new input worked in the other terminal. A
second explicit-candidate-path live update also succeeded. Disposable
terminals and the daemon were stopped afterward.

## Remaining qualification

Native macOS physical-terminal checks, real Lima VM/forwarding flows,
multi-window interaction, actual Homebrew installation/upgrade and the full
upstream Ghostty Debug suite remain open in TODO #41/#44/#50. This Linux guest
lacks `limactl` and accessible `/dev/kvm`. Authorization to publish does not
complete those checks. The three-platform automated release matrix remains
mandatory before publication.

## Publication

- The changes were fast-forward merged into `main` as
  `b862207942c8aeb5e29915f1a05d5dc2061db95b`. Annotated tag `v0.3.3` resolves
  to the same commit.
- [Main CI](https://github.com/brianrackle/codelima/actions/runs/34560934341)
  passed Linux/macOS verification and race tests, plus daemon integration.
- [Release workflow](https://github.com/brianrackle/codelima/actions/runs/34560934605)
  passed all jobs on the first attempt. Verification, race, integration and
  package checks passed on macOS arm64 and Linux amd64/arm64 before publication.
- [v0.3.3](https://github.com/brianrackle/codelima/releases/tag/v0.3.3) is a
  regular release and GitHub Latest, with all three archives and manifests.
  Its notes include the upgrade instructions and remaining QA limits.
- Homebrew tap commit `43cbe51f71a3602f4d9f7b2f76d87f03423665cb` updates
  `Formula/codelima.rb` to 0.3.3. It exactly matches the formula generated
  from the downloaded manifests; the retired beta formula remains absent.
- Every downloaded asset's size and SHA-256 match GitHub's metadata. Archive
  checksums also match the manifests and formula. Each archive contains the
  two executable files, and renderer fingerprints match v0.3.2 on all targets.
- `make test-package-artifact` passed against the downloaded Linux arm64
  package, including version/provenance and real renderer output/read with an
  empty runtime PATH. The published executable pair was not rebuilt for this
  check.
- Disposable previous-release/candidate binaries, upgrade homes, release
  downloads, scratch tap checkout and test artifacts were removed. Normal
  ignored development builds and toolchain caches remain.

| Target | Archive bytes | Archive SHA-256 |
| --- | ---: | --- |
| darwin/arm64 | 12882900 | `0165f4d901546ed2ce1811f39b7fd9faef01fcf992ec26122cd3a0aeb9b6d96b` |
| linux/amd64 | 13204678 | `4f31fb499cff66acc071e6189ac2f1f1effb32e4bd93ab045c2ecccf22317d5f` |
| linux/arm64 | 12410212 | `68056302307bbd9288afbefa91df4d7d1a05715b31f7db3e299d845633ea24dd` |
