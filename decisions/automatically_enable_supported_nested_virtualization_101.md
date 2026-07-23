# Automatically Enable Supported Nested Virtualization

## Context and Problem Statement

CodeLima nodes may themselves need to run KVM-backed workloads, but Lima's nested virtualization support is available only on compatible macOS arm64 hosts. Requiring every node or configuration to opt in would duplicate a host capability decision, while unconditionally enabling the Lima setting would make unsupported hosts fail. How should CodeLima expose nested virtualization wherever the host can provide it?

## Decision Drivers

* Every node on a capable host should receive the same behavior without per-node configuration.
* Unsupported hosts must continue to create and start nodes normally.
* Nodes created before this behavior was added should gain it on their next start.
* Capability detection must use the authoritative Apple virtualization API instead of hardware-name or OS-version guesses.
* Linux QEMU/KVM hosts must not accidentally request an additional nesting layer through a macOS-specific Lima option.

## Considered Options

* Detect native macOS arm64 support and apply nested virtualization automatically during both create and start.
* Add a user-managed nested-virtualization field to each configuration.
* Always emit Lima's nested-virtualization setting and let Lima reject unsupported hosts.

## Decision Outcome

Chosen option: "Detect native macOS arm64 support and apply nested virtualization automatically during both create and start", because host capability, not workload sizing, determines whether Lima can provide the feature.

On Darwin arm64 with cgo, a small platform adapter calls `VZGenericPlatformConfiguration.isNestedVirtualizationSupported` through a runtime selector. Runtime lookup preserves compatibility with older macOS SDKs and returns false when the API or capability is unavailable. Non-Darwin and non-cgo builds use a false stub.

For a supported host, CodeLima writes `nestedVirtualization: true` into every newly rendered `instance.lima.yaml` and passes `--nested-virt` on every `limactl start`. The start-time flag gives existing nodes the same behavior without rewriting durable node metadata or requiring recreation. Unsupported hosts render `false` and omit the flag. `codelima doctor` reports whether the macOS capability is enabled automatically or unavailable.

### Positive Consequences

* All nodes receive nested virtualization consistently when the host supports it.
* Existing nodes gain support on their next start.
* Unsupported hosts retain their current lifecycle behavior.
* The decision is based on Apple's runtime capability result rather than inferred model lists.
* Users do not need a per-configuration switch or raw Lima command override.

### Negative Consequences

* The macOS build gains a small Objective-C/cgo bridge and links Virtualization.framework.
* Native release qualification needs both a supported Apple silicon host and an unsupported macOS case.
* Linux does not use this mechanism even when a particular QEMU/KVM topology could be configured for deeper nesting.

## Pros and Cons of the Options

### Detect support and enable during create and start

* Good, because all nodes follow one host capability decision.
* Good, because the start flag upgrades existing nodes without metadata migration.
* Good, because unsupported hosts remain valid.
* Bad, because platform-native detection and native-host qualification are required.

### Add a configuration field

* Good, because operators could disable the behavior per workload.
* Bad, because every configuration would duplicate a host-level fact.
* Bad, because existing nodes would need a metadata migration or recreation.
* Bad, because selecting true on an unsupported host would defer failure until runtime.

### Enable unconditionally

* Good, because the implementation would be small.
* Bad, because Lima rejects nested virtualization on unsupported hosts.
* Bad, because ordinary node lifecycle would become hardware-dependent and fragile.

## Links

* Refines [Return to Lima as the sole runtime with schema v4](return_to_lima_as_the_sole_runtime_92.md).
