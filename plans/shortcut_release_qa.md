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

## Publication gates

The release workflow must pass `make verify test-race test-integration
test-package` on macOS arm64 and Linux amd64/arm64 before publishing the
regular release and updating `Formula/codelima.rb`. Public archives and
manifests, the tap formula, and the downloaded native artifact will be checked
after publication; results will be recorded here.
