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

Three-platform CI, publication and downloaded artifact evidence are recorded
below.

## Release recovery and local upgrade verification

The copy fixes were already on `main` at
`948ccea66b6eada589417a468edfb9aa524e59b3`, with annotated tag `v0.3.5`
resolving to that same commit. [Main CI](https://github.com/brianrackle/codelima/actions/runs/34627572998)
passed. The first [release attempt](https://github.com/brianrackle/codelima/actions/runs/34627573149)
passed on macOS arm64 and Linux amd64. Linux arm64 stopped during `make init`
before application tests: downloading `honnef.co/go/tools@v0.5.1` from the Go
module proxy returned an HTTP/2 `INTERNAL_ERROR`. The maintainer requested
completion of the release, and only failed jobs were retried with the same tag
and checks.

The versioned Linux arm64 candidate reported `0.3.5`. In an isolated home,
schema 4 / seed 9, all five resource presets, native agent definitions, and
non-mutating schema-v3 rejection were checked. `doctor` confirmed this VM
lacks `limactl` and access to `/dev/kvm`; physical-host checks remain open.
QA.md's stale seed-7 expectation was corrected to the observed seed 9.

The published `v0.3.4` executable started an isolated daemon and a real host
shell using disposable node metadata. After output filled the journal to
1,045,529 bytes, the candidate's no-argument `daemon update` preserved the
terminal ID, shell PID and final output marker. An explicit-path update also
preserved that shell while a command was running. Both reported live handoff,
and anchored markers confirmed actual command output and fresh input afterward.

Stopping only the disposable renderer triggered recovery from generation 1
to 2 with a new renderer PID, the same shell PID and responsive output. The
Flow 9 diagnostic capture succeeded without changing daemon PID or terminal
IDs. These host-shell checks do not establish the guest-shell or interactive
parts of Flow 5.

The first fresh local verification used an overly deep project-local `TMPDIR`
and exceeded the 160-character metadata path limit in several test fixtures.
Using the short project-rooted `tmp` directory resolved those path failures.
That next run encountered `text file busy` executing the fake Lima CLI in
`TestLimaClientLifecycleWithFakeLimactl`; investigation remains in TODO #56.
The unchanged suite passed on its one retry: `make verify test-race
test-integration test-package test-clipboard test-native-agents`, with
`TMPDIR` set to the project's short `tmp` directory. No application tests or
release gates were disabled.

| QA flow | Evidence and remaining limit |
| --- | --- |
| 1 | Versioned CLI, seeded schema/presets and v3 rejection checked; Lima doctor prerequisites unavailable. |
| 2–4 | Real configuration/node lifecycle, clone, provisioning and guest ownership remain unverified without Lima. |
| 5 / 5b | Host-shell live updates, large history and renderer containment passed; guest shells and two-window seat checks remain unverified. |
| 6 | Real VM forwarding remains unverified without Lima. |
| 7 | Physical-terminal interaction remains unverified in this session. |
| 8 | macOS VirtioFS reclaim requires a macOS host. |
| 9 | Linux diagnostic capture passed and preserved daemon/terminal identity. |
| 10 | Clipboard/native regression evidence is recorded above; physical-host and full upstream Debug checks remain open. |
| 11 | Actual Homebrew installation/upgrade requires a Homebrew host. |

The outstanding native/manual work remains in TODO #41/#44/#54/#55.

## Publication

The Linux arm64 release job passed on its one retry, including `make verify`,
`make test-race`, `make test-integration` and `make test-package`. Together
with the successful first-attempt macOS arm64 and Linux amd64 jobs, all three
native release targets passed the required checks. The release and Homebrew
publication jobs also succeeded in
[run 34627573149](https://github.com/brianrackle/codelima/actions/runs/34627573149).

- [v0.3.5](https://github.com/brianrackle/codelima/releases/tag/v0.3.5) was
  published at 2026-09-11 18:07:46 UTC as a regular release and GitHub Latest,
  with all three archives and manifests. Its tag still resolves to `948ccea`.
- Homebrew tap commit
  [`e09657275fa29d96bdf1920470bb73575dd34bdc`](https://github.com/brianrackle/homebrew-codelima/commit/e09657275fa29d96bdf1920470bb73575dd34bdc)
  updates `Formula/codelima.rb` to 0.3.5. The downloaded formula exactly
  matches `make package-formula` using the published manifests; the beta
  formula is absent.
- Every public asset was downloaded and checked against GitHub's size and
  SHA-256 metadata. Archive hashes match their manifests. All three archives
  contain only the executable CLI and renderer worker with mode 0755.
- `make test-package-artifact` passed against the downloaded Linux arm64
  archive, checking version, build provenance and actual renderer operation
  with an empty runtime PATH. The published executable reported `0.3.5` and
  was not rebuilt for this test.
- Disposable daemons, shell and renderer processes were stopped. Candidate
  and previous-release binaries, homes, public downloads, scripts, formula
  copies, diagnostic captures and fixture INPUTRC state were removed. Normal
  ignored development binaries and toolchain caches remain.

| Target | Archive bytes | Archive SHA-256 |
| --- | ---: | --- |
| darwin/arm64 | 12890473 | `467c56b5fb1885c45d88d0b9b9dabe80cc351eb8cb807b8d5f97d1cad8a29473` |
| linux/amd64 | 13214573 | `b7c039870731f43069af33b645c680d1b93d2e22dbe779112b167a833ef9befc` |
| linux/arm64 | 12416204 | `da8c4cf36ad9fa142489111ff86bb0981ccdb3750329b612920b20394f66d778` |
