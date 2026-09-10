# CodeLima 0.3.2 resize and input verification

The maintainer requested merging and releasing these fixes after the local
results and remaining release qualification gaps were reported.

ADRs 140–142 cover outer resize ordering, printable input without a physical
key identity, and preserving nonblocking PTY mode during resize. Daemon wire
protocols and native library dependencies are unchanged.

## Local evidence

Regression tests reproduced the defects before the fixes and pass afterward.
`make verify`, `make test-race`, `make test-integration` and `make test-package`
pass on Linux arm64. Focused native encoder/resize tests and gopls checks pass.
The integration test resizes a real PTY, waits for an emitted completion
marker after filling the renderer journal, and checks shell identity, geometry
and output after handoff. Printable tests cover decoded ASCII, accented text,
CJK, emoji, modified input and Kitty press/repeat/release reporting.

The rebuilt TUI ran with real host shells and Ghostty workers in an isolated
tmux server. A stopped-node metadata/list fixture enabled host tabs; no VM was
created. Outer sizes 140x40, 80x20, 120x30 and 100x30 produced matching shell
sizes 140x36, 80x16, 120x26 and 100x26. Minimum-size recovery, menu/messages
overlays and search resizing passed. Direct TUI typing preserved
`>`, `%`, `$`, accented letters, CJK and emoji.

After repeated resizing and 1.1 MB of output, an idle-shell daemon update
completed in 0.17 s with the same shell and retained output. The TUI reconnected
and accepted input. A second explicit-path update preserved 110x24 geometry.
Stopping one disposable renderer caused automatic replacement and reaping;
its shell survived and a second terminal remained usable. Read-only diagnostic
probes all passed without changing the daemon PID, terminals or input owner.

Verification processes, shell inputrc files, homes, fixtures, packages and
captures were removed. TODO #46 records the per-flow manual scope.

## Remaining qualification

Native macOS physical-key and visual checks, real Lima VM/forwarding flows,
multi-window interaction, actual Homebrew installation/upgrade, and the full
upstream Ghostty Debug suite remain open in TODO #41/#44/#46. This Linux guest
lacks `limactl` and accessible `/dev/kvm`. Authorization to publish does not
complete these checks.

The nonblocking fix applies to the new daemon. An old daemon already blocked
in handoff cannot be repaired by changing the candidate executable; stop/start
recovery closes its terminals. The release notes disclose this upgrade limit.

## Publication

The first macOS release attempt failed under the race detector in the
unchanged VirtioFS cancellation test: its callback closed a notification
channel twice when cancellation and another tick were both ready. The
independent macOS CI race job and 500 local Linux arm64 race-enabled
repetitions passed on the same commit. TODO #49 records the investigation and
proposed deterministic follow-up. The failed macOS job passed on retry without
changing the tag or bypassing checks; successful Linux jobs were retained.

- The fixes were fast-forward merged into `main` as
  `da3d4985893112f8f91a23e18cd16d6d08e79892`. Annotated tag `v0.3.2` resolves
  to that same commit.
- [Main CI](https://github.com/brianrackle/codelima/actions/runs/34535427107)
  passed Linux/macOS verification and race tests, plus daemon integration.
- [Release workflow, attempt 2](https://github.com/brianrackle/codelima/actions/runs/34535427384/attempts/2)
  passed all jobs. `make verify test-race test-integration test-package`
  passed on macOS arm64 and Linux amd64/arm64 before publication.
- [v0.3.2](https://github.com/brianrackle/codelima/releases/tag/v0.3.2) is a
  regular release and GitHub Latest, with all three archives and manifests.
  Its published notes contain the reviewed fixes and qualification limits.
- Tap commit `01523879dc748b797c5b064f25ec689ccb916c0a` updates the standard
  `Formula/codelima.rb` to 0.3.2. It exactly matches the formula generated from
  the downloaded manifests; the retired beta formula remains absent.
- Every public asset's size and SHA-256 match GitHub's metadata. Archive
  checksums match the manifests and formula. Each archive contains exactly
  the executable CLI and renderer worker; renderer fingerprints match v0.3.1
  on all targets.
- `make test-package-artifact` passed against the downloaded Linux arm64
  package, including version/provenance and real renderer output/read with an
  empty runtime PATH. The published executable pair was not rebuilt for this
  check.
- Verification downloads, scratch tap checkout and test artifacts were removed.
  Normal ignored development builds and toolchain caches remain.

| Target | Archive bytes | Archive SHA-256 |
| --- | ---: | --- |
| darwin/arm64 | 12880648 | `40ea0d1a6bbade26ff9aa6086c62103a91428c12d07cce551ead40e56aee6b50` |
| linux/amd64 | 13210223 | `b2595c74d53204688dbf95e7ac8de0f603cef8066eafcb1e2054aca98e787b94` |
| linux/arm64 | 12403046 | `733a2042ccb025766b5535db416a91125c93fb61a5b83890dc91d63de7eb9e84` |
