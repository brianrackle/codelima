# Replace Lima with microsandbox as the sole runtime backend

Status: Accepted — microsandbox 0.6.6 passed the complete local Phase 0 gate

## Context and Problem Statement

CodeLima has used Lima as its only VM backend since Milestone 1, and the data model is Lima-shaped in core places (`lima_instance_name`, `lima_commands`, `default_lima_template`, `lima_home`). An evaluation of libkrun-based microVM runtimes (smolvm, microsandbox) found that microsandbox offers persistent detached sandboxes, interactive PTY access (`msb ssh`, `msb exec -t`), read-write host mounts, explicit port publishing, per-sandbox egress policy, and writable-layer snapshots — a closer fit to CodeLima's sandboxed-agent product than Lima's full-VM model, with much faster startup. The question was whether to keep Lima, support both behind a provider abstraction, or replace Lima outright.

## Decision Drivers

* Sub-second node startup versus Lima's slow full-VM boot.
* Egress control per node (deny-by-default network policy) strengthens the core "sandboxed agentic coding" pitch; Lima offers nothing comparable.
* OCI images make node environments reproducible and versionable; Lima provisioning depends on a mutable `template:default`.
* The provider-abstraction path (`plans/RUNTIME_PROVIDER_PLAN.md`) was already assessed as a large Lima-preserving refactor and cancelled.
* A single backend keeps the Service layer, tests, and docs simple; CodeLima has no users requiring Lima compatibility guarantees.

## Considered Options

* Keep Lima as the only backend.
* Add microsandbox behind a runtime-provider abstraction, supporting both.
* Replace Lima with microsandbox outright as a breaking change.

## Decision Outcome

Originally chosen option: "Replace Lima with microsandbox outright as a breaking change", because the provider abstraction only pays for itself if Lima must stay alive, and keeping Lima alive was the costliest part of every prior backend proposal. A clean break deletes the template-rendering subsystem, avoids threading two runtime vocabularies through metadata, and lets the upcoming daemon (IMPROVEMENT_PLAN Track 3) be born without Lima assumptions. Existing Lima-backed homes are rejected with an actionable error rather than migrated; this ships as a major version bump. Execution details live in `plans/MICROSANDBOX_MIGRATION_PLAN.md`, gated on a blocking validation spike (TTY fidelity, JSON status output, snapshot-as-clone, mount uid mapping, detached-process ownership).

### Positive Consequences

* Node creation and startup drop from VM-boot timescales to sub-second sandbox boots.
* Per-node network policy and read-only/hardened mounts become available product features.
* The Lima YAML template fetch/render pipeline, `lima_commands` template set, and Lima-shaped metadata fields are deleted rather than abstracted over.
* The Track 3 daemon and its persistence formats never encode `limactl` invocations.

### Negative Consequences

* microsandbox is beta software; breaking CLI changes are expected and must be absorbed behind the client seam with an exact-version pin.
* Lima's automatic guest-port forwarding is lost; ports must be declared at sandbox creation, a user-visible regression handled with configurable default port ranges.
* Existing Lima-backed nodes are not migrated; users must tear them down with the previous release.
* Guests are OCI-image-rooted rather than full distro VMs; workloads expecting systemd or cloud-init no longer work.

## Pros and Cons of the Options

### Keep Lima as the only backend

* Good, because it is proven in this codebase and requires no work.
* Good, because guest environments are full distro VMs with automatic port forwarding.
* Bad, because VM boots are slow, there is no egress control, and provisioning depends on a mutable upstream template.

### Add microsandbox behind a runtime-provider abstraction

* Good, because existing Lima nodes keep working during a transition.
* Good, because a future third backend would slot into the same seam.
* Bad, because it requires the full data-model refactor `RUNTIME_PROVIDER_PLAN.md` scoped and was cancelled over, plus permanent dual-backend test and support burden.

### Replace Lima with microsandbox outright as a breaking change

* Good, because the swap reduces to one client implementation plus a metadata schema break, and large Lima-specific subsystems are deleted.
* Good, because every downstream track (daemon, session persistence, integration tests) builds on the final backend from the start.
* Bad, because it bets the runtime layer on beta software and abandons existing Lima-backed homes.

## Links

* Supersedes `plans/RUNTIME_PROVIDER_PLAN.md` (cancelled draft)
* Execution plan: `plans/MICROSANDBOX_MIGRATION_PLAN.md`
* Accepted validation spike: `plans/spike-notes/MSB_SPIKE.md`
* Historical after this accepted replacement: [ADR 17](project_scoped_lima_command_templates_17.md), [ADR 18](global_lima_command_defaults_with_project_overrides_18.md), [ADR 19](apply_vm_resources_via_limactl_create_flags_19.md), [ADR 22](command_template_first_lima_overrides_22.md), [ADR 23](lima_command_lists_and_bootstrap_23.md), [ADR 37](use_lima_as_runtime_status_source_for_read_surfaces_37.md), [ADR 42](use_node_slug_for_new_lima_identity_42.md) — the command-template and slug-identity patterns carry forward under microsandbox vocabulary

## Validation Gate Outcome

The required E1 spike exercised microsandbox 0.6.6 through CodeLima's real
embedded Ghostty terminal. PTY allocation, resize propagation, raw input, Vim,
job control, OSC 52, and OSC 8 passed for `msb exec -t`. Closing a terminal with
the default microsandbox agent as guest PID 1 left an unreaped foreground-job
zombie. A focused retest then used the supported `--init auto` configuration and
an upstream systemd-equipped Debian image; the exact CodeLima close path reaped
the host client and guest job for both `exec -t` and SSH with no process left.

The zombie is therefore a guest-contract/configuration requirement, not a
fundamental backend blocker: the default image must provide a real PID 1 reaper
and sandbox creation must enable it. A subsequent complete automated rerun in
the available nested Linux/aarch64 environment passed both embedded transports,
including PTY/resize/raw input, Vim/htop/mouse, real-agent process ownership,
signals/job control, truecolor/OSC, latency, and close cleanup. This permits the
remaining Phase 0 experiments locally. E2 then established the JSON-array
status schema; E3 proved snapshot-to-new-name cloning; E4 proved bidirectional
mount writes retain the invoking host UID/GID; E5 characterized the detached
VMM and graceful stop; E6 preserved files and installed packages across
stop/start; E7 proved declared-only port publishing; and E8–E10 established the
version, root-without-sudo guest contract, duplicate error shape, and 128-byte
sandbox-name grammar. Those results accept the replacement. Native macOS/Linux
and authenticated human visual coverage remain release qualification in
TODO.md rather than blocking local implementation. Full evidence and exact
commands are in the spike report.
