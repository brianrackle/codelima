# Use the Microsandbox Go SDK without a CLI fallback

Status: Superseded by [ADR 92](return_to_lima_as_the_sole_runtime_92.md)

## Context and Problem Statement

CodeLima currently renders configurable shell commands and spawns `msb` for every sandbox operation. The official Microsandbox 0.6.6 Go SDK provides typed lifecycle, exec, filesystem, snapshot, and SSH APIs. CodeLima requires detached VM ownership, daemon-owned interactive terminals, and dynamic node-hostname forwarding. The SDK's SSH server serves only over the current process's stdin/stdout, and its client does not expose raw `direct-tcpip` dialing.

## Decision Drivers

* Remove stringly typed command and JSON parsing boundaries.
* Never silently fall back to a second runtime path.
* Preserve detached lifecycle and the existing terminal/forwarding UX.
* Keep external runtime errors typed and testable.
* Avoid mutating global Microsandbox SSH authorization state.

## Considered Options

* Keep the CLI implementation.
* Prefer SDK calls with CLI fallback for gaps and overrides.
* Use the SDK for all operations and launch a CodeLima-owned SDK SSH helper for stdio transport.
* Fork the SDK immediately to expose raw Go streams or TCP dialing.

## Decision Outcome

Chosen option: use the SDK for all runtime operations with no CodeLima CLI fallback. Dynamic forwarding launches the current CodeLima executable in a hidden helper mode. The helper connects through the SDK, prepares an SSH server using CodeLima's authorized-key file, and calls `ServeConnection` on its own stdin/stdout. The parent retains the existing Go SSH client and reverse proxy.

Before reporting or validating runtime versions, CodeLima calls the SDK's `EnsureInstalled` API. This installs the SDK-pinned internal runtime executable and `libkrunfw` bundle under `~/.microsandbox` when absent; CodeLima still never discovers or executes that binary itself.

Lifecycle `runtime_commands` overrides are removed as a supported surface. Exact old built-ins migrate away; customized overrides fail explicitly. Guest bootstrap and workspace preparation commands remain data, not runtime transport overrides.

Because SDK 0.6.6 has no bundled macOS Intel FFI library, `darwin/amd64` is removed from the supported release matrix rather than using a CLI fallback.

### Positive Consequences

* Runtime operations use typed APIs and errors.
* No CodeLima code invokes `msb` or parses CLI output.
* Interactive attach and filesystem transfer avoid intermediate shell processes.
* Forwarding keys are scoped directly to helper server instances.
* One runtime implementation exists on every supported platform.
* A fresh host obtains the exact support bundle pinned by the SDK instead of depending on a separately installed CLI version.

### Negative Consequences

* The embedded FFI materially increases binary and module size.
* The SDK and detached runtime still require Microsandbox's internal runtime executable and `libkrunfw` on disk.
* The first runtime dependency check may perform a network download and mutate the shared SDK cache under `~/.microsandbox`.
* Dynamic forwarding retains one child process per node, now a CodeLima helper rather than `msb`.
* macOS Intel and arbitrary CLI command overrides are no longer supported.
* SDK attach and live-update/helper interaction require a new native qualification matrix.

## Links

* [Microsandbox Go SDK Migration Specification](../plans/MICROSANDBOX_GO_SDK_MIGRATION_SPEC.md)
* Replaces the CLI client portion of [ADR 55](replace_lima_with_microsandbox_as_sole_runtime_55.md)
* Preserves the dynamic routing architecture in [ADR 70](daemon_dynamic_node_hostname_forwarding_70.md)
