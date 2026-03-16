# Platform-Scoped Local Toolchains

## Context and Problem Statement

CodeLima bootstraps its development toolchain locally under `.tooling` so contributors do not need system Go or Zig installs. That repository is also used from inside Linux guest nodes, and a shared `.tooling` path allowed a host-installed macOS binary to be reused accidentally inside the guest, which broke `make init`.

## Decision Drivers

* `make init` must be self-bootstrapping on both host and guest platforms.
* A shared repository checkout must work from multiple operating systems without manual cleanup.
* The fix should preserve the existing `make init` developer entrypoint.

## Considered Options

* Keep a single shared `.tooling` directory and add binary validation/reinstall checks.
* Scope `.tooling` by platform and install separate toolchains per OS and architecture.
* Require separate repository checkouts for host and guest development.

## Decision Outcome

Chosen option: "Scope `.tooling` by platform and install separate toolchains per OS and architecture", because it prevents host/guest collisions up front while keeping `make init` as the single documented bootstrap command.

### Positive Consequences

* Host and guest `make init` runs no longer fight over the same Go, Zig, lint, and Ghostty artifacts.
* Existing bootstrap scripts can keep installing local toolchains without needing system dependencies.
* The failure mode is prevented structurally instead of depending on stale-binary detection.

### Negative Consequences

* Multiple platforms now download and cache their own copies of the toolchain artifacts.
* Existing stale binaries under the old shared `.tooling` paths are left behind until manually removed.

## Pros and Cons of the Options

### Keep a single shared `.tooling` directory and add binary validation/reinstall checks

Reuse the same install location and try to detect when the existing binary belongs to the wrong platform.

* Good, because it minimizes disk usage.
* Good, because it preserves the old directory layout.
* Bad, because alternating between host and guest would keep overwriting or invalidating each other's tools.
* Bad, because every tool installer would need reliable wrong-platform detection.

### Scope `.tooling` by platform and install separate toolchains per OS and architecture

Derive the local tool root from `uname -s` and `uname -m`, for example `.tooling/darwin-arm64` or `.tooling/linux-aarch64`.

* Good, because each platform gets a compatible binary set.
* Good, because the same checkout can be used from both host and guest without manual cleanup.
* Bad, because it duplicates downloads and caches across platforms.

### Require separate repository checkouts for host and guest development

Document that the repo should not be shared between host and guest development environments.

* Good, because it avoids any shared-cache/tooling collision.
* Good, because it needs no Makefile or script changes.
* Bad, because it pushes complexity onto every contributor.
* Bad, because it conflicts with the current CodeLima workflow of using the same project path inside nodes.
