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
One shell emitted 1.1 MB of output; its journal reached 922,671 bytes before
handoff. The versioned `0.3.4` candidate's no-argument `daemon update` and a
second explicit-candidate-path update both reported live handoff. Both
terminal IDs and shell PIDs survived, retained output remained readable,
and fresh input and a command running during the explicit update completed.

## Remaining qualification

Native macOS physical-terminal checks, real Lima VM/forwarding flows, multi-window
interaction, actual Homebrew installation/upgrade and the full upstream Ghostty
Debug suite remain open in TODO #41/#44/#51. This Linux guest lacks `limactl`
and accessible `/dev/kvm`. Release authorization does not complete those checks.
The three-platform automated release matrix remains mandatory before publication.
