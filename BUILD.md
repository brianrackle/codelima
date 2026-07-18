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
  - runs every Go package with the race detector
- `make test-integration`
  - builds the real CLI and exercises daemon lifecycle, stale recovery, PTY continuity across live update, and rollback after an injected import failure
  - uses the deliberately short `./tmp/i` root so derived Unix handoff socket paths remain within platform limits; override it with `INTEGRATION_TMP` only with an equally short path

Useful supporting targets:

```sh
make test
make test-race
make test-integration
make lint
make fmt
make smoke
make gopls GOPLS_ARGS="check internal/codelima/tui_test.go"
```

The source checkout intentionally namespaces development binaries by the same platform tag used for `.tooling`, such as `linux-aarch64` or `darwin-arm64`. This prevents a host build and a guest build in the same shared checkout from overwriting each other's executable. Use `make run` or `make tui` when possible; both invoke the platform-scoped binary directly.

Runtime-backed manual checks require a Microsandbox-capable host. CodeLima embeds the official Go SDK at `v0.6.6`; the SDK's `EnsureInstalled` path installs its matching `msb` and `libkrunfw` support files under `~/.microsandbox` when absent. CodeLima never shells out to that binary, and there is no CLI fallback. Release qualification must start from both a warm install and an empty SDK runtime cache. Keep `MSB_HOME` short enough for platform Unix-socket path limits.

Dynamic forwarding uses the pinned `golang.org/x/crypto/ssh` module over a hidden CodeLima helper process. The helper connects with the Go SDK, prepares a per-sandbox SDK SSH server, and serves it on stdin/stdout; it invokes neither `msb` nor host OpenSSH. Release qualification must verify `{node}.localhost` HTTP and Upgrade traffic on both native platforms, including two nodes sharing one guest port and a guest-loopback-only service.

## Self-Hosted Development Metadata

The repository includes a sanitized reusable configuration example at `examples/self-host/configuration.yaml`. Configurations are directory-independent in schema v3. Import or reproduce its fields in a live configuration, then create a node with that configuration while the node directory points at the local checkout.

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

The Go SDK embeds one platform-specific FFI library. As a reference point, the
unstripped local `linux/arm64` development binary measured 32,649,456 bytes
after the SDK migration; artifact size varies by target and Go toolchain and
must be recorded during release qualification.

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

- installs `git` as a runtime dependency; microsandbox remains an explicit host prerequisite
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
3. Complete every flow in `QA.md`, including the native macOS and Linux microsandbox qualification and interactive TUI checks.
4. Verify `daemon update` with the candidate packaged binary while a long-running terminal command is active.
5. Ensure the tap repo settings and token are configured.
6. Create and push the release tag:

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
