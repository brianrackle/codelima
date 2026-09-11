# CodeLima 0.3.4 compact tab verification

The maintainer requested merging and releasing the compact tab defaults after
local verification and remaining native/manual QA limits were reported.

ADR 145 covers `shell` and `host` defaults without numbering, recognition of
common shell titles, preservation of application titles and independent status
badges. The daemon protocol, storage and native renderer dependency are unchanged.

## Local evidence

`make verify`, `make test-race`, `make test-integration`, `make test-package`
and `make test-release` pass on Linux arm64. Focused race
checks cover labels, metadata, bell acknowledgement and tab scoping. Gopls
reports no diagnostics in the new classifier or its tests. Regression tests
reproduced the previous long/numbered labels before the implementation.

The built TUI was exercised under isolated tmux with two real host shells and
a stopped-node metadata/list fixture. Both tabs showed `host`. Application
titles and spinners appeared; username/path, home-relative, shell executable
and empty titles restored `host`. Background 60% progress and a bell remained
visible; visiting cleared the bell. Reordering preserved active identity,
closing left one unnumbered usable tab, and live daemon update preserved the
terminal ID, shell PID and new input. Reopening the final frontend retained
the default. Automated tests cover guest names and long-title classification.

Disposable shells, TUI, tmux server, daemon, fixtures, diagnostics and scratch
files were removed. TODO #51 records results and limits for every QA.md flow;
TODO #52 records an existing temporary INPUTRC cleanup issue observed during QA.

## Previous-release upgrade

The published `v0.3.3` Linux arm64 archive was checked against its manifest
SHA-256 and used to start an isolated daemon with two real host terminals.
One shell emitted 1.1 MB of output; its journal reached 1,044,738 bytes before
handoff. The versioned `0.3.4` candidate's no-argument `daemon update` and a
second explicit-candidate-path update both reported live handoff. Both
terminal IDs and shell PIDs survived, retained output remained readable,
and fresh input and a command running during the explicit update completed.
Anchored markers in the decoded terminal text verified actual command output.

## Remaining qualification

Native macOS physical-terminal checks, real Lima VM/forwarding flows, multi-window
interaction, actual Homebrew installation/upgrade and the full upstream Ghostty
Debug suite remain open in TODO #41/#44/#51. This Linux guest lacks `limactl`
and accessible `/dev/kvm`. Release authorization does not complete those checks.
The three-platform automated release matrix remains mandatory before publication.

## CI qualification

[Main CI](https://github.com/brianrackle/codelima/actions/runs/34564389208)
passed for `50de45eeabe380e419e8c4aa982f59bb6f275924`. Its first macOS
verification job reproduced the existing VirtioFS cancellation test panic
(TODO #49); the unchanged job passed on its one retry. Both race jobs,
Linux verification and integration passed on their first attempts.

The initial macOS release job passed ordinary verification but failed under
`-race` when `TestTUITabOpenCloseKeyLifecycle` decoded a release sequence as
bare Escape (TODO #53). Both Linux release jobs passed on their first attempts.
The complete macOS release job passed on its one retry without changing the
tag or skipping any checks. The
[release workflow](https://github.com/brianrackle/codelima/actions/runs/34564404178)
completed verification, race, integration and package checks on all three
platforms before publishing the archives.

## Publication

- The change was committed directly on `main` as
  `50de45eeabe380e419e8c4aa982f59bb6f275924`. Annotated tag `v0.3.4` resolves
  to the same commit.
- [v0.3.4](https://github.com/brianrackle/codelima/releases/tag/v0.3.4) is a
  regular release and GitHub Latest, with all three archives and manifests.
- Homebrew tap commit `e3fad8ba090fc1e07c16115efcfc790c7c922f4b` updates
  `Formula/codelima.rb` to 0.3.4. The formula exactly matches the generated
  formula from the downloaded manifests; the retired beta formula is absent.
- Every downloaded asset's size and SHA-256 matches GitHub metadata. Archive
  checksums match their manifests and the formula. Each archive contains both
  executables; all three renderer fingerprints match v0.3.3.
- `make test-package-artifact` passed against the downloaded Linux arm64
  archive, including version/provenance and actual renderer output/read with
  an empty runtime PATH. The published executable pair was not rebuilt for
  this check.
- Disposable candidate/previous-release binaries, upgrade homes, downloads,
  tap checkout, scripts and verification INPUTRC leftovers were removed.
  Normal ignored development binaries and toolchain caches remain.

| Target | Archive bytes | Archive SHA-256 |
| --- | ---: | --- |
| darwin/arm64 | 12885264 | `d653eb844a11685b780338df41d7e0145f65059b395b4ae91480796c8d9bbe8f` |
| linux/amd64 | 13209122 | `7aa25b152fb92798e37de11fdd855a912bb2ea86a4cfffb202e9aebcf11db21a` |
| linux/arm64 | 12412921 | `980a2b4a0dcb4511f7bd11017f333275779d8b0ed972b85a009c33ae90a5d53a` |
