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
```

What each target does:

- `make init`
  - installs Go, `golangci-lint`, Zig, and a locally patched upstream `libghostty-vt` build
  - downloads Go modules
- `make build`
  - builds `./bin/codelima`
- `make verify`
  - runs `fmt`, `lint`, `test`, and `build`

Useful supporting targets:

```sh
make test
make lint
make fmt
make smoke
```

## Self-Hosted Development Metadata

The repository includes a sanitized self-host project metadata example at `examples/self-host/project.yaml`.
It mirrors the CodeLima project configuration used to develop this repository, but replaces host-specific values such as the local checkout path and hard-coded username assumptions.

Before using that file as live metadata, update:

- `workspace_path` to the absolute path of your local `codelima` checkout
- any bootstrap commands that need host- or distro-specific adjustments for your environment

## Release Artifacts

Release packaging is native per platform.
Each packaged archive contains:

- `bin/codelima`
  - wrapper script that exports `CODELIMA_GHOSTTY_VT_LIB`
- `bin/codelima-real`
  - compiled Go binary
- `lib/libghostty-vt.dylib` on macOS or `lib/libghostty-vt.so` on Linux
- `<asset>.json`
  - manifest with version, target platform, asset name, and SHA-256

Build a release archive for the current platform:

```sh
make package PACKAGE_VERSION=1.2.3 DIST_DIR=./tmp/dist
```

That target uses:

- `scripts/package_release.sh`
- `cmd/codelima-release`
- `internal/release`

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

- installs `git` and `lima` as runtime dependencies
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
   - `darwin-amd64`
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
2. Ensure the tap repo settings and token are configured.
3. Create and push the release tag:

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

### Release publishes assets but does not update Homebrew

Check:

- `HOMEBREW_TAP_REPO` is set
- `HOMEBREW_TAP_BRANCH` matches the tap default branch
- `HOMEBREW_TAP_TOKEN` exists and can push to the tap repo

### Homebrew formula changes are not pushed

The workflow skips the tap commit when the generated `Formula/codelima.rb` is identical to the existing file.
