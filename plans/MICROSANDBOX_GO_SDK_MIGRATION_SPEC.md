# Microsandbox Go SDK Migration Specification

Status: Implemented and locally qualified (Go, Microsandbox SDK 0.6.6); native release qualification remains tracked in `TODO.md`
Purpose: Remove every CodeLima invocation of the `msb` CLI while preserving node lifecycle, terminal, clone, copy, and dynamic `{node}.localhost` behavior through the official Go SDK.

## 1. Problem Statement

CodeLima currently implements `SandboxClient` by rendering shell command templates and spawning `msb`. This creates a stringly typed protocol, duplicates Microsandbox validation and error parsing, and makes ordinary operations depend on shell quoting and CLI output stability. The official Go SDK 0.6.6 exposes typed lifecycle, filesystem, exec, PTY, snapshot, and SSH-server APIs over its embedded FFI library.

The SDK does not expose raw SSH `direct-tcpip` dialing or an SSH server over caller-provided Go streams. `SSHServer.ServeConnection` is fixed to the serving process's stdin/stdout. Dynamic forwarding therefore requires a CodeLima-owned helper process, but must not invoke `msb` directly.

## 2. Goals and Non-Goals

### 2.1 Goals

* Use `github.com/superradcompany/microsandbox/sdk/go` pinned exactly to `v0.6.6` for all runtime operations.
* Prohibit `exec.Command` or shell templates that invoke `msb` anywhere in CodeLima.
* Preserve detached sandbox lifetime across CodeLima CLI and daemon process exit.
* Preserve interactive PTY behavior, workdir selection, exit status, signal handling, and resize behavior.
* Preserve copied and mounted workspace modes, explicit ports, network policy, snapshot clone, and typed runtime reconciliation.
* Preserve dynamic HTTP/WebSocket forwarding by spawning the current CodeLima executable in a hidden SDK SSH-server mode.
* Translate SDK errors and non-zero guest exits into CodeLima's stable application error categories.
* Keep Microsandbox runtime installation explicit; CodeLima may validate the SDK/runtime but must not silently download runtime components during ordinary commands.

### 2.2 Non-Goals

* Supporting arbitrary `msb` command-template overrides after the SDK migration.
* Providing a CLI fallback on unsupported SDK platforms.
* Eliminating the Microsandbox runtime executable or `libkrunfw` used internally by the SDK for detached VM ownership.
* Extending the upstream SDK with raw TCP dialing in this change.
* Supporting macOS Intel while SDK 0.6.6 lacks a bundled `darwin/amd64` FFI library.

## 3. Main Components

* `SDKSandboxClient`: implements `SandboxClient` with typed SDK calls.
* `sdkRuntime`: narrow wrapper around package-level SDK functions, providing a fakeable test seam.
* `sdkSandboxHandle` and `sdkSandbox`: wrappers for persisted and live sandbox handles with explicit detach/close ownership rules.
* `sdk-ssh-serve`: hidden CodeLima invocation that connects to one running sandbox, prepares an SDK SSH server with CodeLima's authorized-key file, and serves one transport on stdin/stdout.
* `SDKSandboxClient.OpenSSHTransport`: daemon-side launcher for the helper. It replaces the former CLI transport and global authorization mutation.
* Existing `DynamicForwarder`: retains Go SSH discovery and `direct-tcpip` routing unchanged above the transport seam.

## 4. Runtime Contract

### 4.1 Version and Installation

* `Version` must return `microsandbox.SDKVersion()` only when `RuntimeVersion()` matches exactly.
* Required SDK and runtime versions are `0.6.6`.
* CodeLima must not call `EnsureInstalled` automatically from normal operations.
* Missing FFI/runtime components must surface as `DependencyUnavailable` with an installation action.
* `MSB_HOME` remains authoritative for sandbox state. The SDK's internal runtime executable remains an implementation dependency.

### 4.2 Create

`Create` must translate persisted node metadata to SDK options:

* image -> `WithImage`;
* 2 CPUs and 4096 MiB -> `WithCPUs` and `WithMemory` until resource metadata becomes configurable;
* `/bin/bash` -> `WithShell`;
* real PID 1 -> `WithInit(Init.Auto())`;
* mounted workspace -> `WithMounts` and a writable bind mount;
* explicit TCP ports -> `WithPorts`;
* network default/rules -> `WithNetwork`;
* detached ownership -> `WithDetached`.

The SDK creates a running VM. CodeLima must stop it and release the handle before returning so `node start` remains the sole bootstrap transition. Failure cleanup must kill/remove partially created state.

### 4.3 Start and Stop

* `Start` must call `StartDetached`, then `Detach`; calling `Close` on an owning detached handle would stop the VM and is forbidden on the success path.
* `Stop` must use the persisted handle's bounded graceful stop.
* `Delete` must be force-equivalent: stop or kill a running sandbox, then remove its persisted state. Missing sandboxes remain idempotent where the Service contract expects it.

### 4.4 List and Reconciliation

`ListSandboxes` supplies typed names and statuses. CodeLima must normalize status strings to lower case and must not parse JSON CLI output.

### 4.5 Copy and Non-Interactive Exec

* `CopyToGuest` must connect to the running sandbox and use `FS().CopyFromHost`. Because SDK 0.6.6 copies one host file at a time, directories are walked explicitly, guest directories are created with `FS().Mkdir`, and symbolic links are recreated with a typed guest exec.
* Non-interactive `Shell` must use `Exec` or `ExecStream` with `WithExecCwd`.
* Caller stdin requires `ExecStream` plus `WithExecStdinPipe`; stdout/stderr events must be relayed to the supplied streams without duplication.
* A non-zero guest exit is an `ExternalCommandFailed` error even though the SDK represents it as a successful Go call with an exit code.
* Every live SDK handle and stream handle must be closed exactly once.

### 4.6 Interactive PTY

Interactive `Shell` must connect to the running sandbox and call SDK `Attach`. Because `Attach` bridges the current process terminal directly:

* supplied streams must be the process stdin/stdout/stderr used by the shell process;
* CodeLima must reject interactive use without a real TTY;
* workdir must be implemented with a shell wrapper that changes directory and `exec`s the requested login command;
* the full E1 PTY, resize, signal, raw-mode, job-control, and cleanup matrix is required before completion.

### 4.7 Clone

Clone must keep the existing stopped-source transaction:

1. snapshot the stopped source by name;
2. create the target detached from `WithSnapshot` plus target mounts, ports, network, resources, shell, and init;
3. stop and release the target so it returns created/stopped;
4. remove the temporary snapshot on every terminal path;
5. preserve Service-level restoration of a previously running source.

## 5. Dynamic Forwarding Helper Contract

The parent invokes the current executable using a hidden command equivalent to:

```text
codelima __sdk-ssh-serve \
  --sandbox <sandbox-name> \
  --authorized-keys <public-key-file>
```

Rules:

* The helper must use only SDK APIs: `GetSandbox`, `Connect`, `SSH().PrepareServer`, and `ServeConnection`.
* stdin/stdout are exclusively the SSH byte stream. Diagnostics go to stderr.
* The helper must not initialize the CodeLima daemon, metadata layout, or terminal UI.
* The sandbox name and key path are explicit arguments and validated before SDK calls.
* Parent context cancellation must terminate the helper and close pipes.
* `SandboxSSHRuntime.Prepare` becomes local key-file preparation only; no global Microsandbox authorization mutation remains.
* The existing parent-side Go SSH handshake, discovery, and `direct-tcpip` multiplexing remain unchanged.

## 6. Configuration and Compatibility

Fresh configuration must not contain lifecycle `runtime_commands` fields. `bootstrap` and `workspace_seed_prepare` are guest commands and may remain temporarily under the existing key until a later schema cleanup.

Compatibility rules:

* An exact copy of the previous built-in CLI templates is automatically removed during config repair.
* Any user-customized lifecycle/list/copy/shell/clone command override is rejected with `PreconditionFailed`; it is never ignored and never executed as fallback.
* Project and node metadata with customized CLI overrides receive the same actionable rejection.
* Documentation must remove CLI-template customization as a supported capability.

## 7. Platform and Packaging Contract

SDK 0.6.6 supports:

* `darwin/arm64`;
* `linux/amd64`;
* `linux/arm64`.

CodeLima release and CI matrices must remove `darwin/amd64`. Unsupported platforms fail at build qualification or startup; they must not select a CLI fallback.

The embedded FFI increases binary size and extracts a versioned library below the user's Microsandbox installation directory. Packaging tests must assert the supported-platform matrix and record artifact size changes.

## 8. Error and Recovery Contract

* SDK typed not-found errors -> `NotFound`.
* invalid config/arguments -> `InvalidArgument` or `PreconditionFailed` according to the existing operation contract.
* unavailable FFI/runtime -> `DependencyUnavailable`.
* timeout/cancellation -> preserve context cancellation when caller initiated; otherwise `ExternalCommandFailed`.
* filesystem, snapshot, and internal runtime failures -> `ExternalCommandFailed` with sandbox/action fields and no secret values.
* Partial create/clone must retain enough metadata for `node cleanup-incomplete` and must make best-effort SDK teardown.

## 9. Test and Validation Matrix

Automated tests must cover:

* option translation for image, init, resources, mount, ports, and network;
* detached create/start ownership and cleanup order;
* typed list/status mapping and SDK error mapping;
* non-interactive exit codes, stdout/stderr, cwd, stdin, and cancellation;
* interactive attach invocation and workdir wrapper;
* copy and snapshot clone cleanup;
* rejection and exact-default migration of CLI command templates;
* hidden helper argument validation, SDK call order, cancellation, and no stdout diagnostics;
* parent helper process command construction and error propagation;
* existing dynamic forwarding HTTP/Upgrade/race tests;
* supported release-platform matrix.

Manual verification must run the complete `QA.md` flow plus:

1. prove process inspection shows no CodeLima-spawned `msb` lifecycle, exec, copy, snapshot, or SSH process;
2. exercise SDK Attach in direct CLI and daemon-owned PTYs;
3. verify two same-port `{node}.localhost` routes through SDK helper processes;
4. verify daemon restart/live update kills and reconstructs helpers without losing terminal continuity;
5. verify all helper, sandbox, snapshot, FFI-probe, and downloaded test state is removed.

## 10. Implementation Checklist

* [x] Add ADR and exact SDK dependency.
* [x] Add fakeable SDK runtime/handle seams.
* [x] Implement typed lifecycle/list/error mapping.
* [x] Implement recursive filesystem copy and exec/streaming.
* [x] Implement SDK interactive attach and run a real nested-host PTY check.
* [x] Implement SDK snapshot clone.
* [x] Implement hidden SDK SSH helper and daemon launcher.
* [x] Remove all direct `msb` process execution.
* [x] Reject/migrate CLI command templates.
* [x] Update supported platform and packaging matrices.
* [x] Update README, BUILD, QA, PATTERNS, ROADMAP, TODO, and progress.
* [x] Run automated and real-runtime verification; clean artifacts. Native release qualification remains explicitly deferred in `TODO.md`.
