# Provision pkg-config as a managed build tool

## Context and Problem Statement

ADR 132 changed the worker to static libghostty-vt linkage using upstream
pkg-config metadata. The native archive built successfully on a macOS arm64
host, but the following cgo worker build failed because `pkg-config` was absent
from PATH. `make init` provisioned the compiler and library but omitted the
metadata resolver. Depending on a preinstalled Linux package masked this gap.

## Decision Drivers

- `make build` must provision its build tools without a separate Homebrew step.
- Keep exact upstream metadata interpretation rather than a custom `.pc` parser.
- Keep native archive identity and release contents unchanged.
- Use only project-local caches, verified downloads and atomic publication.

## Considered Options

- Require operators to install pkg-config with a host package manager.
- Replace upstream pkg-config metadata with hard-coded flags or a partial parser.
- Build and select an exact project-managed pkgconf executable.

## Decision Outcome

Chosen option: build and select project-managed pkgconf `2.5.1`. Its upstream
release archive is pinned by SHA-256
`cd05c9589b9f86ecf044c10a2269822bc9eb001eced2582cfffd658b0a50c243`.
The release includes generated configure files, so bootstrap needs no installed
Autotools, Meson or existing pkg-config. Managed Zig builds the executable with
libpkgconf linked statically. Small relative compiler/archive shims preserve
paths containing spaces across Autoconf/libtool command parsing. No system
install is performed; the executable and upstream license are copied into an
immutable version/build-stamped prefix under `.tooling/<platform>/pkgconf`.

The installer holds the existing kernel installer lock, verifies cached and
downloaded source before extraction, validates the resulting version and
atomically publishes `.tooling/<platform>/bin/pkg-config`. Failed attempts
clean their private staging tree and cannot publish an incomplete tool.

Make provisions the tool after Zig and exports its absolute `PKG_CONFIG` path
to cgo. Native bridge/benchmark recipes invoke that same path. Direct release
packaging validates the existing native profile before provisioning and
selecting the same tool. The change does not rebuild or alter a matching
Ghostty archive and does not add pkgconf to runtime release packages.

### Positive Consequences

- Hosts without pkg-config use the documented `make build` workflow.
- Builds do not silently select a different resolver from a developer's PATH.
- Existing cached native archives remain reusable.
- Installer tests cover absent tools, corrupt caches, failed publication,
  paths with spaces, concurrency and reuse.

### Negative Consequences

- Initial setup downloads and compiles one additional small upstream tool.
- Its source checksum/version and installer contract need maintenance.
- Native macOS execution still requires host verification; Linux tests cannot
  establish a macOS build pass on their own.

## Pros and Cons of the Options

Host package managers provide a quick local workaround but leave sandbox
setup incomplete and build inputs variable. Hard-coded flags or a custom
resolver remove a dependency but duplicate the upstream metadata contract.
A pinned real resolver adds bootstrap work while preserving correct semantics
and the existing project-managed toolchain policy.

## Links

- [Upstream pkgconf build and symlink instructions](https://github.com/pkgconf/pkgconf/blob/pkgconf-2.5.1/README.md)
- [Upstream release archive](https://distfiles.ariadne.space/pkgconf/pkgconf-2.5.1.tar.xz)
- [Static native worker decision](adopt_static_libghostty_vt_with_bounded_worker_contracts_132.md)
- [Build guide](../BUILD.md)
