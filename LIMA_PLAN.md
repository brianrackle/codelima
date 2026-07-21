# Return to Lima Runtime Plan

Status: Implemented locally (Go, Lima 2.x); full native release qualification remains in `TODO.md` item 28

Purpose: Replace Microsandbox with Lima as CodeLima's sole VM runtime while preserving the current directory-bound node, reusable configuration, daemon-owned terminal, and dynamic hostname-forwarding product model.

## 1. Decision and Scope

CodeLima will return to Lima through a clean backend replacement. This is not a git revert and will not restore the pre-schema-v3 application architecture. The current daemon, terminal protocol, node/configuration model, lifecycle rollback rules, and TUI remain authoritative.

The implementation will add a Lima client behind the existing runtime seam, introduce a Lima-native metadata schema, qualify the current Lima 2.x behavior, and then remove Microsandbox completely. No released build will expose a runtime selector or support both backends.

The intended host runtime is:

- macOS arm64: Lima with Apple's Virtualization.framework (`vmType: vz`).
- Linux amd64/arm64: Lima with QEMU and KVM acceleration.
- Guest: an upstream Lima Ubuntu template unless the Phase 0 comparison selects a Debian template.
- Filesystem sharing: VirtioFS on VZ; 9p on QEMU.
- Built-in containerd: disabled.
- VM lifecycle and guest entry: `limactl` 2.x.

This change replaces an opaque Microsandbox VMM process with Lima's per-instance hostagent and VM driver. It does not attempt to eliminate host processes for running VMs.

## 2. Assumptions

- CodeLima may make another major metadata break because Microsandbox VM disks cannot be converted into Lima VM disks safely.
- Users will recreate nodes in a fresh schema-v4 `CODELIMA_HOME`; no automatic schema-v3 or disk migration will be provided.
- Configuration and environment definitions are small enough to recreate or copy manually. An export/import tool is outside this plan.
- Lima 2.1.0 is the minimum implementation target. CodeLima will support compatible Lima 2.x releases rather than pinning one exact patch version; the local 2.1.0 qualification confirmed the required template, list, clone, watch, and SSH metadata contracts.
- The current release platforms remain macOS arm64, Linux amd64, and Linux arm64.
- Lima does not replace Microsandbox network policy. Per-node domain egress rules will be removed from the supported surface.

## 3. Goals and Non-Goals

### 3.1 Goals

- Make Lima the only runtime used by node create, list, start, stop, clone, delete, copy, shell, logs, and forwarding operations.
- Retain the current daemon-owned terminal model, live daemon update, terminal restoration, and Ghostty terminal behavior.
- Retain directory-bound nodes and reusable configurations.
- Retain mounted and copied workspace modes.
- Retain explicit `HOST:GUEST` mappings and dynamic `localhost`, `127.0.0.1`, and `{node}.localhost` HTTP/WebSocket forwarding.
- Use a full distribution guest with systemd, ordinary package management, and a distribution kernel.
- Remove the Microsandbox Go SDK, runtime downloader, `msb` helper process, custom libkrunfw dependency, and MSB-specific SSH relay.
- Requalify the mounted-workspace file-pressure mitigation against Lima VirtioFS and retain a runtime-neutral version if Lima can reproduce the pressure condition.
- Keep idle host CPU near zero and prevent a recurring runtime query from producing visible input stalls.
- Preserve production error mapping, lifecycle rollback, incomplete-node cleanup, and observable runtime status.

### 3.2 Non-Goals

- Supporting Lima and Microsandbox concurrently.
- Converting Microsandbox writable layers or snapshots into Lima disks.
- Preserving Microsandbox OCI images as bootable node images.
- Reimplementing Microsandbox domain-based egress policy on top of a host firewall or proxy.
- Eliminating Lima hostagent, VZ, QEMU, or SSH processes from the host process list.
- Supporting non-native guest architectures in the first Lima return release.
- Supporting macOS, Windows, or FreeBSD guests.
- Adding Docker, containerd, Kubernetes, or another container runtime to the default node.

## 4. Required Product Decisions

These decisions are normative for the initial implementation:

1. Lima is the sole backend. Do not add a provider registry, runtime selection flag, or dual-backend compatibility layer.
2. Metadata schema v4 retains the existing `sandbox_name` and `image` field names so the runtime replacement does not create unnecessary product-surface churn. In schema v4, `image` contains a Lima template reference and `sandbox_name` contains the Lima instance name.
3. Schema v3 is rejected with an actionable error. CodeLima must not mutate a schema-v3 home.
4. The default template is `template:ubuntu` unless Phase 0 demonstrates a material reliability or idle-cost advantage for a specific Debian template.
5. The default VM driver is `vz` on macOS and `qemu` on Linux.
6. Built-in containerd is disabled in both user and system modes.
7. A mounted node exposes only its selected workspace. CodeLima must remove inherited Lima home-directory mounts.
8. Lima automatic port publication is disabled. Codelima remains the owner of public host listeners and hostname routing.
9. Explicit ports are rendered as static Lima forwarding rules before a final ignore-all rule.
10. Initial Lima SSH connectivity uses a localhost TCP SSH port (`ssh.overVsock: false`) so the Go forwarding client can use a stable, inspectable transport. A later optimization may add direct AF_VSOCK support.
11. Lima's current clone operation requires a stopped source. `node clone` temporarily stops a running source, clones it, and restores the source to its prior running state. A stopped source remains stopped.

## 5. Target Architecture

```text
CodeLima TUI / CLI
        |
        v
per-home CodeLima daemon
  - terminal ownership
  - Lima observation cache
  - dynamic HTTP/WebSocket routing
        |
        +---- limactl create/start/stop/list/clone/delete/shell/copy
        |
        +---- persistent Go SSH connection per running node
        |
        v
Lima hostagent per node
        |
        +---- VZ on macOS
        +---- QEMU/KVM on Linux
        |
        v
Ubuntu or Debian full VM
```

### 5.1 Main Components

#### `LimaClient`

`LimaClient` implements the existing `SandboxClient` contract. The interface may be renamed to `RuntimeClient` only if the rename is mechanical and completed in the same change; the implementation must not reintroduce the old project-shaped `LimaClient` interface.

Required operations:

- `Version`
- `ResolveCommands`
- `List`
- `Create`
- `Start`
- `Stop`
- `Delete`
- `Clone`
- `CopyToGuest`
- `Shell`
- `Watch`

The implementation should recover the proven process-execution and JSON-lines parsing patterns from `v0.0.15:internal/codelima/lima.go`, then adapt them to current node metadata and error types.

#### Lima template renderer

The renderer materializes `nodes/<node-id>/instance.lima.yaml` from a resolved Lima template and frozen node metadata. The file is generated runtime input, not an independent source of truth.

Rendering must:

- resolve the configured template with `limactl template copy --fill`;
- parse and modify YAML structurally rather than with textual replacement;
- set CPU, memory, disk, VM type, containerd, SSH, mounts, and forwarding rules;
- strip inherited mounts and disable inherited container tooling while preserving boot-critical distribution provisioning;
- preserve the image locations and architecture metadata required to boot the chosen template;
- write atomically with mode `0600`;
- pass `limactl validate` before `limactl create` is attempted.

#### Lima observation service

The daemon owns one `limactl watch --json` process and a runtime observation cache. It performs one full `limactl list --json` reconciliation at startup and after watcher recovery.

Rules:

- TUI refreshes must read cached observations rather than spawning `limactl list` every two seconds.
- Watcher EOF or malformed events must be logged and retried with bounded exponential backoff.
- While the watcher is unavailable, the daemon may run a full list reconciliation no more frequently than once every 30 seconds.
- A successful watch reconnection resets the backoff and triggers a full reconciliation.
- Cache entries are keyed by normalized Lima instance name.
- CodeLima lifecycle metadata remains distinct from live Lima observations.

#### Lima forwarding transport

The forwarding implementation retains the existing `forwardingPeer` behavior but replaces the Microsandbox SSH relay.

For every running node, the daemon must:

- resolve the instance SSH config path from machine-readable `limactl list` output;
- read only the generated instance SSH config owned by Lima;
- obtain host, port, user, identity file, and host-key behavior from that config;
- establish one persistent `golang.org/x/crypto/ssh` client;
- discover listening guest TCP ports without spawning a process per poll;
- use SSH `direct-tcpip` channels for proxied connections;
- reconnect after node restart, SSH failure, daemon handoff, or Lima hostagent replacement.

The existing daemon-generated Microsandbox authorization key and `__sdk-ssh-serve` helper are removed. CodeLima must not edit the user's global SSH configuration.

## 6. Configuration and Metadata Schema

### 6.1 Settings

Schema-v4 keeps the current daemon-only serialized settings contract:

```yaml
daemon:
  autostart: true
  restore: respawn
  virtiofs_reclaim: true
  virtiofs_reclaim_threshold_percent: 20
```

The reusable default configuration retains its `image` field and seeds it as
`template:ubuntu`. Runtime command defaults remain an internal compatibility
seam and are not added to `settings.yaml`.

The compiled minimum version is authoritative and is not user-configurable. CodeLima accepts Lima major version 2 at or above the minimum, rejects older or different major versions, and warns in `doctor` when the installed minor is newer than the highest release qualified by CodeLima.

### 6.2 Configuration

```yaml
image: template:ubuntu
```

The field name remains `image`, but OCI image references are no longer accepted: the value resolves through Lima's template mechanism.

CPU, memory, disk, agent profile, environments, and bootstrap commands retain their current frozen-at-node-create semantics.

### 6.3 Node

The durable node fields become:

```yaml
runtime: vm
provider: lima
sandbox_name: api-dev
image: template:ubuntu
vcpus: 2
memory_mib: 4096
disk_mib: 20480
ports: []
workspace_mode: mounted
guest_workspace_path: /workspace
workspace_mount_path: /Users/example/src/api
```

Remove:

- `net_policy`
- Microsandbox-only command-template compatibility fields

Keep:

- UUID and slug identity
- directory path
- configuration reference
- frozen resource values
- environments and bootstrap state
- workspace mode and paths
- lifecycle state
- timestamps

The store artifact remains `sandbox.ref`, and the sandbox-name index continues to be updated atomically with node metadata. In schema v4 the referenced sandbox is always a Lima instance.

### 6.4 Schema Guard

`metadataSchemaVersion` becomes `4`.

On startup:

- schema `4`: proceed;
- schema `3`: fail without mutation and explain how to stop/delete Microsandbox nodes with the previous CodeLima release or select a fresh home;
- legacy Lima layouts without a schema marker: reject as unsupported rather than guessing;
- a truly empty home: initialize schema v4;
- any other marker: reject as unsupported.

The guard must be tested to prove that rejected homes receive no created directories, rewritten settings, or modified node files.

## 7. Lima Instance Specification

The rendered Lima YAML must satisfy these invariants.

### 7.1 Compute and Disk

- Convert `MemoryMiB` and `DiskMiB` without decimal/binary unit drift.
- Reject zero or overflow values before rendering.
- Use the node's frozen CPU, memory, and disk values.
- Use native host architecture only.
- Disable video and audio devices unless a later feature explicitly requires them.

### 7.2 Guest Services

- Set `containerd.system: false` and `containerd.user: false`.
- Do not use `plain: true`, because mounted workspaces and the Lima guest agent are required.
- Do not enable package upgrades automatically during every boot.
- Retain cloud-init and systemd behavior supplied by the selected distribution template.
- Set a predictable bash-compatible guest shell.

### 7.3 Mounts

- Copy workspace mode renders no host mount and seeds through `limactl copy` after first start.
- Mounted workspace mode renders exactly one writable mount from `WorkspaceMountPath` to `GuestWorkspacePath`.
- Inherited home, `/tmp/lima`, or other template mounts must be removed.
- Host and guest paths must be canonicalized and validated before rendering.
- The host mount must not broaden from the selected directory through a symlink or parent path.
- Mount failures must fail node start visibly; CodeLima must not fall back silently to copy mode.

### 7.4 Ports

Port rules are ordered:

1. One static rule for each explicit `HOST:GUEST` node mapping.
2. An ignore-all TCP/UDP guest port rule.

This prevents Lima's fallback automatic forwarder from racing CodeLima for host listeners. The daemon's HTTP/WebSocket forwarder remains independent and may route any discovered guest TCP listener.

Explicit host ports must remain unique across running nodes. Conflicts return the existing precondition error before Lima start where possible and remain recoverable if an unrelated host process temporarily owns the port.

### 7.5 Networking

The default Lima user-mode network is sufficient for outbound access and SSH management. `vzNAT`, `socket_vmnet`, bridged networking, and routable guest IPs are outside the first release.

Schema v4 has no network-policy model, configuration, validation, or CLI/TUI
surface. Lima nodes use ordinary outbound network access.

## 8. Lifecycle Contract

### 8.1 Create

1. Validate Lima dependency and version.
2. Resolve and render the node's Lima template under its metadata directory.
3. Validate the rendered template.
4. Run `limactl create` with the final instance name.
5. Verify the instance appears as stopped in `limactl list --json`.
6. Persist registration state only after Lima creation succeeds.

Create must be idempotent with respect to CodeLima retry state. If the expected Lima instance already exists and belongs to the same incomplete node, cleanup/recovery may continue. An unrelated name collision is a precondition failure.

### 8.2 Start

1. Reject a missing instance.
2. Start with the configured timeout and observable progress.
3. Wait for Lima to report `Running` and for `limactl shell` to succeed.
4. Seed a copy-mode workspace if not already seeded.
5. Execute frozen bootstrap commands and validation using the current bootstrap state machine.
6. Record successful bootstrap and surface the running observation.

The optional containerd readiness path must not run. A guest that is SSH-ready and satisfies the bootstrap validation is ready for CodeLima.

### 8.3 Stop

- Stopping a stopped node succeeds.
- A normal stop uses Lima's graceful stop path with a bounded timeout.
- A forced stop is used only for delete recovery after graceful stop fails.
- Stopping the VM must not stop the CodeLima daemon or close host-shell terminals.
- Guest terminals for the node close or report the runtime exit through the current terminal event path.

### 8.4 Clone

- The source node and Lima instance must exist.
- If the source is running, CodeLima stops it before cloning and restarts it afterward. If cloning fails, restoring the source still takes precedence over returning, and both failures are surfaced when applicable.
- A source that was stopped before cloning remains stopped.
- The target instance name must be unique before metadata is committed.
- `limactl clone` creates a stopped target.
- The target inherits frozen configuration, resources, bootstrap completion, workspace mode, and directory association according to current `NodeClone` semantics.
- Mounted workspaces continue to reference the same host directory unless a future worktree feature changes that contract.
- A clone failure removes incomplete target metadata only after runtime cleanup succeeds or records durable cleanup state.

### 8.5 Delete

- Runtime teardown occurs before durable node metadata becomes unreachable.
- Running instances are stopped, then deleted.
- Missing Lima instances are treated as already deleted.
- A failed runtime delete retains enough metadata for `node cleanup-incomplete` to retry.
- CodeLima must never use a broad `limactl factory-reset` or delete an instance not resolved from the node's exact instance identity.

### 8.6 Host Restart and Daemon Update

- Stopped and running intent remains CodeLima metadata; Lima is authoritative for actual runtime state.
- After host restart, nodes reconcile from `limactl list` and remain stopped unless explicitly started.
- Daemon live update transfers terminal state as it does today; the replacement daemon reconnects the Lima watch stream and forwarding peers.
- Daemon shutdown does not stop Lima instances.

## 9. Error and Recovery Model

The Lima client maps errors into existing CodeLima classes:

| Condition | CodeLima error |
|---|---|
| `limactl` missing or unusable | `dependencyUnavailable` |
| unsupported Lima version | `dependencyUnavailable` |
| invalid template, resources, mount, or port | `invalidArgument` |
| instance not found | `notFound` |
| instance already exists | `preconditionFailed` |
| source stop or restart during clone fails | existing lifecycle error mapping |
| host port already reserved by a CodeLima node | `preconditionFailed` |
| malformed `limactl` JSON | `metadataCorruption` or dependency protocol error, never partial data |
| guest command nonzero exit | `guestExitError` with bounded stderr tail |
| context cancellation/deadline | original context error |
| other `limactl` failure | `externalCommandFailed` with action and instance fields |

Rules:

- Command logs must include action, instance, elapsed time, and exit status without recording secrets or full environment contents.
- Stderr retained for error messages remains bounded.
- Timeouts must terminate the direct child and its process group where supported.
- JSON parsers must accept documented extra fields but reject missing identity or invalid status values.
- Partial command success across a multi-command template must identify the failing command index.

## 10. Observability

### 10.1 Doctor

`codelima doctor` must report:

- parsed `limactl` version and supported-version result;
- host VM driver selection;
- KVM availability on Linux or VZ availability on macOS;
- resolved `LIMA_HOME`;
- mount type;
- Lima list health and unmatched instances through the existing warnings.

Doctor must remain read-only unless `--repair` is supplied.

### 10.2 Node Logs

`node logs` retains the existing CodeLima lifecycle-event contract. Lima's
hostagent, serial, and cloud-init files remain directly inspectable beneath the
instance directory reported by `limactl list --json`; this backend change does
not add a second log format to the product surface.

### 10.3 Daemon Snapshot

The daemon snapshot adds:

- watcher connected state, last event time, restart count, and last error;
- last full reconciliation time;
- cached instance count alongside the existing forwarding connection state.

Microsandbox SDK state is removed. The VirtioFS reclaim section is retained only if L0.14 requires a runtime-neutral Lima implementation.

## 11. Security and Operational Safety

- Only exact instance names from validated node metadata may reach destructive `limactl` commands.
- Instance names must satisfy Lima's name and Unix-socket path-length limits under the resolved `LIMA_HOME`.
- Generated templates must expose only requested workspace paths.
- CodeLima must not inherit or mount the host home by default.
- SSH identity files and instance configs must be read only from the resolved Lima home and must have safe ownership and permissions.
- Host-key relaxation, if required for Lima's localhost SSH endpoint, must be scoped to that exact generated instance endpoint and never applied globally.
- Command construction must continue to use structured argv where possible. Shell templates must apply the existing quoting rules and reject unresolved placeholders.
- CodeLima must not alter arbitrary Lima instances. Cleanup and doctor distinguish CodeLima-owned names from unrelated instances.
- Users must be told that Lima provides VM isolation but, in this release, outbound guest network access is unrestricted.

## 12. Implementation Phases

### Phase 0: Blocking Lima 2.x Spike

Deliverable: `plans/spike-notes/LIMA_RETURN_SPIKE.md` containing exact commands, versions, outputs, measurements, and PASS/FAIL results for macOS arm64 and Linux KVM.

The spike uses project-rooted `./tmp/` artifacts and cleans all temporary instances and files when complete.

Required experiments:

| ID | Experiment | Pass criterion |
|---|---|---|
| L0.1 | Version and machine output | Version and list output are machine-parseable and contain name, status, SSH config path/port, and instance directory. |
| L0.2 | VZ and QEMU boot | Selected Ubuntu template boots on macOS VZ and Linux KVM/QEMU with containerd disabled. |
| L0.3 | Embedded terminal | Ghostty-backed `limactl shell` passes TTY, resize, raw input, Vim/htop, signals, job control, paste, OSC, and tab-close cleanup checks. |
| L0.4 | Mounted workspace | Bidirectional writes work, ownership is the invoking host user, and no extra host directory is mounted. |
| L0.5 | Copy workspace | Recursive copy preserves regular files, directories, executable bits, and symlinks without path escape. |
| L0.6 | Persistence | Guest packages and files survive stop/start and host reboot. |
| L0.7 | Clone | Stopped clone preserves disk state; running clone fails with a stable classifiable error. |
| L0.8 | Port ownership | Explicit static forwarding works, ignore-all suppresses automatic listeners, and two nodes can serve the same guest port through `{node}.localhost`. |
| L0.9 | SSH transport | A persistent Go SSH client can connect using Lima-generated instance data with `ssh.overVsock: false` and open `direct-tcpip` channels. |
| L0.10 | Watch stream | `limactl watch --json` reports lifecycle changes and, if available, guest listening-port changes with stable instance identity. |
| L0.11 | Idle behavior | After a 60-second settle, each idle VM's host runtime averages below 2% of one CPU core for five minutes and shows no two-second input hitch. |
| L0.12 | Sleep/wake | VM, SSH, watcher, and forwarding recover after macOS sleep/wake without recreating the instance. |
| L0.13 | Failure recovery | Interrupted create/start/delete operations leave classifiable state that `cleanup-incomplete` can repair. |
| L0.14 | Host file pressure | A metadata-heavy mounted-workspace workload determines whether Lima VirtioFS needs the existing host file-table mitigation; retain and adapt it if pressure is reproducible. |

Blocking failures:

- terminal fidelity or resize failure;
- persistent idle CPU comparable to the current MSB problem;
- mounted workspace correctness failure;
- inability to suppress Lima-owned dynamic host listeners;
- inability to create a persistent per-node forwarding transport;
- unreliable stop/start persistence.

If a blocking failure cannot be resolved through supported Lima configuration, stop implementation and supersede this plan with the evidence. Do not patch around a failed foundation in product code.

### Phase 1: ADR and Characterization Tests

1. Add a numbered ADR superseding ADR 55 and recording the clean Lima return, schema-v4 break, network-policy removal, and process-model tradeoff.
2. Mark ADR 55 as superseded without rewriting its historical rationale.
3. Add characterization tests around the current `SandboxClient` call sites, lifecycle rollback, terminal launch contract, and forwarding factory.
4. Recover useful Lima test cases from tag `v0.0.15` without copying obsolete project-schema assumptions.
5. Add fake `limactl` fixtures for success, JSON-lines output, timeouts, malformed output, and known error shapes.

Exit criterion: the existing behavior to be preserved is executable as tests before the production backend changes.

### Phase 2: Lima Client and Template Renderer

1. Implement `LimaClient` against the current runtime interface.
2. Implement version parsing and compatible Lima 2.x validation.
3. Implement structured list parsing and status normalization.
4. Implement template resolution, structural YAML rendering, validation, and atomic writes.
5. Implement create/start/stop/delete/clone/copy/shell with typed errors and bounded timeouts.
6. Add unit and fake-process integration tests before enabling the client in `NewService`.

Exit criterion: all runtime operations pass automated tests using a fake `limactl`, and the native spike fixture passes through the new client.

### Phase 3: Schema v4 and Product Surface

1. Add schema-v4 types and guard behavior.
2. Retain image fields and flags while changing their accepted values from Microsandbox OCI images to Lima template references.
3. Retain sandbox identity fields while using them as Lima instance identities.
4. Set `provider: lima` and reject all other providers.
5. Remove network-policy fields, flags, forms, outputs, examples, and validation paths.
6. Restore the generated `instance.lima.yaml` artifact and retain `sandbox.ref` under node metadata.
7. Update default configuration seeding and self-host examples.
8. Preserve bootstrap state, mounted/copy workspace semantics, and lifecycle rollback.

Exit criterion: a fresh home supports the complete CLI lifecycle, while schema-v3 and legacy homes fail without mutation.

### Phase 4: Daemon Observation and Forwarding

1. Add the daemon-owned Lima watch process and observation cache.
2. Remove recurring per-client runtime list calls from the two-second TUI refresh path.
3. Replace `SandboxSSHRuntime` and `__sdk-ssh-serve` with the Lima SSH connection resolver.
4. Preserve the persistent forwarding peer and HTTP/WebSocket proxy behavior.
5. Use watch port events for listener discovery if L0.10 proves them complete; otherwise retain bounded `/proc/net/tcp*` discovery over the persistent SSH connection.
6. Verify two nodes can use the same guest port and claimant transfer remains deterministic.
7. Reconnect watch and SSH peers across node restarts, daemon updates, and sleep/wake.

Exit criterion: all dynamic forwarding and daemon-integration tests pass with no one-second or two-second process-spawn loop.

### Phase 5: Remove Microsandbox

Remove:

- `github.com/superradcompany/microsandbox/sdk/go` and transitive-only dependencies;
- `msb_sdk.go`, `msb_sdk_runtime.go`, `msb_ssh.go`, and their MSB-specific tests;
- required Microsandbox version checks and runtime installation;
- `MSB_HOME` guidance and SDK cache behavior;
- Microsandbox runtime command compatibility code;
- MSB-specific VirtioFS pressure code; preserve or replace it with a runtime-neutral implementation if L0.14 requires the mitigation;
- Microsandbox wording from errors, logs, CLI help, TUI labels, examples, and release packaging.

Repository-wide checks must prove production code contains no `microsandbox`, `msb`, `libkrun`, or `libkrunfw` runtime references except historical ADRs, migration plans, or release notes intentionally retained as history.

Exit criterion: CodeLima builds and runs without Microsandbox installed and never creates an `msb` process.

### Phase 6: Packaging, Documentation, and Release Qualification

1. Add Lima as a Homebrew/runtime dependency.
2. Update `README.md` with install, node lifecycle, template, workspace, forwarding, troubleshooting, and process-model examples.
3. Update `BUILD.md` with the supported Lima range, native qualification, packaging, and release process.
4. Update `QA.md` with the flows in Section 13.
5. Update `PATTERNS.MD` for the reusable Lima CLI adapter, rendered-template-as-derived-artifact, and daemon observation-cache patterns.
6. Update `ROADMAP.md` when the backend return is complete or partially complete.
7. Record any explicitly deferred work in `TODO.md` before release.
8. Run all automated and manual verification on macOS arm64, Linux amd64, and Linux arm64.

Exit criterion: release archives and Homebrew installation work from a machine with Lima installed and no Microsandbox state.

## 13. Test and Validation Matrix

### 13.1 Automated Tests

Required unit coverage:

- Lima semver parsing and supported-range decisions.
- Instance-name validation, normalization, collision behavior, and path-length limits.
- JSON-lines and JSON-array list parsing using captured Lima 2.x fixtures.
- Unknown list fields, malformed records, missing identity, and status normalization.
- Template resolution and structural YAML rendering.
- Removal of inherited mounts and containerd settings.
- Resource unit conversion and overflow rejection.
- Explicit port rule ordering followed by ignore-all.
- Mounted and copy workspace rendering.
- Command placeholder resolution and shell quoting.
- Lifecycle error mapping and context cancellation.
- Create/start/delete rollback and incomplete cleanup.
- Stopped clone success, running-source stop/clone/restart, and source restoration after clone failure.
- Schema-v4 initialization and schema-v3 no-mutation rejection.
- SSH config parsing restricted to Lima-owned files.
- Forwarding discovery, direct TCP dialing, reconnect, IPv4/IPv6 loopback, HTTP Upgrade, and two-node same-port routing.
- Watch event parsing, cache updates, EOF recovery, backoff, and full reconciliation.
- No recurring TUI list process after the observation cache is initialized.
- Release formula includes Lima and excludes Microsandbox.

Required commands:

```sh
make verify
make test-race
make test-integration
make smoke
```

All tooling must run through Make recipes. New dedicated native Lima checks should be added as a Make recipe rather than documented as uncaptured ad hoc tooling.

### 13.2 Manual QA

`QA.md` must define and be completed for:

1. Fresh schema-v4 home and default configuration.
2. Create, inspect, start, shell, stop, restart, clone, and delete.
3. Mounted workspace bidirectional editing and ownership.
4. Copy workspace seeding, executable files, and symlinks.
5. Bootstrap Codex and Claude environment configurations.
6. Interactive terminal fidelity, resize, paste, mouse, hyperlinks, clipboard, signals, and tab close.
7. Generic and node-qualified dynamic HTTP/WebSocket forwarding.
8. Two nodes listening on the same guest port.
9. Explicit static TCP forwarding and host-port conflict recovery.
10. Daemon live update with terminal and forwarding recovery.
11. Host reboot persistence and stopped-instance recovery.
12. macOS sleep/wake recovery.
13. Activity Monitor idle CPU and input-latency observation for at least five minutes.
14. Multiple simultaneous TUIs without multiplied runtime queries.
15. Schema-v3 rejection without state mutation.
16. `doctor`, node logs, and cleanup-incomplete failure recovery.
17. Installation from release archive and Homebrew on a host without Microsandbox.

Every verification-only Lima instance, home, SSH session, temporary file, test listener, and generated package must be removed before qualification is complete.

## 14. Rollout and Compatibility

### 14.1 Release Boundary

This change requires a major CodeLima release because it changes the backend, metadata schema, image vocabulary, clone preconditions, and network-policy surface.

Before upgrading, release notes instruct users to:

1. Use the previous CodeLima release to stop and delete Microsandbox nodes they no longer need.
2. Preserve any host workspace files; mounted workspaces already live outside the VM.
3. Archive or rename the existing schema-v3 `CODELIMA_HOME` if its metadata is needed for reference.
4. Start the new release with a fresh schema-v4 home.
5. Recreate configurations and nodes with Lima template locators.

The new release must never delete `~/.microsandbox` automatically.

### 14.2 Rollback

Rollback means running the previous CodeLima release against the preserved schema-v3 home. Schema-v4 and schema-v3 homes must remain separate. No release may attempt bidirectional migration.

### 14.3 Completion Criteria

The Lima return is complete only when:

- Lima is the only production runtime dependency.
- A fresh node completes every supported lifecycle operation on all release platforms.
- No production path loads the Microsandbox SDK or launches `msb`.
- Mounted and copied workspaces pass automated and manual verification.
- Dynamic hostname forwarding and explicit ports pass multi-node verification.
- Schema-v3 homes are rejected without mutation.
- Idle CPU meets the Phase 0 threshold and interactive input has no recurring hitch.
- `make verify`, race tests, integration tests, smoke tests, and every `QA.md` flow pass.
- README, BUILD, QA, PATTERNS, ROADMAP, TODO, release packaging, and the superseding ADR are current.
- All manual-test artifacts have been cleaned up.

## 15. Estimated Schedule

For one developer familiar with the current codebase:

| Work | Estimate |
|---|---:|
| Phase 0 spike and ADR | 3-5 days |
| Lima client and renderer | 5-8 days |
| Schema-v4 product conversion | 4-6 days |
| Observation cache and forwarding transport | 5-8 days |
| Removal, packaging, documentation, and native QA | 5-8 days |
| Contingency for platform-specific Lima behavior | 3-5 days |

Expected total: four to six focused weeks. A basic lifecycle-only backend could appear earlier, but it is not complete until forwarding, idle behavior, native QA, documentation, and cleanup satisfy this plan.

## 16. References

- Historical CodeLima Lima implementation: git tag `v0.0.15`, especially `internal/codelima/lima.go` and `internal/codelima/lima_commands.go`.
- Historical replacement decision: `decisions/replace_lima_with_microsandbox_as_sole_runtime_55.md`.
- Historical migration inventory: `plans/MICROSANDBOX_MIGRATION_PLAN.md`.
- [Lima documentation](https://lima-vm.io/docs/)
- [Lima VZ driver](https://lima-vm.io/docs/config/vmtype/vz/)
- [Lima filesystem mounts](https://lima-vm.io/docs/config/mount/)
- [Lima port forwarding](https://lima-vm.io/docs/config/port/)
- [Lima SSH integration](https://lima-vm.io/docs/usage/ssh/)
- [Lima clone command](https://lima-vm.io/docs/reference/limactl_clone/)
- [Lima watch command](https://lima-vm.io/docs/reference/limactl_watch/)
- [Lima internals and process state](https://lima-vm.io/docs/dev/internals/)
