# CodeLima 0.3.5 clipboard and native agent verification

The maintainer reported the clipboard issue fixed and requested merging and
releasing after the reproduced defects, local verification and remaining
physical-host qualification limits were explained.

ADRs 146–149 cover native agent installation as the guest login user, system
bubblewrap for Codex, clipboard routing to the seat owner's event connection,
and empty selection releases. Seed revision 9 migrates untouched built-in
environments. Daemon protocol 7, renderer protocol 3 and the native renderer
dependency remain unchanged.

## Local evidence

`make verify`, `make test-race`, `make test-integration`, `make test-package`,
`make test-native-agents` and `make test-clipboard` passed on Linux arm64 during
implementation. Gopls reported no diagnostics in the changed Go code. The
rebuilt CLI ran locally. Verification scratch artifacts were removed.

The real-socket TUI regression failed before the routing fix because the input
connection never subscribes to events; it now receives clipboard effects before
and after reconnect. A built CLI integration test sends a real shell's OSC 52
output through the native renderer and daemon to the owning event connection
and repeats with distinct Unicode text after reconnect. Ownership tests exclude
observers and superseded streams. Native tests cover fragmented OSC 52 through
64 KiB, the previous no-selection `-4` error, and normal word-selection copying.
The TUI continues to use the outer terminal's clipboard handling.

Executable installer fixtures cover privilege dropping, failed downloads,
installer errors, missing binaries, cleanup and legacy migration. Real vendor
installers succeeded under disposable non-root homes for Codex 0.154.0 and
Claude Code 2.1.268. An extracted Ubuntu arm64 bubblewrap package reported
version 0.11.1. Disposable seeded metadata confirmed bubblewrap is specific to
Codex. These checks do not replace real Lima provisioning.

## Remaining qualification

The user's "fixed" report confirms their reported clipboard symptom; it does
not establish every multi-window, SSH, Unicode or near-limit case in QA.md.
Those checks and real Lima native-agent provisioning/permission-bypass flows
remain in TODO #54/#55. Broader macOS physical-terminal, VM/forwarding, Homebrew
upgrade and full upstream Ghostty Debug qualification remain in TODO #41/#44.
This Linux guest has no `limactl` or accessible physical Mac clipboard.
Release authorization does not mark the remaining QA flows complete.

## Release qualification

The 0.3.5 Linux arm64 candidate was packaged under a disposable project-local
directory. `make test-package-artifact` passed against that archive, including
checksum, executable provenance, version and actual native renderer operation
with an empty runtime PATH. The versioned executable reported `0.3.5`.
Release metadata, native installer and clipboard regression checks also passed.

Three-platform CI, publication and downloaded artifact evidence will be
recorded here as each check completes.
