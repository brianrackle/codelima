# Homebrew Beta Preparation — 2026-09-08

This is the historical beta publication record. The maintainer subsequently
requested regular promotion: [v0.3.0](regular_release_qa.md) is now published
through `codelima`, and the separate beta formula has been removed.

## Published result

Published [v0.3.0-beta.3](https://github.com/brianrackle/codelima/releases/tag/v0.3.0-beta.3)
from `1c146b71e0a43a29b84d7426caa02f210ed6428c` on the libghostty branch.
[Release workflow 34252513885](https://github.com/brianrackle/codelima/actions/runs/34252513885)
passed every job, including verify, race, integration and package smoke tests
on macOS arm64 and Linux amd64/arm64. The macOS fixes and output-based handoff
checks passed on the actual native runner.

Homebrew published `Formula/codelima-beta.rb` in tap commit
`a8ecf2967f073eba233d4e4561f7a78866e4f8db`. All three formula URLs and checksums were compared
with the downloaded release manifests and archives. Each archive contains
exactly the executable CLI and renderer worker. The release is a published
prerelease, contains the explicit qualification limitations, and Latest still
points to `v0.2.3`. The stable formula remains byte-for-byte unchanged (SHA-256
`1e6f8bf759e853d9a7d3a9a9fee99789f192c150af79aeb2713596fbe610d3ad`).

`make test-package-artifact` passed against the actual downloaded Linux arm64
archive: build provenance, version, modes, empty-PATH CLI help/version, native
worker initialization and output/read. The shared `make test-package` recipe
also passed after extracting that reusable artifact-check command.

| Published archive | Bytes | SHA-256 |
| --- | ---: | --- |
| `codelima_0.3.0-beta.3_darwin_arm64.tar.gz` | 12,872,590 | `fdf4f118762fc1575388a577c3bf1394cc50c929001d84390426e09b263b256a` |
| `codelima_0.3.0-beta.3_linux_amd64.tar.gz` | 13,192,098 | `f89d144c81115a2e1d8c7e386c392990d4c0e21449895ad9eef83d747c9ea025` |
| `codelima_0.3.0-beta.3_linux_arm64.tar.gz` | 12,396,729 | `4b41d7065b6bcd0b86b8acbf5bb98c13ba6721d1217cbe33ff4f13f76ebebf5b` |

Manual Homebrew installation, native VM/forwarding and physical/interactive QA,
and the full upstream Ghostty Debug suite remain unverified as disclosed.
Publication is complete; remaining qualification stays in TODO #44.
All downloaded archives, manifests and temporary verification metadata were
removed after validation, and package tests cleaned their renderer processes.

## Initial preparation history

Candidate: `0.3.0-beta.1` on `feat/libghostty-vt-adoption`, including the local
release-support changes over `90e22d89f34e757ec977f7cfbdb2aec9d5b100a2`.
At preparation time no tag, GitHub release, or public tap update existed.
The maintainer subsequently authorized publishing this beta on 2026-09-08
with the remaining native checks explicitly marked unverified. The exception
does not convert any unexecuted QA flow into a passing result.

The first tag `v0.3.0-beta.1` points to
`35785cb699d61c13ba5779d0b451dad961fe480c`. Its
[native workflow](https://github.com/brianrackle/codelima/actions/runs/34250335019)
failed macOS verification: Alt+x encoded to no bytes and the portable cgo guard
rejected the existing host-capability adapter. No release or tap update was
published. ADR 136 corrects both issues and preserves the adapter in macOS
packages. The corrected release candidate is `v0.3.0-beta.2`.

The second candidate `effc43ffd888a3067365174bc03c294b0a254ff5` passed macOS
verify and race checks and both complete Linux jobs in
[its workflow](https://github.com/brianrackle/codelima/actions/runs/34251455962).
macOS integration failed because counter progress had reached only 28 within
the five-second window and rollback had reached only 5 after the fixed delay.
Beta.3 replaces fixed readiness delays with bounded waits for exact output,
retains every counter-continuity assertion, and adds shell PID preservation
checks. The second candidate also produced no public release or tap update.

## Automated verification

Passed on Linux/aarch64:

* `make verify` (format, lint, all Go packages, Vaxis fork, both binaries).
* `make test-race test-integration test-package`.
* `make test-release`, including execution of the actual publication shell
  with a recording GitHub CLI and execution of the tap update shell against
  disposable local Git repositories. First beta creation preserves the stable
  file; repeating the update creates no extra commit.
* `make gopls` checks of the changed Go implementation and tests.

Tag parsing rejects unknown suffixes, malformed versions and tag/manifest
version mismatches. Beta publication sets prerelease and excludes Latest;
stable publication retains its normal policy. Both paths include all three
native archives and manifests in the tested GitHub CLI invocation. This is
local workflow execution with fixtures, not an actual GitHub Actions run.

## Local candidate execution

`make package PACKAGE_VERSION=0.3.0-beta.1` used isolated binary and dist
paths. `make package-formula RELEASE_TAG=v0.3.0-beta.1` selected
`codelima-beta.rb` without an explicit output filename. The formula is
`CodelimaBeta`, keg-only, with the matching version, URL and SHA-256.

The Linux/arm64 archive contains exactly the CLI and renderer worker, totals
12,396,199 bytes, and has SHA-256
`9f12a7fd7534dd71a6b1f65006ecc4d0d0097b4d6a3e0713f237d4a694fd7969`.
Its native identity is
`2e205255ce6d39bbfdf6f314824a31f12c8de3bbf32fac535a3d01e13289626c`.
The locally run candidate reports `0.3.0-beta.1`.

## Manual QA status

| Flow | Actual result |
| --- | --- |
| 1 | Candidate help, settings, metadata repair, schema 4/seed 7, all five presets, and non-mutating schema-v3 rejection passed in an isolated home. |
| 2 | Environment/configuration creation passed. Node creation failed with `DependencyUnavailable: inspect Lima version: exec: "limactl": executable file not found in $PATH`; frozen-node checks remain blocked. |
| 3–4 | Blocked by missing Lima and inaccessible KVM; no node/clone/VM lifecycle qualification claimed. |
| 5 | Candidate daemon start/status/stop passed with protocol 6. Full manual guest/host terminal flow remains blocked by node creation. Automated real worker/host-shell/handoff checks passed separately. |
| 5b, 7 | Two-window and native keyboard/graphics interaction unavailable. |
| 6 | Native guest forwarding blocked by missing Lima. |
| 8 | macOS VirtioFS requires a native Mac. |
| 9 | Full terminal-backed diagnostic flow not repeated; its node prerequisite is unavailable. |
| 10 | Package and in-project automated checks passed. Unfiltered upstream Debug/native qualification remains open in TODO #41; prior attempts exceeded this guest's memory. |
| 11 | Local archive, checksum and formula reviewed. Actual Homebrew install/test and public release/tap verification remain pending; `brew` is unavailable here. |

These results do not complete the repository's manual release gate. Remaining
work, proposed execution and tradeoffs are recorded in TODO #44.

## Cleanup

The isolated candidate daemon was stopped. Candidate binaries, archives,
formula, metadata homes, failed-node records and captured command output were
removed. Package tests and local tap tests cleaned their own disposable roots;
integration tests removed `tmp/i`. No VM or Homebrew installation was created.
