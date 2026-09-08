# Homebrew Beta Preparation — 2026-09-08

Candidate: `0.3.0-beta.1` on `feat/libghostty-vt-adoption`, including the local
release-support changes over `90e22d89f34e757ec977f7cfbdb2aec9d5b100a2`.
No tag, GitHub release, or public tap update has been created.
The maintainer subsequently authorized publishing this beta on 2026-09-08
with the remaining native checks explicitly marked unverified. The exception
does not convert any unexecuted QA flow into a passing result.

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
