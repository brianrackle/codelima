# Platform-Scoped Development Binaries

## Context and Problem Statement

CodeLima can be developed from the same repository checkout on a macOS host and inside a Linux Lima guest. The toolchain is already platform-scoped under `.tooling/<os>-<arch>`, but `make build` wrote every native executable to `bin/codelima`, so whichever platform built last could overwrite the other platform's binary. This caused long-lived TUI processes to fail when opening a node tab because node tabs re-exec `codelima shell <node>` and could hit a binary built for the wrong OS or architecture.

## Decision Drivers

* A shared host/guest checkout must support builds from both platforms without binary clobbering.
* Existing local commands and QA docs should keep a short `./bin/codelima` path for convenience.
* TUI node tab re-exec should not follow a compatibility link that may be repointed after the TUI starts.
* Release archives should keep their existing end-user layout.

## Considered Options

* Keep a single `bin/codelima` build output and improve the error message only.
* Scope development build output by platform and keep `bin/codelima` as a compatibility symlink.
* Require separate repository checkouts for host and guest development.

## Decision Outcome

Chosen option: "Scope development build output by platform and keep `bin/codelima` as a compatibility symlink", because it prevents host and guest builds from overwriting each other while preserving the documented short path for manual commands.

### Positive Consequences

* macOS and Linux builds can coexist under `bin/<os>-<arch>/codelima`.
* `make run`, `make tui`, smoke tests, and packaging can consume the platform-scoped binary directly.
* `./bin/codelima` remains usable for docs and one-off local commands.
* TUI node tabs cache the resolved executable path when the session store is created, so later symlink changes do not affect already-running TUI processes.

### Negative Consequences

* Each platform now has its own development binary on disk.
* The compatibility symlink points to the platform that last ran `make build`, so automation should prefer make targets or the platform-scoped binary path.

## Pros and Cons of the Options

### Keep a single `bin/codelima` build output and improve the error message only

Leave the build output unchanged and make node-tab failures clearer when the kernel returns `exec format error`.

* Good, because it minimizes implementation scope.
* Good, because no documented paths change.
* Bad, because the host and guest can still overwrite each other's binary.
* Bad, because the TUI would still fail at runtime after an opposite-platform build.

### Scope development build output by platform and keep `bin/codelima` as a compatibility symlink

Build native development binaries under `bin/<os>-<arch>/codelima`, refresh `bin/codelima` as a short symlink, and make long-lived re-exec paths use the resolved platform binary.

* Good, because it structurally prevents cross-platform binary clobbering.
* Good, because it follows the existing `.tooling/<os>-<arch>` pattern.
* Good, because existing docs and manual commands can keep using `./bin/codelima`.
* Bad, because callers that care about platform stability need to avoid relying on the moving symlink.

### Require separate repository checkouts for host and guest development

Document that contributors should not share one checkout across host and guest platforms.

* Good, because each checkout can keep a simple `bin/codelima` path.
* Good, because it needs little build-system logic.
* Bad, because it conflicts with CodeLima's shared-workspace development workflow.
* Bad, because it shifts the burden to every contributor and agent.

## Links

* Refines [Platform-Scoped Local Toolchains](platform_scoped_toolchains_2.md)
* Related to [Bundled Runtime Wrapper](package_binary_releases_and_homebrew_tap_4.md)
