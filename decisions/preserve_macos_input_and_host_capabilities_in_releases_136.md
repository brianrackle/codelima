# Preserve macOS Input And Host Capabilities In Releases

## Context and Problem Statement

The first libghostty beta's native macOS verification failed because Alt+x
encoded to no bytes and an overly broad cgo guard rejected the existing
Virtualization.framework host-capability adapter. Inspection also found that
release packaging disabled cgo for the macOS CLI, selecting the adapter's false
stub and disabling automatic nested virtualization detection.

## Decision Drivers

* Preserve the Alt modifier already decoded by the frontend.
* Preserve native macOS capability detection in packaged builds.
* Keep Ghostty native execution isolated in its worker.

## Considered Options

* Skip the failing native checks for the beta.
* Remove macOS host capability detection and accept upstream key defaults.
* Set explicit frontend key policy and qualify the precise native boundary.

## Decision Outcome

Chosen option: "Set explicit frontend key policy and qualify the precise native
boundary", because the failures expose real platform differences that should
be corrected before publishing.

Immediately before native key encoding, the bridge sets
`GHOSTTY_KEY_ENCODER_OPT_MACOS_OPTION_AS_ALT` to `GHOSTTY_OPTION_AS_ALT_TRUE`.
Vaxis has already interpreted the host key as Alt. The upstream terminal-option
refresh resets this setting, so setting it only at encoder creation is
insufficient. Terminal protocol modes still come from the native terminal.

Linux CLI archives retain `CGO_ENABLED=0`. macOS CLI archives use cgo for the
existing Foundation/Virtualization.framework adapter from ADR 101. The worker
retains Ghostty cgo on both platforms. Dependency checks still reject Ghostty
in the CLI graph; cross-platform source checks allow only the named macOS
host adapter in the application package and no cgo in portable data packages.
Package integration checks inspect each binary's actual cgo build setting.

### Positive Consequences

* Alt input behaves consistently after repeated terminal-mode refreshes.
* Packaged macOS binaries retain automatic nested virtualization detection.
* Checks distinguish the host capability adapter from the renderer boundary.

### Negative Consequences

* macOS CLI builds require the system Apple frameworks and cgo toolchain.
* Native macOS tests remain necessary to qualify platform-specific input.

## Pros and Cons of the Options

### Skip the failing native checks

* Good, because it shortens beta publication.
* Bad, because it ships the observed input defect without a fix.

### Remove host capability detection and accept upstream key defaults

* Good, because the CLI would remain entirely Go on macOS.
* Bad, because it removes existing behavior and loses decoded Alt input.

### Set explicit frontend policy and qualify the precise native boundary

* Good, because it preserves existing behavior and native renderer isolation.
* Bad, because platform-specific source allowlists must be maintained.

## Links

* Refines [ADR 132](adopt_static_libghostty_vt_with_bounded_worker_contracts_132.md).
* Preserves [ADR 101](automatically_enable_supported_nested_virtualization_101.md).
* [First beta workflow](https://github.com/brianrackle/codelima/actions/runs/34250335019).
