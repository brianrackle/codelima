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

The release workflow must pass verification, race, integration and package
checks on macOS arm64 and Linux amd64/arm64 before publishing `v0.3.2` and
updating the standard Homebrew formula. Results will be recorded here after
the workflow and published artifacts have been verified.
