# Return to Lima as the sole runtime with schema v4

Status: Accepted

## Context and Problem Statement

Microsandbox 0.6.6 introduced a persistent host runtime process, an opaque
libkrun firmware/runtime bundle, recurring idle CPU usage in an Ubuntu-image
node, and integration limitations that CodeLima could not repair. CodeLima
needs a runtime whose lifecycle, logs, VM configuration, SSH transport, and
kernel are inspectable without changing the existing directory-bound node,
daemon terminal, or hostname-forwarding product behavior.

## Decision Drivers

* Use an upstream distribution guest and kernel with ordinary systemd/package behavior.
* Keep runtime lifecycle and configuration reproducible with documented host commands.
* Preserve directory-bound nodes, mounted/copy workspaces, terminals, clone behavior, and forwarding.
* Remove the Microsandbox SDK, helper process, downloader, and libkrunfw dependency.
* Prevent the two-second UI refresh path from spawning a runtime process.
* Keep one supported backend and one current metadata schema.

## Considered Options

* Continue using the Microsandbox Go SDK and wait for upstream fixes.
* Build a new libkrun integration and maintain a guest kernel/firmware bundle.
* Support both Microsandbox and Lima through a provider registry.
* Return to Lima 2.x as the sole runtime with a clean schema-v4 home.

## Decision Outcome

Chosen option: "Return to Lima 2.x as the sole runtime with a clean schema-v4
home", because Lima exposes the VM definition, full-distribution guest,
instance logs, SSH configuration, and host process model while meeting the
existing CodeLima lifecycle and forwarding contracts.

Schema v4 retains the public `image` and `sandbox_name` field names. `image`
now contains a Lima template locator and `sandbox_name` contains the Lima
instance name. Schema-v3 homes are rejected without mutation because their
Microsandbox disks are not safely convertible. The network-policy surface is
removed; Lima guests use ordinary outbound access.

CodeLima renders a private per-node Lima YAML file, disables inherited mounts,
containerd, and Lima automatic port publication, and uses VZ/VirtioFS on macOS
or QEMU/KVM/9p on Linux. Guest commands continue to run as root through Lima's
passwordless `sudo` boundary. Running sources are stopped before `limactl
clone` and restored afterward.

The daemon owns one `limactl watch --json` process and serves list refreshes
from an observation cache. Dynamic forwarding uses a persistent Go SSH client
configured only from the Lima-owned instance SSH file; it does not launch a
per-node CodeLima or runtime helper.

### Positive Consequences

* Runtime configuration and failures are inspectable with ordinary Lima tooling.
* Nodes use an upstream Ubuntu distribution image and kernel.
* CodeLima no longer embeds cgo runtime bindings or installs libkrunfw.
* TUI refresh does not create a recurring `limactl list` process.
* The existing terminal, workspace, explicit-port, and HTTP/WebSocket behavior is retained.

### Negative Consequences

* Running VMs have visible Lima hostagent and VZ/QEMU processes.
* Existing schema-v3 nodes must be recreated in a fresh schema-v4 home.
* Outbound domain allowlisting is no longer available.
* CodeLima depends on a compatible external Lima 2.x installation.
* Native VZ, sleep/wake, and interactive Ghostty behavior require macOS release qualification.

## Pros and Cons of the Options

### Continue using the Microsandbox Go SDK

* Good, because it minimizes implementation work.
* Bad, because CodeLima cannot fix the observed idle behavior or opaque runtime limitations.
* Bad, because it retains the SDK helper/runtime download and libkrunfw bundle.

### Build a libkrun integration

* Good, because CodeLima could control its API and process ownership.
* Bad, because CodeLima would own kernel, firmware, image, networking, mount, SSH, and platform qualification work.
* Bad, because it recreates substantial runtime infrastructure without preserving existing VM disks.

### Support both runtimes

* Good, because users could choose per workload.
* Bad, because it doubles lifecycle, forwarding, packaging, recovery, and native-test obligations.
* Bad, because no safe disk migration exists between the backends.

### Return to Lima only

* Good, because Lima already supplies the full VM lifecycle and supported VZ/QEMU integrations.
* Good, because a CLI boundary is diagnosable and replaceable without embedding a VMM SDK.
* Bad, because hostagent and VM-driver processes remain visible while a node runs.

## Links

* Supersedes [ADR 55](replace_lima_with_microsandbox_as_sole_runtime_55.md).
* Supersedes [ADR 71](use_microsandbox_go_sdk_without_cli_fallback_71.md).
* Preserves the routing decision in [ADR 70](daemon_dynamic_node_hostname_forwarding_70.md).
* Implementation plan: [LIMA_PLAN.md](../LIMA_PLAN.md).
* Qualification evidence: [Lima return spike](../plans/spike-notes/LIMA_RETURN_SPIKE.md).
