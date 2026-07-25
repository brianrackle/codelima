# Build And Release

This document is for maintainers.
User-facing setup and usage stay in `README.md`.

## Local Build

CodeLima bootstraps its own local toolchain under `.tooling/<os>-<arch>`.
Use the make targets from the repository root:

```sh
make init
make build
make verify
make test-race
make test-integration
make diagnose-terminal-freeze
```

What each target does:

- `make init`
  - installs Go, `gopls`, `golangci-lint`, Zig, and a locally patched upstream `libghostty-vt` build
  - downloads Go modules
- `make build`
  - builds `./bin/<os>-<arch>/codelima`
  - refreshes `./bin/codelima` as a compatibility symlink to the current platform's binary
- `make verify`
  - runs `fmt`, `lint`, `test`, and `build`
- `make test-race`
  - runs every Go package with the race detector serially by default
- `make test-integration`
  - builds the real CLI and exercises daemon lifecycle, isolated renderer spawning, stale recovery, PTY continuity across framed-stream live update, rollback after an injected import failure, delayed legacy-macOS restart fallback, and startup recovery while the previous daemon still owns its shutdown lock
  - uses the deliberately short `./tmp/i` root so derived Unix handoff socket paths remain within platform limits; override it with `INTEGRATION_TMP` only with an equally short path
- `make diagnose-terminal-freeze`
  - runs the repository `diagnose-codelima-terminal-freezes` skill's read-only capture script without rebuilding or restarting CodeLima
  - writes incident evidence under `./tmp/terminal-freeze-*`; pass `DIAG_ARGS='--home PATH --binary PATH --terminal-id ID'` to override discovery

All test recipes run packages and tests within each package serially by
default, avoiding filesystem-resource spikes on virtualized development hosts.
Override `GO_TEST_PARALLEL` or `GO_RACE_TEST_PARALLEL` only after qualifying
the host.

The Ghostty terminal integration requires cgo. Make enables cgo for every recipe, uses a host `cc` when present, and otherwise uses the managed Zig compiler. Zig is installed before Go-based development tools so `make init` also succeeds in minimal sandboxes without a system C compiler.

`make build` produces both `codelima` and the private
`codelima-renderer-worker` helper beside it. Release archives package both
executables. The helper is not a user-facing command: a daemon-owned terminal
starts it only through an inherited Unix socket descriptor, generation-fences
the link, and kills that process when a native operation exceeds its deadline.
Applied renderer mutations publish immutable screen/read bundles through a
capacity-one dirty edge at no more than 20 FPS; initialization and explicit
recovery snapshots remain immediate. Normal renderer calls do not write
start/completion info records, while failures and calls exceeding 250
milliseconds remain logged. Bulk output and ordered input share a bounded
mutation lane that backpressures only the owning PTY. Tracked health and
lifecycle calls use a separate control lane, and fire-and-forget mutations keep
unique event IDs without generating response frames.
Keeping the worker as a separate executable makes the native-code boundary
visible in the package graph and prevents the daemon from entering Ghostty
through a hidden mode of its own executable.

Handoff version 4 keeps terminal metadata in the manifest and transfers each
renderer replay through ordered 512 KiB raw chunks. JSON base64 expansion keeps
every encoded chunk below the 1 MiB frame limit. Import caps one terminal at 1
MiB and the complete handoff at 64 MiB before allocating or accepting replay.
A new importer accepts a version-3 stream manifest when that old daemon can
encode it below its compiled limit; an already-oversized version-3 sender
requires closing high-history tabs or one terminal-restarting stop/start.

Useful supporting targets:

```sh
make test
make test-race
make test-integration
make lint
make fmt
make smoke
make test-lima-native
make gopls GOPLS_ARGS="check internal/codelima/tui_test.go"
```

The source checkout intentionally namespaces development binaries by the same platform tag used for `.tooling`, such as `linux-aarch64` or `darwin-arm64`. This prevents a host build and a guest build in the same shared checkout from overwriting each other's executable. Use `make run` or `make tui` when possible; both invoke the platform-scoped binary directly.

Runtime-backed manual checks require Lima 2.1.0 or a compatible newer Lima
2.x release. Use VZ on macOS arm64 and QEMU/KVM on Linux amd64/arm64. Keep
`LIMA_HOME` short, private, and on a local filesystem that supports Unix
sockets. Release qualification must include a warm guest-image cache and a
clean cache so template resolution/download failures are visible. The gated
`make test-lima-native` recipe resolves the Ubuntu template and validates the
CodeLima-rendered YAML with the installed `limactl`.

The built-in `codex` and `claude-code` environments share one Node.js 22
prerequisite and user-owned npm-prefix pattern. Noninteractive Lima commands
still cross the single root boundary in `LimaClient.Shell`; the agent installers
then resolve `SUDO_USER`, run npm as that login user, and expose only stable
links in `/usr/local/bin`. Keep the two npm package commands independently
usable because either environment may be selected without the other. A new
installer definition requires a seed-revision bump plus exact legacy specs so
untouched environment records upgrade while customized and deleted records do
not. Existing node bootstrap snapshots remain frozen.

macOS release qualification must exercise nested virtualization on an Apple
silicon host where Virtualization.framework reports it supported and an
unsupported macOS case. Confirm `doctor`, newly rendered YAML, and `/dev/kvm`
inside both a new node and a pre-existing restarted node agree with the host
capability. Linux qualification confirms the rendered macOS-specific setting
remains false while the ordinary QEMU/KVM host checks still pass.

Dynamic forwarding uses the pinned `golang.org/x/crypto/ssh` module and a
persistent client per running node. Connection data comes only from Lima's
generated instance `ssh.config`; no hidden runtime helper or host OpenSSH
process is launched per node. The config path comes from Lima's machine output,
while its ownership and containment trust root comes from CodeLima's resolved
`LIMA_HOME`; do not require a `LimaHome` field in `limactl list --json`. The
daemon also owns one `limactl watch --json` observation process. Release
qualification must verify generic `localhost` and `127.0.0.1` claimant
selection, one-second bind retry and claimant transfer, and
`{node}.localhost` HTTP and Upgrade traffic on both native platforms, including
two nodes sharing one guest port and a guest-loopback-only service.

## Self-Hosted Development Metadata

The repository includes a sanitized reusable configuration example at `examples/self-host/configuration.yaml`. Configurations are directory-independent in schema v4. Import or reproduce its fields in a live configuration, then create a node with that configuration while the node directory points at the local checkout.

Review the bootstrap commands before use; they intentionally install development tools and may need distro-specific adjustments.

## Release Artifacts

Release packaging is native per platform.
The packaging script builds from the platform-scoped source binary, but the archive layout remains stable for end users.
Each packaged archive contains:

- `bin/codelima`
  - wrapper script that exports `CODELIMA_GHOSTTY_VT_LIB`
- `bin/codelima-real`
  - compiled Go binary
- `lib/libghostty-vt.dylib` on macOS or `lib/libghostty-vt.so` on Linux
- `<asset>.json`
  - manifest with version, target platform, asset name, and SHA-256

Artifact size varies by target, Ghostty library, and Go toolchain and must be
recorded during release qualification.

Build a release archive for the current platform:

```sh
make package PACKAGE_VERSION=1.2.3 DIST_DIR=./tmp/dist
```

That target uses:

- `scripts/package_release.sh`
- `cmd/codelima-release`
- `internal/release`

`make package` rebuilds the platform-scoped source binary with `PACKAGE_VERSION` before archiving it. Stop or live-update any daemon running from that path first: the daemon protocol requires an exact binary version, so a newly packaged CLI correctly rejects an older development daemon. Run `make build` afterward to restore the normal development version.

## Homebrew Formula Generation

The Homebrew formula is generated from the release manifests rather than maintained by hand.

Render the formula locally:

```sh
make package-formula \
  PACKAGE_VERSION=1.2.3 \
  RELEASE_TAG=v1.2.3 \
  RELEASE_REPO=brianrackle/codelima \
  DIST_DIR=./tmp/dist \
  FORMULA_OUTPUT=./tmp/dist/Formula/codelima.rb
```

The generated formula:

- installs `git` and Lima as runtime dependencies
- installs the packaged binary and Ghostty library into `libexec`
- writes a wrapper `bin/codelima` that points `CODELIMA_GHOSTTY_VT_LIB` at the packaged library

## GitHub Actions

### CI

`.github/workflows/ci.yml` runs:

```sh
make verify
```

on Ubuntu and macOS for pushes to `main` and pull requests.

### Release

`.github/workflows/release.yml` runs on:

- pushed tags matching `v*`
- manual dispatch with a `tag` input

The release workflow does this:

1. Resolves the tag and version.
2. Builds release archives on:
   - `linux-amd64`
   - `linux-arm64`
   - `darwin-arm64`
3. Uploads the `.tar.gz` archives and `.json` manifests to the GitHub release.
4. Generates `Formula/codelima.rb`.
5. Updates the Homebrew tap if the tap repo settings are configured.

## Homebrew Tap Automation

The tap repo is separate from the main source repo:

- source repo: `brianrackle/codelima`
- tap repo: `brianrackle/homebrew-codelima`

The release workflow expects these GitHub Actions settings on `brianrackle/codelima`:

- variable `HOMEBREW_TAP_REPO=brianrackle/homebrew-codelima`
- variable `HOMEBREW_TAP_BRANCH=main`
- secret `HOMEBREW_TAP_TOKEN`

`HOMEBREW_TAP_TOKEN` should be a GitHub token with push access to the tap repo only.
For a fine-grained PAT, grant:

- repository access: `brianrackle/homebrew-codelima`
- permission: `Contents: Read and write`

The token does not need write access to `brianrackle/codelima`.

## Releasing

Standard release flow:

1. Ensure `make verify` passes locally.
2. Ensure `make test-race` and `make test-integration` pass locally.
3. Complete every flow in `QA.md`, including native macOS VZ, Linux QEMU/KVM, Lima observation/forwarding, and interactive TUI checks.
4. Leave an attached TUI idle beyond the request timeout, then verify its next
   terminal action uses the existing connection without a `tui refresh failed`
   timeout. Generate sustained terminal output and verify rendering remains
   responsive, renderer snapshots stay within the 20 FPS ceiling, and the
   daemon log contains neither normal per-call renderer records nor repeated
   stale/fresh dirty pairs. Run `cmatrix -u 0` for at least 15 seconds and
   verify the renderer PID/generation stays ready, the prompt returns after
   `Ctrl+c`, and no `queue-full` connection closure or repeated daemon
   reconnection appears. Start it once more in a terminal with an adjacent tab,
   press `Option+w`, and verify the busy tab disappears immediately without
   freezing cursor or tab selection while daemon cleanup completes.
5. Verify both no-argument `daemon update` (which must select the invoking candidate binary) and `daemon update /explicit/candidate/path` while a long-running terminal command is active. For a protocol-changing release, start the old release first and verify the new candidate's update-only compatibility handshake preserves that terminal.
   Fill at least one renderer journal above 900 KiB before one update and
   verify handoff version 4 preserves its terminal ID, shell PID, final replay
   marker, and responsive daemon.
6. Ensure the tap repo settings and token are configured.
7. Create and push the release tag:

```sh
git tag v1.2.3
git push origin v1.2.3
```

The release workflow then:

- publishes the native archives and manifests to the GitHub release
- updates `Formula/codelima.rb` in `brianrackle/homebrew-codelima`

End users upgrade with:

```sh
brew update
brew upgrade codelima
```

## Manual Release Dry Run

Before the first real release, do a local dry run:

```sh
make verify
make test-race
make test-integration
make package PACKAGE_VERSION=0.0.0-qa DIST_DIR=./tmp/dist
make package-formula \
  PACKAGE_VERSION=0.0.0-qa \
  RELEASE_TAG=v0.0.0-qa \
  RELEASE_REPO=brianrackle/codelima \
  DIST_DIR=./tmp/dist \
  FORMULA_OUTPUT=./tmp/dist/Formula/codelima.rb
```

Check:

- the archive layout with `tar -tzf`
- the manifest JSON contents
- the rendered formula URLs and SHA-256 values

## Troubleshooting

### Every daemon-backed terminal freezes

Do not rebuild, stop, update, or send `SIGQUIT` before capturing the live
process. From the host that owns the daemon, run:

```sh
make diagnose-terminal-freeze
```

The target intentionally has no `build` or `init` prerequisite so incident
capture cannot replace a binary or contend with the affected daemon. Its output
includes independent control-plane probes, at most one read-only terminal actor
probe, daemon metadata and logs, process state, and a non-terminating macOS
`sample` stack capture when available. It uses `CODELIMA_HOME` when set and
otherwise defaults to `~/.codelima`. Interpret the bundle with
`.agents/skills/diagnose-codelima-terminal-freezes/references/interpretation.md`.

### `make init` fails when relinking Ghostty

The Ghostty installer maintains both:

- `.tooling/<os>-<arch>/ghostty-vt/current`
- `.tooling/ghostty-vt/current`

The first path is the real per-platform install root.
The second path is a compatibility link used by the cgo bridge include path.

If relinking fails, rerun `make init`; the installer removes and recreates both links.

### `make init` stalls while building Ghostty

The Ghostty installer now vendors Ghostty's `uucode` package into its temporary checkout before running Zig.
That keeps the local `libghostty-vt` build from depending on a live Zig package fetch in the middle of `make init` or `make ghostty-vt`.
The packaged Ghostty source commit is controlled by `GHOSTTY_VT_GHOSTTY_COMMIT` in `Makefile` and is intentionally kept aligned with the Ghostling `libghostty-vt` demo API surface.
When rebasing that commit, rebase `scripts/patches/ghostty-vt-codelima.patch` at the same time and verify it with `make ghostty-vt`.
On macOS the installer passes `-Demit-xcframework=false` because CodeLima loads `libghostty-vt.dylib` directly and does not consume Ghostty's lib-vt xcframework output.

### Release publishes assets but does not update Homebrew

Check:

- `HOMEBREW_TAP_REPO` is set
- `HOMEBREW_TAP_BRANCH` matches the tap default branch
- `HOMEBREW_TAP_TOKEN` exists and can push to the tap repo

### Homebrew formula changes are not pushed

The workflow skips the tap commit when the generated `Formula/codelima.rb` is identical to the existing file.
