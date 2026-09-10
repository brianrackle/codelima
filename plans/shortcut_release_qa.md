# CodeLima 0.3.1 shortcut verification

The maintainer requested merging the shortcut fixes and publishing v0.3.1
after the local results and remaining native/manual QA were reported.

ADRs 138–139 cover press-only one-shot actions, repeatable navigation, and
consumption of shortcut followups across input-context changes. Release
packaging, renderer dependencies, and daemon protocols are unchanged.

## Local evidence

Permanent tests reproduce the original failures before the fix and pass after
it. Whole-tree `make verify` and `GOFLAGS=-race make test-tui-input` pass on
Linux arm64. The full `make test-race test-integration test-package` release
checks also pass, including the built CLI/daemon integration tier and packaged
CLI/renderer smoke test. gopls reports no errors in the changed Go files.
`make --silent release-metadata RELEASE_TAG=v0.3.1` resolves version `0.3.1`.

The rebuilt TUI ran with real host PTYs and Ghostty workers in an isolated
tmux session. A disposable Lima-list fixture reported one stopped node; no VM
was created. Checks passed for single-tab creation/closure, rapid separate
presses, F7 search, Info, menu/form opening, parent-selector confirmation and
cancellation, selection toggling/clearing, field/selector movement, submit
after releasing a modifier first, paste, and fresh shell input. Shell title
updates changed labels without creating additional terminal IDs. All created
terminals, the test configuration, processes, homes, fixtures and captures
were cleaned up. TODO #45 records the exact manual scope.

## Remaining qualification

Native macOS physical-key and visual checks, real Lima VM/forwarding flows,
multi-window interaction, actual Homebrew installation/upgrade, and the full
upstream Ghostty Debug suite remain open in TODO #41/#43/#44/#45. The current
Linux guest lacks `limactl` and accessible `/dev/kvm`; local host checks do not
complete the full QA.md matrix. Publication authorization does not mark these
checks complete.

## Published result

- The fix was fast-forward merged into `main` as
  `608580b4a7feeedeac2f7cde4655affab5fdc745`. Annotated tag `v0.3.1` resolves
  to that same commit.
- [Main CI](https://github.com/brianrackle/codelima/actions/runs/34529111917)
  passed Linux/macOS verification and race tests, plus daemon integration.
- [Release workflow](https://github.com/brianrackle/codelima/actions/runs/34529111583)
  passed every job. `make verify test-race test-integration test-package`
  passed on macOS arm64 and Linux amd64/arm64 before publication.
- [v0.3.1](https://github.com/brianrackle/codelima/releases/tag/v0.3.1) is a
  regular release and GitHub Latest, with all three archives and manifests.
- Tap commit `b7298922eaf5a3913015f646e1de07c794b18211` updates the standard
  `Formula/codelima.rb` to 0.3.1. It exactly matches the formula generated from
  the downloaded manifests; the retired beta formula remains absent.
- Every public asset's size and SHA-256 match GitHub's metadata. Archive
  checksums match the manifests and formula; each archive contains exactly
  the executable CLI and renderer worker. Renderer fingerprints match v0.3.0
  on all targets.
- `make test-package-artifact` passed against the downloaded Linux arm64
  package, including CLI version/provenance and real renderer output/read
  with an empty runtime PATH. This verifies the published artifact without
  rebuilding it.
- Downloads, tap scratch checkout, test fixtures and logs were removed.
  No verification services remain. Normal ignored development builds and
  toolchain caches are retained.

| Target | Archive bytes | Archive SHA-256 |
| --- | ---: | --- |
| darwin/arm64 | 12876499 | `78f07e9a4ee658dcd122872efb7b8d8e495632490842486c416505492463a731` |
| linux/amd64 | 13200454 | `72cfa2b49c89e3c25463a60ff812ba426df7d320e649d7dacac8ef548aba5c86` |
| linux/arm64 | 12400453 | `4cc4755aab9a5b50466cd8d9a4459a15a78c1084780c1c28f8f7356cd78f3f1c` |
