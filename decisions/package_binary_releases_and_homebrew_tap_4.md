# Package Binary Releases And Homebrew Tap

## Context and Problem Statement

CodeLima had local developer bootstrap and build targets, but no repeatable distribution flow for end users. The project now ships a Ghostty shared library alongside the Go binary, so a release process needs to package both runtime pieces together and keep Homebrew metadata in sync with published assets.

## Decision Drivers

* End-user installs should not need to run the repo's developer bootstrap flow.
* Release metadata should be generated from built artifacts instead of hand-maintained checksums.
* The same package layout should work for manual tarball installs and Homebrew distribution.
* Release automation should be runnable locally and in GitHub Actions without separate logic.

## Considered Options

* Keep distribution manual and document hand-built tarballs plus manual Homebrew formula edits.
* Publish source-only releases and make the Homebrew formula build Ghostty and CodeLima from source at install time.
* Publish per-platform binary archives with generated manifests and derive the Homebrew formula from those manifests.

## Decision Outcome

Chosen option: "Publish per-platform binary archives with generated manifests and derive the Homebrew formula from those manifests", because it keeps installation fast for users, packages the Ghostty runtime dependency explicitly, and lets GitHub Actions update Homebrew from release artifacts instead of from hand-edited checksums.

### Positive Consequences

* Release archives now have a stable runtime layout: wrapper script, real binary, and packaged Ghostty library.
* The Homebrew formula is generated from artifact manifests, which reduces checksum drift and manual release steps.
* Local `make package` and `make package-formula` targets match the GitHub Actions release flow.

### Negative Consequences

* Release automation now has to build and publish per-platform archives before Homebrew can be updated.
* The repository depends on a separate tap repository and token for automatic Homebrew updates.
* Release hardening such as code signing and notarization remains a separate follow-up concern.

## Pros and Cons of the Options

### Keep distribution manual and document hand-built tarballs plus manual Homebrew formula edits

Continue releasing by hand with ad hoc tarball creation and direct tap edits.

* Good, because it adds almost no automation complexity.
* Good, because it avoids introducing release-specific tooling.
* Bad, because checksums and URLs are easy to drift or forget.
* Bad, because the Ghostty runtime layout would be repeated manually for every release.

### Publish source-only releases and make the Homebrew formula build Ghostty and CodeLima from source at install time

Use GitHub source archives and make Homebrew compile both the Go binary and Ghostty on the target machine.

* Good, because it avoids publishing per-platform binaries.
* Good, because the tap only tracks one source version per release.
* Bad, because Homebrew installs become slower and more toolchain-heavy.
* Bad, because the formula would need to rebuild the patched Ghostty library during install.

### Publish per-platform binary archives with generated manifests and derive the Homebrew formula from those manifests

Build one archive per supported OS and architecture, emit a manifest alongside it, and render the formula from the manifests.

* Good, because users install prebuilt artifacts that already include the matching Ghostty library.
* Good, because the release workflow can update the tap from generated metadata instead of hand-authored checksums.
* Bad, because the release pipeline now needs native builds for each supported platform.
* Bad, because missing or delayed assets block the tap update until the full matrix completes.
