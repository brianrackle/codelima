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
  - installs Go, `gopls`, `golangci-lint`, Zig, `pkgconf` (as `pkg-config`), and a locally patched upstream `libghostty-vt` build
  - downloads Go modules
- `make build`
  - builds `./bin/<os>-<arch>/codelima` and its `codelima-renderer-worker` companion
  - refreshes `./bin/codelima` as a compatibility symlink to the current platform's binary
- `make verify`
  - runs `fmt-check`, `lint`, `test`, `test-vaxis-fork`, and `build`
- `make test-race`
  - runs every Go package with the race detector serially by default
- `make test-tui-input`
  - runs decoded tab, focus, search, dialog and selector shortcut regressions,
    including followups across overlays and terminal payload preservation;
    use `GOFLAGS=-race` for the race detector or
    `TUI_INPUT_TEST_FILTER='^TestTUIShortcut'` for the application shortcut set
- `make test-integration`
  - builds the real CLI and exercises daemon lifecycle, isolated renderer spawning, stale recovery, PTY continuity across framed-stream live update, rollback after an injected import failure, delayed legacy-macOS restart fallback, and startup recovery while the previous daemon still owns its shutdown lock
  - uses the deliberately short `./tmp/i` root so derived Unix handoff socket paths remain within platform limits; override it with `INTEGRATION_TMP` only with an equally short path
- `make test-tui-resize`
  - exercises window growth/shrinkage, minimum-size recovery, pixel-only changes,
    overlays, daemon geometry propagation and idle PTY handoff after resizing;
    use `GOFLAGS=-race` to check races
- `make diagnose-terminal-freeze`
  - runs the repository `diagnose-codelima-terminal-freezes` skill's read-only capture script without rebuilding or restarting CodeLima
  - writes incident evidence under `./tmp/terminal-freeze-*`; pass `DIAG_ARGS='--home PATH --binary PATH --terminal-id ID'` to override discovery

Ordinary Go tests use `GO_TEST_PARALLEL=4`; race tests default to
`GO_RACE_TEST_PARALLEL=1`. Override these only after qualifying the host.

The Ghostty worker requires cgo and `pkg-config`. Make enables cgo and exports
`PKG_CONFIG` as the exact platform-local `.tooling/<os>-<arch>/bin/pkg-config`
path for cgo, native bridge checks, and benchmarks. This is upstream
`pkgconf` `2.5.1`, built from pinned, checksum-verified source using
managed Zig, not a script that special-cases Ghostty flags. No system
`pkg-config`, Homebrew package, or host C compiler is needed to provision it.
Make uses a host `cc` for cgo when present and otherwise uses managed Zig.
Zig and `pkgconf` are installed before Go-based development tools. The
CLI/daemon package graph contains no Ghostty cgo. Linux release packaging builds
the main executable with `CGO_ENABLED=0`; macOS enables cgo only for its existing
Virtualization.framework host-capability adapter (ADR 136). The renderer worker
uses cgo on every platform.

`make pkg-config` provisions just Go, Zig, and the metadata resolver;
`make test-pkgconf` adds its focused installer and resolver tests. Both
`make init` and `make ghostty-vt` share this prerequisite so a native-only
recipe works from a checkout without preinstalled Go or `pkg-config`.

Vaxis is pinned to `v0.17.1` with a local replacement in `third_party/vaxis`.
The complete upstream module, tests, and Apache-2.0 license are retained;
`UPSTREAM.md` records the verified module checksums and narrow encoded-image
API delta. `make test-vaxis-fork` runs its full module suite. Keep dependency
changes in this reviewed copy, never in the Go module cache. The added API
uploads already-encoded PNGs synchronously within a wire-byte budget, keeps
multipart Kitty transfers serialized, and exposes exact-pixel/z-order placement
without asynchronous resizing workers.

Vaxis resize events require an explicit `Vaxis.Resize` call on the UI event
loop before drawing or calculating pane geometry. Handle them before overlay
input routing so dialogs cannot leave the screen buffers at their old size
(ADR 140).

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

### Native Renderer Dependency

The audited dependency is Ghostty commit
`82232ecde55405559dec29c5466cb9e39938cb41`, compiled with Zig `0.16.0` as a
static archive. The selected features are formatter, selection, render-state,
input-encode, color, grid-introspection, snapshot, search, and kitty-graphics;
all other optional features are disabled. The default profile is
`ReleaseSmall`, target `native`, CPU `baseline`.

Four separately checksummed patches retain the characterized XTQMODKEYS
reply, expose clipboard acknowledgement requirements, and enforce the bounded
static-graphics policy/checkpoint guard and allocation-bounded snapshot decode.
Their digests and the native inputs
are reviewed in `internal/rendererbuild/profile.go`. The installer checks
these inputs before cache reuse, verifies the official Zig archive checksum,
and retains upstream Zig dependency hashes. It does not replace dependency
manifests with unverified local paths.

Builds are isolated under `.tooling/<os>-<arch>/ghostty-vt`. The exact upstream
checkout lives in `sources/<commit>`; `current/source` is the matching patched
build tree. A completed immutable install is published through the platform's
`current` link only after successful compilation. A kernel lock serializes
installers, and failed builds leave the previous current install available.
There is no cross-platform `.tooling/ghostty-vt/current` include-path alias.

The managed `pkg-config` resolves `libghostty-vt-static` through
`current/share/pkgconfig`, using `current/include` and
`current/lib/libghostty-vt.a`. The native fingerprint covers source, Zig,
features, patch digests, width policy, platform and compiler options. The same
fingerprint is embedded in the C header, static archive, CLI and worker, and
checked at native initialization and the worker handshake. Runtime loading,
library-path search, and `CODELIMA_GHOSTTY_VT_LIB` overrides are not supported.
The worker still uses the platform's normal system C libraries.

Use these qualification targets when changing the native boundary:

```sh
make test-installers
make test-renderer-boundary
make test-ghostty-vt-schema
make test-ghostty-vt-build
make test-ghostty-vt
make test-ghostty-bridge
make test-ghostty-adapter GHOSTTY_TEST_FILTER='Ghostty|CloneColors|ValidateColors'
make benchmark-ghostty-compression
make test-package
```

Upstream unit execution and compile-only unit validation use `Debug`, since
the upstream page-list tests require tracked-pin safety instrumentation.
They default to `GHOSTTY_VT_TEST_JOBS=1` and can require substantial memory;
`GHOSTTY_VT_UNIT_FILTER=clipboard` is a focused diagnostic, not a substitute for
the full suite. ABI schema and adapter checks use the selected production
profile. The compression benchmark runs fresh off/on processes and reports
current RSS on Linux, bounded-step latency and resumed-input timings; RSS is
reported as unavailable on other platforms.

Changing the source or a patch requires reviewing its profile constant and
rebasing the narrow patches before rebuilding. Compiler-profile overrides
(`GHOSTTY_VT_TARGET`, `GHOSTTY_VT_CPU`, `GHOSTTY_VT_OPTIMIZE`, or
`GHOSTTY_VT_FEATURES`) require rebuilding both executables with Make so their
embedded fingerprint follows the installed dependency. Qualify release
packages on each native target rather than reusing another platform's cache.

Handoff version 5 keeps terminal metadata in the manifest and transfers raw
replay and renderer recovery envelopes through ordered 512 KiB chunks. JSON
base64 expansion keeps every encoded chunk below the 1 MiB frame limit. Raw
replay is capped at 1 MiB per terminal and 64 MiB per handoff. Recovery envelopes
are separately capped at 24 MiB per terminal and 64 MiB per handoff before
allocation; retained native checkpoints also share a 64 MiB daemon memory quota.
An envelope binds terminal identity, native build identity, integrity, application
color policy, and ordered journal watermarks. Same-build recovery uses an eligible
checkpoint plus a complete tail; cross-build recovery keeps the bounded ordered
raw fallback. Native snapshots exclude image state, so live or in-progress images
prevent new checkpoints. A truncated raw fallback reports partial recovery.
The importer also accepts version-4 chunked replay and version-3 stream manifests
when the old daemon can encode them below its compiled limit. An already-oversized
version-3 sender requires closing high-history tabs or one terminal-restarting
stop/start.

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

The built-in `codex` and `claude-code` environments use the official native
installers. Noninteractive Lima commands still cross the root boundary in
`LimaClient.Shell` for system prerequisites. The shared installer wrapper resolves
`SUDO_USER`, rejects UID 0, and drops to that login user before downloading or
executing the vendor script. A failed download must never execute a partial
script; temporary scripts are removed on exit. Only the compatibility links in
`/usr/local/bin` are published by root after installation succeeds. Agent
execution and update state belong to the login user.

Run `make test-native-agents` for executable installer failure/identity checks
and exact legacy migration coverage. Run `make verify test-race test-integration
test-package` for release checks. Keep both environments independently usable.
Installer definition changes require a seed-revision bump plus exact legacy
specs: revision 9 also migrates the revision-8 native Codex definition to
install the system `bubblewrap` package and check `bwrap --version` before
agent installation. Older npm definitions migrate to native installers while
preserving customized and deleted records. `NodeStart` repairs exact legacy
bootstrap sequences and reruns installation. It does not remove old npm package
files or credentials (ADRs 146–147).

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
selection, one-second bind retry and claimant transfer, the port-1455 Codex
login exception that transfers generic routing to the newest listener, and
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
  - the compiled CLI/daemon executable, with no Ghostty dependency
- `bin/codelima-renderer-worker`
  - the private renderer executable, statically linked to the audited Ghostty archive

The adjacent `<asset>.json` manifest records version, target platform, asset
name, archive SHA-256, and `renderer_build_id`. It is not an entry inside the
tar archive. There is no wrapper script, `codelima-real`, or packaged Ghostty
shared library. Keep both executables together: the daemon resolves the
worker beside its own executable, not through `PATH`.

Artifact size varies by target, Ghostty features, and Go toolchain and must be
recorded during release qualification.

Build a release archive for the current platform:

```sh
make package PACKAGE_VERSION=1.2.3 DIST_DIR=./tmp/dist
```

That target uses:

- `scripts/package_release.sh`
- `cmd/codelima-release`
- `internal/release`

`make package` validates the selected native profile and rebuilds both
platform-scoped executables with the same `PACKAGE_VERSION` and native
fingerprint before archiving them. Stop or live-update any daemon running from
those paths first: the daemon protocol requires an exact binary version, so a
newly packaged CLI correctly rejects an older development daemon. Run
`make build` afterward to restore the normal development version.

Direct `scripts/package_release.sh` invocation still requires the verified
static renderer install. After checking its identity, the script provisions
the pinned Zig/`pkgconf` tools and exports the same absolute `PKG_CONFIG` path
as Make; it does not depend on a developer's shell finding `pkg-config`.
Neither build-time tool is included in the release archive.

`make test-package` performs a self-cleaning release smoke test under `./tmp`
without replacing development binaries. It verifies the two-entry archive,
checksum, executable modes, embedded build identities, CLI help/version, and
native worker initialization/output/read with an empty `PATH` and unavailable
legacy shared-library paths. Packaging is deliberately passed an invalid
inherited `PKG_CONFIG` to verify that direct packaging selects the managed
resolver itself. Run it on every release platform.

To verify an already downloaded native archive without rebuilding that archive,
place it and its single matching manifest in a directory and run
`make test-package-artifact DIST_DIR=./tmp/downloaded-native-release`. This uses
the same checksum, build-provenance, executable and real renderer smoke checks.
Remove downloaded verification artifacts afterward.

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
- installs both executables together in `libexec/bin`
- links `bin/codelima` to the installed CLI; no library cache or launcher is required

Tags must use `vMAJOR.MINOR.PATCH` and generate `Codelima` in `codelima.rb`.
The formula generator rejects prerelease tags and tag/manifest version
mismatches. Without `FORMULA_OUTPUT`, Make writes `$(DIST_DIR)/codelima.rb`.

`make package-formula`, `make release-metadata`, and `make test-release` need
only the managed Go toolchain; they do not build the native renderer. Inspect
metadata with `make --silent release-metadata RELEASE_TAG=v0.3.0`.
The Go metadata parser is shared by formula generation and GitHub Actions.

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

1. Requires an existing `vMAJOR.MINOR.PATCH` tag and resolves its version.
   Manual dispatch checks out the requested tag.
2. Runs `make verify test-race test-integration test-package`, then builds
   release archives from that exact tag on:
   - `linux-amd64`
   - `linux-arm64`
   - `darwin-arm64`
3. Creates the GitHub release with all `.tar.gz` archives and `.json` manifests.
   Requires nonempty `.github/release-notes/<tag>.md` and prepends its contents
   to generated notes. The release is regular and explicitly becomes Latest.
   Existing releases are not overwritten; if only the tap update failed, rerun
   that failed job.
4. Generates `Formula/codelima.rb`.
5. Updates the Homebrew tap if configured, removing the retired
   `Formula/codelima-beta.rb` in the same commit. Repeating this update is a no-op.

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
   verify handoff version 5 preserves its terminal ID, shell PID, final replay
   marker, and responsive daemon. Exercise both eligible same-build checkpoint
   recovery and cross-build bounded raw fallback; verify a truncated fallback is
   marked partial rather than claiming full terminal-state preservation.
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

The maintainer authorized `v0.3.2` after the resize/input fixes and remaining
native/manual qualification gaps were reported. Its release notes retain
those limits; publication still requires the three-platform automated release
matrix. Local and published evidence belongs in
[the resize release report](plans/resize_release_qa.md), with outstanding
manual work in TODO #41/#44/#46.

The maintainer authorized `v0.3.3` after the tab status and bell fixes and the
remaining native/manual qualification gaps were reported. Daemon protocol 7
adds metadata to dirty events; upgrade verification must cover a protocol-6
daemon from `v0.3.2`, preserving live terminals through the candidate's
update-only compatibility handshake. The frontend must be reopened to use the
new labels and acknowledgement behavior. Release evidence belongs in
[the tab status release report](plans/tab_status_release_qa.md), with remaining
manual work in TODO #41/#44/#50.

The maintainer authorized `v0.3.4` after the compact tab defaults and remaining
native/manual QA gaps were reported. This frontend presentation change keeps
daemon protocol 7 and the renderer dependency unchanged. Reopen the TUI after
upgrading. Release evidence belongs in
[the compact tab release report](plans/compact_tab_release_qa.md), with remaining
manual work in TODO #41/#44/#51. Publication requires the normal three-platform
automated release matrix.

The maintainer authorized `v0.3.5` after reporting the clipboard issue fixed.
It also includes the authorized native agent installers and Codex bubblewrap
provisioning. Update the daemon and reopen attached TUIs after upgrading;
untouched built-in agent environments migrate on their next node start.
Daemon protocol 7, renderer protocol 3 and the native dependency are unchanged.
Evidence belongs in [the clipboard and native agents release report](plans/clipboard_native_agents_release_qa.md).
Remaining manual qualification is tracked in TODO #41/#44/#54/#55, and
publication requires the normal three-platform automated release matrix.

### Libghostty Promotion To The Regular Release

On 2026-09-08 the maintainer explicitly requested promoting the published
`v0.3.0-beta.3` to a regular release and removing the beta path. Version
`v0.3.0` retains that candidate's application code and native packaging fixes;
only release tooling and documentation change. Historical beta tags/releases
remain available as records. ADR 137 supersedes the separate channel policy.

The beta passed automated verification, race, integration and package tests on
macOS arm64 and Linux amd64/arm64. The native/manual and interactive checks in
TODO #41/#43/#44 remain unverified. The maintainer authorized publication with
these limitations and then explicitly requested this promotion. This exception
does not mark those checks complete or waive qualification for future releases.
The regular release notes carry these limitations forward.

After qualifying and committing the candidate on `feat/libghostty-vt-adoption`:

```sh
make --silent release-metadata RELEASE_TAG=v0.3.0
git tag -a v0.3.0 -m 'CodeLima 0.3.0: libghostty-vt adoption'
git push --atomic origin feat/libghostty-vt-adoption refs/tags/v0.3.0
```

Verify the tag resolves to the candidate commit, GitHub marks it regular and
Latest, and the standard tap formula's URLs/checksums match all three uploaded
manifests. Confirm the beta formula is absent. Run the Homebrew upgrade flow in
`QA.md` on native targets. Workflow artifacts and tap scratch checkouts stay
under `./tmp/release` in the disposable runner.

## Manual Release Dry Run

Before the first real release, do a local dry run:

```sh
make verify
make test-race
make test-integration
make test-package
make package PACKAGE_VERSION=0.0.0 DIST_DIR=./tmp/dist
make package-formula \
  PACKAGE_VERSION=0.0.0 \
  RELEASE_TAG=v0.0.0 \
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

### Native build identity or header/archive mismatch

Run `make init` and then `make build` using the same selected compiler profile.
Do not copy headers or archives between installs, or mix a release worker with
a different CLI. If an audited patch changed, update its reviewed profile
digest only after reviewing the patch. Cache reuse deliberately rejects
unreviewed inputs; bypassing that check can invalidate checkpoint compatibility.

### `pkg-config` executable not found

Use `make build` or `make init` to provision the managed binary; installing a
Homebrew package is not necessary. Make and the release script select it
explicitly even when the host has no `pkg-config`. For ad-hoc `go` commands,
export `PKG_CONFIG` to `.tooling/<os>-<arch>/bin/pkg-config` using an absolute
path, and set `PKG_CONFIG_PATH` to the matching Ghostty install's
`current/share/pkgconfig`. Prefer the Make targets so these paths and the
embedded native build identity remain consistent.

### `make init` stalls while building Ghostty

The first build downloads the pinned upstream source and content-addressed Zig
dependencies; a cold cache therefore needs network access. Subsequent builds
reuse the verified caches in `.tooling/<os>-<arch>/cache`. Reduce
`GHOSTTY_VT_BUILD_JOBS` if the compiler exhausts host memory. The installer
passes `-Demit-xcframework=false` on all platforms: CodeLima consumes a static C
archive, not an xcframework or shared library. A failed build does not publish
its staging tree or replace the previous working install.

### Release publishes assets but does not update Homebrew

Check:

- `HOMEBREW_TAP_REPO` is set
- `HOMEBREW_TAP_BRANCH` matches the tap default branch
- `HOMEBREW_TAP_TOKEN` exists and can push to the tap repo

### Homebrew formula changes are not pushed

The workflow skips the tap commit when the generated `Formula/codelima.rb` is identical to the existing file.

Clipboard changes should run `make test-clipboard` and the regular release
checks. The TUI delegates host clipboard handling to the outer terminal through
OSC 52, even when a native desktop utility is available. Tests must not modify
the developer's clipboard. Bridge tests cover fragmented responses through the
existing 64 KiB limit. The socket regression test uses the TUI's separate request
and event connections and checks clipboard delivery before and after event
reconnect; daemon tests cover seat takeover and superseded subscriptions.
`make test-integration` also sends OSC 52 from a real shell through the built
renderer and daemon to the owning frontend's event connection, then reconnects
and repeats with distinct Unicode text.
Native interaction tests also check that releasing without a selection is a
quiet no-op and that word-selection copying still works.
Real macOS and SSH clipboard qualification remains in
QA.md; offline tests do not establish outer-terminal permission behavior.
