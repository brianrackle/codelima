# Isolate Homebrew Beta Releases

Superseded by [ADR 137](use_one_regular_homebrew_release_channel_137.md).

## Context and Problem Statement

The libghostty branch needs an opt-in Homebrew beta. The existing release
workflow accepts every `v*` tag, creates a normal GitHub release, and always
overwrites the stable formula. Manual dispatch also builds its selected
workflow branch rather than necessarily the requested tag.

## Decision Drivers

* Preserve the stable Homebrew channel and GitHub Latest release during betas.
* Install the matching CLI and static renderer worker together.
* Share channel decisions between local packaging and release automation.

## Considered Options

* Publish beta versions through the existing stable formula.
* Build the branch from source through a Homebrew HEAD installation.
* Publish numbered prereleases through a separate binary beta formula.

## Decision Outcome

Chosen option: "Publish numbered prereleases through a separate binary beta
formula", because it reuses the native archive matrix and gives users an
explicit choice of release channel.

`internal/release.ParseTag` accepts stable `vMAJOR.MINOR.PATCH` tags and
`vMAJOR.MINOR.PATCH-beta.N`. Unknown suffixes fail. The metadata command and
formula renderer share that parser, and manifests must match the tag version.
Beta tags create GitHub prereleases with `--latest=false` and generate
`Formula/codelima-beta.rb` with class `CodelimaBeta`. This formula is keg-only;
users select its CLI explicitly, and its renderer remains adjacent in libexec.
The existing stable formula and default runtime home remain unchanged.

Every native build checks out the requested tag and runs verify, race,
integration and package checks before publication. Manual QA gates remain
required. Releases are created with all assets and an existing verified tag;
published releases are never silently overwritten. Tap updates stage the
selected file before inspecting the diff, including first-time formula creation.

### Positive Consequences

* Beta publication cannot select the stable formula or Latest release.
* Installing the beta preserves the current command selection.
* Formula URLs, versions and binaries come from the same release tag.

### Negative Consequences

* Beta users must select the beta path and manage daemon version transitions.
* Release builds take longer because each native target runs automated checks.
* Failed publication recovery needs inspection; existing releases are not clobbered.

## Pros and Cons of the Options

### Publish beta versions through the existing stable formula

* Good, because it needs no second formula.
* Bad, because normal upgrades would opt stable users into a beta.

### Build the branch from source through Homebrew HEAD

* Good, because users can follow the branch immediately.
* Bad, because installations rebuild the patched native dependency and are not
  tied to a qualified, reproducible release tag.

### Publish numbered prereleases through a separate binary beta formula

* Good, because existing archives and manifests support fast installations.
* Good, because stable and beta users choose their channel independently.
* Bad, because two formula names need documentation and verification.

## Links

* Refines [ADR 4](package_binary_releases_and_homebrew_tap_4.md).
* [Build and release procedure](../BUILD.md).
* [Homebrew Formula Cookbook](https://docs.brew.sh/Formula-Cookbook).
* [GitHub release creation flags](https://cli.github.com/manual/gh_release_create).
