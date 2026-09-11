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

Pending the local release checks, merge, tag, three-platform release workflow,
published-asset verification and Homebrew tap update.
