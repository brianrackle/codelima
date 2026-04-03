# CodeLima Milestone 1 CLI Control Plane Specification

Status: Draft v1 (Go-targeted, Lima-native)  
Purpose: Define the Milestone 1 implementation contract for a local, CLI-first CodeLima control plane that manages project lineage, Lima-backed nodes, and bidirectional patch proposals without a web frontend or daemon API.

## Assumptions

- This specification is normative for Milestone 1 only.
- Milestone 1 is direct CLI mode only. Daemon mode, node-initiated control requests, and any local RPC transport are out of scope.
- Patch payloads must use a Git-style unified diff format. Git repositories are not the source of truth for project lineage or workspace state.
- `SPEC.md` remains as historical material; this document is the replacement implementation spec for Milestone 1.
- `vm` and `lima` are the only supported runtime/provider pair in Milestone 1. `container` and `colima` remain reserved enum values for forward compatibility.

## 1. Problem Statement

The existing CodeLima concept is underspecified and conflates several concerns: browser-driven interaction, custom terminal bridging, multi-agent runtime abstraction, and VM lifecycle management that Lima already provides. That creates unnecessary implementation risk and weakens the boundary between CodeLima metadata and Lima runtime state.

Milestone 1 must narrow the system to a local CLI control plane with three responsibilities:

- maintain CodeLima-specific metadata and lineage on the host filesystem
- delegate VM lifecycle and shell behavior to Lima and `limactl`
- provide snapshot-based project forking and bidirectional patch proposals along direct lineage edges

The result must be implementable as a Go CLI binary that works on macOS and Linux and can be extended later by a TUI or daemon without redesigning the storage model.

## 2. Goals and Non-Goals

### 2.1 Goals

- Provide a local CLI named `codelima` with stable command groups for `doctor`, `config`, `project`, `node`, `patch`, and `shell`.
- Store CodeLima metadata in a Lima-style filesystem hierarchy rooted at `CODELIMA_HOME`, defaulting to `~/.codelima`.
- Treat CodeLima metadata as authoritative for projects, lineage, snapshots, patch proposals, and node metadata.
- Treat Lima metadata under `LIMA_HOME` as authoritative for VM runtime mechanics and runtime state.
- Delegate VM creation, startup, shutdown, deletion, cloning, shell access, and runtime inspection to `limactl` or `lima` whenever Lima already provides the behavior.
- Model project branching as immutable snapshot lineage, not Git branch lineage.
- Support patch proposals in both `child_to_parent` and `parent_to_child` directions along a single existing lineage edge.
- Support agent installation profiles as declarative bootstrap data instead of an adapter-heavy runtime abstraction.
- Allow a future TUI or daemon to reuse the same metadata store and command semantics without redefining project, node, or patch concepts.

### 2.2 Non-Goals

- No web frontend, browser terminal, websocket shell bridge, or custom terminal transport.
- No daemon mode, no background scheduler, and no node-initiated control API in Milestone 1.
- No nested VMs. All Lima instances are created on the host by the host control plane.
- No public container runtime support in Milestone 1, even though the data model preserves `container` and `colima` enum values.
- No Git-based source of truth for project state, approval history, or lineage.
- No subtree fan-out patch application. Milestone 1 patch apply is limited to one source and one target on one direct lineage edge.
- No automatic long-running agent launch orchestration. Milestone 1 provisions and validates agent availability; future clients may consume the stored launch command.

## 3. System Overview

### 3.1 Main Components

- CLI binary
  - Parses commands, resolves configuration, acquires locks, performs preflight checks, invokes Lima commands, and persists metadata updates atomically.
- Filesystem metadata store
  - Stores project, node, snapshot, patch, event, and index data under `CODELIMA_HOME`.
- Snapshot engine
  - Captures immutable workspace snapshots and materializes forked workspaces from those snapshots.
- Patch engine
  - Computes Git-style diff bundles relative to an immutable base snapshot and performs checked apply operations against a target workspace.
- Lima adapter
  - Wraps `limactl` and `lima` invocation, normalizes command results, and reconciles runtime state back into CodeLima metadata.
- Agent bootstrap layer
  - Resolves an `AgentProfile`, injects install/setup commands into the generated Lima instance template, and validates the installed tool after first boot.

### 3.2 Abstraction Levels

- Host control plane
  - The only authority that mutates CodeLima metadata or creates/deletes Lima instances.
- Project lineage layer
  - Defines workspace lineage, immutable fork bases, and patch adjacency.
- Node runtime layer
  - Maps a project to a concrete Lima instance plus effective Lima command templates and bootstrap settings.
- External runtime layer
  - Lima owns VM boot, shell transport, copy semantics, instance storage, and guest execution transport.

For a Go implementation, the recommended package split is:

- `cmd/codelima`
- `internal/config`
- `internal/store`
- `internal/index`
- `internal/snapshot`
- `internal/patch`
- `internal/lima`
- `internal/agent`
- `internal/cli`

### 3.3 External Dependencies

Required:

- local writable filesystem for `CODELIMA_HOME`
- Lima installation exposing `limactl`
- a supported host OS: macOS or Linux
- POSIX-like process execution environment for command invocation
- `git` CLI for the underlying patch engine implementation

Reference material used for this specification:

- Lima internal layout: https://lima-vm.io/docs/dev/internals/
- `limactl` command reference: https://lima-vm.io/docs/reference/limactl/
- `limactl shell`: https://lima-vm.io/docs/reference/limactl_shell/
- `limactl clone`: https://lima-vm.io/docs/reference/limactl_clone/
- `limactl snapshot`: https://lima-vm.io/docs/reference/limactl_snapshot/
- `limactl list`: https://lima-vm.io/docs/reference/limactl_list/
- `limactl copy`: https://lima-vm.io/docs/reference/limactl_copy/

## 4. Core Domain Model

### 4.1 Entities

#### 4.1.1 Project

A `Project` represents a host workspace plus lineage metadata.

Required fields:

- `id`: opaque stable identifier; conforming Milestone 1 implementations should use UUIDv7
- `slug`: human-readable unique key
- `workspace_path`: canonical absolute host path
- `parent_project_id`: optional direct parent project identifier
- `fork_base_snapshot_id`: optional immutable base snapshot recorded when the project was forked
- `agent_profile_name`: default agent profile for new nodes in this project
- `setup_commands`: ordered list of shell commands to run inside new nodes on first boot
- `default_runtime`: enum, must be `vm` in Milestone 1
- `default_provider`: enum, must be `lima` in Milestone 1
- `default_lima_template`: template locator or absolute file path
- `lima_commands`: optional per-project Lima command override templates
- `created_at`
- `updated_at`
- `deleted_at`: optional tombstone timestamp

Invariants:

- `parent_project_id` and `fork_base_snapshot_id` are immutable after creation.
- `workspace_path` must remain local to the host and must not point inside `CODELIMA_HOME`.
- deleting a project must never delete the host workspace directory
- a project with live descendants or non-terminated nodes must not be deletable in Milestone 1

#### 4.1.2 SnapshotManifest

A `SnapshotManifest` is an immutable capture of a project workspace at a point in time.

Required fields:

- `id`
- `project_id`
- `kind`: `initial | fork_base | patch_source | patch_target | post_apply`
- `created_at`
- `workspace_path`
- `entry_count`
- `total_bytes`
- `entries`: normalized file list
- `tree_root`: filesystem path to the immutable stored tree copy

Each snapshot entry must include:

- `path`: workspace-relative, `/`-normalized, no leading slash
- `type`: `file | dir | symlink`
- `mode`: preserve executable bit; full host ACL preservation is out of scope
- `size`
- `sha256`: required for regular files
- `link_target`: required for symlinks

Invariants:

- snapshots are append-only
- snapshot IDs are never reused
- special files such as sockets, device nodes, and fifos are unsupported and must cause snapshot failure
- symlinks resolving outside the workspace root are unsupported in Milestone 1 and must cause snapshot failure

#### 4.1.3 AgentProfile

An `AgentProfile` defines guest bootstrap behavior for an installable agent.

Required fields:

- `name`
- `install_commands`: ordered shell commands executed on first boot
- `validation_command`: shell command that proves the agent is installed and callable
- `launch_command`: canonical command future clients may use to invoke the agent
- `environment`: optional key/value map or env var references

Initial built-in profile examples:

- `codex-cli`
- `claude-code`

Invariants:

- profiles are resolved before node creation and copied into node metadata as concrete bootstrap inputs
- changing a profile after node creation does not retroactively mutate existing nodes
- `launch_command` is declarative in Milestone 1; the system stores it but does not auto-run a persistent agent process

#### 4.1.4 Node

A `Node` is a logical runtime attached to a project and backed by a Lima instance in Milestone 1.

Required fields:

- `id`
- `slug`
- `project_id`
- `parent_node_id`: optional direct parent node for clone lineage
- `runtime`: enum `vm | container`
- `provider`: enum `lima | colima`
- `lima_instance_name`
- `lima_commands`: optional per-node Lima command override templates
- `status`
- `agent_profile_name`
- `bootstrap_commands`: concrete command list derived at create time
- `generated_template_path`
- `created_at`
- `updated_at`
- `deleted_at`: optional tombstone timestamp

Derived fields:

- `lima_instance_path`: resolved from `LIMA_HOME` and `lima_instance_name`
- `workspace_mount_path`: guest-visible mount location for the project workspace
- `last_reconciled_at`
- `last_runtime_observation`

Invariants:

- Milestone 1 may only create nodes where `runtime=vm` and `provider=lima`
- `lima_instance_name` must be unique across all non-terminated nodes
- node deletion must never delete the host project workspace
- node metadata must not duplicate Lima runtime internals beyond stable references and last-known summaries

#### 4.1.5 PatchProposal

A `PatchProposal` is a persisted diff bundle moving changes along a direct lineage edge.

Required fields:

- `id`
- `direction`: `child_to_parent | parent_to_child`
- `source_project_id`
- `source_node_id`: optional
- `target_project_id`
- `target_node_id`: optional
- `base_snapshot_id`
- `source_snapshot_id`
- `target_snapshot_id`
- `status`
- `patch_path`
- `diff_summary`
- `conflict_summary`: optional
- `approval`: optional approval metadata
- `apply_result`: optional result metadata
- `created_at`
- `updated_at`

Invariants:

- the source and target must be direct lineage neighbors
- the base snapshot must be the immutable lineage edge snapshot shared by source and target
- proposals are single-source, single-target, single-edge only
- approval and apply are separate state transitions
- apply must never silently overwrite conflicting target changes

#### 4.1.6 Event Logs

Milestone 1 persists structured logs as JSONL files rather than database rows.

Required event categories:

- `project.*`
- `node.*`
- `patch.*`
- `system.*`

Each event record must include:

- `timestamp`
- `level`
- `entity_type`
- `entity_id`
- `action`
- `result`
- `message`
- `fields`

`ContextReport` remains a reserved log type for future daemon mode and is not required to be emitted in Milestone 1.

### 4.2 Stable Identifiers and Normalization Rules

- IDs must be opaque, filesystem-safe, and case-stable.
- Slugs must be lowercase and match `^[a-z0-9][a-z0-9-]{0,62}$`.
- `workspace_path` must be canonicalized via symlink resolution before persistence.
- snapshot entry paths must use `/` separators on all platforms and must not contain `..`.
- Lima instance names should be derived from `<project-slug>-<node-slug>-<short-id>` and truncated conservatively to remain shell-safe and path-safe.
- `NodeStatus.registering` is reserved for future daemon mode and must not be emitted by a conforming Milestone 1 implementation.
- `NodeRuntime.container` and `NodeProvider.colima` are reserved but unsupported; user requests selecting them must fail with `UnsupportedFeature`.

## 5. Workflow and CLI Contract

### 5.1 Invocation and Global Behavior

The canonical command form is:

```text
codelima <group> <command> [flags] [args]
```

Required global flags:

- `--home <path>`: overrides `CODELIMA_HOME`
- `--json`: emits machine-readable JSON output
- `--log-level <level>`: controls CLI log verbosity

Global rules:

- mutating commands must acquire entity-scoped locks before changing metadata
- metadata writes must use write-to-temp then rename semantics
- read commands must tolerate partially missing optional files and surface warnings instead of panicking
- `--json` must emit structured success or error objects; human-readable formatting is implementation-defined

Recommended exit code contract:

- `0`: success
- `2`: invalid arguments or validation failure
- `3`: dependency unavailable
- `4`: object not found
- `5`: precondition failure or state conflict
- `6`: external command failure
- `7`: internal metadata corruption or unrecoverable partial state

The `daemon` command group is reserved and out of scope for Milestone 1.

### 5.2 Project Commands

Required commands:

- `project create`
- `project list`
- `project show`
- `project update`
- `project delete`
- `project tree`
- `project fork`

#### `project create`

Behavior:

- validates the workspace path exists, is local, and is not already bound to another live project
- creates project metadata
- captures an `initial` snapshot
- updates slug and path indexes
- emits `project.created`

The command must not create a node or a Lima instance.

#### `project update`

Mutable fields:

- `slug`
- `agent_profile_name`
- `setup_commands`
- `default_lima_template`
- `lima_commands`

Immutable fields:

- `id`
- `workspace_path`
- `parent_project_id`
- `fork_base_snapshot_id`

#### `project delete`

Behavior:

- refuses deletion if the project has non-terminated nodes
- refuses deletion if the project has live child projects
- marks the project as deleted via `deleted_at`
- removes active indexes so the slug may be reused later
- retains historical metadata and snapshots on disk

#### `project tree`

Behavior:

- renders the lineage rooted at the selected project or at all roots if none is specified
- includes deleted projects only when explicitly requested

#### `project fork`

Behavior:

- requires a source project and a destination workspace path
- captures an immutable `fork_base` snapshot from the source project's current workspace
- materializes the child workspace from that snapshot into the destination path
- creates a child project with `parent_project_id` and `fork_base_snapshot_id` set
- does not create a node automatically

The destination workspace path must not already contain user files; a non-empty destination must fail.

### 5.3 Node Commands

Required commands:

- `node create`
- `node list`
- `node show`
- `node start`
- `node stop`
- `node clone`
- `node delete`
- `node status`
- `node logs`
- `node shell`

#### `node create`

Behavior:

- resolves the project and the effective agent profile
- validates `runtime=vm` and `provider=lima`
- generates a stable Lima instance name
- renders a local Lima template file containing:
  - the selected base template
  - the mounted project workspace
  - first-boot install and setup commands
- invokes `limactl create`
- persists node metadata with `status=created`
- emits `node.created`

CPU, memory, disk, and any other advanced Lima create flags are part of the effective `lima_commands.create` template rather than typed project or node resource fields.

`node create` must not start the instance unless an explicit future flag is introduced. Milestone 1 create and start are separate commands.

#### `node start`

Behavior:

- reconciles current Lima state before mutation
- if the instance is already running, treats the command as idempotent success after reconciliation
- invokes `limactl start <instance>`
- marks the node `provisioning` while first-boot bootstrap and validation are pending
- runs `validation_command` inside the guest using `limactl shell`
- marks the node `running` on success
- marks the node `failed` on bootstrap or validation failure

Bootstrap commands must run once per node, on the first successful boot after creation or clone. They must not rerun automatically on every subsequent `node start`.

#### `node stop`

Behavior:

- invokes `limactl stop <instance>`
- marks the node `stopped` after runtime reconciliation confirms the instance is not running
- is idempotent if the instance is already stopped

#### `node clone`

Definition:

- copy the source node's VM into a new node in the same project

Behavior:

- stops and restarts the source node internally when the source VM is running
- invokes the effective `lima_commands.clone` template as the VM duplication primitive
- keeps the cloned node in the source project
- persists a cloned node with `parent_node_id` set, `project_id` unchanged, and `status=created`

If Lima clone fails, the command must fail rather than leaving partially-written node metadata behind.

#### `node delete`

Behavior:

- invokes `limactl delete <instance>`
- marks the node `terminating` before the external command
- marks the node `terminated` after successful completion
- retains node metadata, generated template, and logs for history

#### `node status`

Behavior:

- reads node metadata
- queries Lima runtime state
- emits a reconciled view combining CodeLima metadata and Lima runtime facts

#### `node logs`

Behavior:

- reads local JSONL event logs by default
- may expose additional Lima-side log references when available
- must not require SSH into the guest merely to show host-side event history

#### `node shell`

Behavior:

- is a required alias-compatible surface for shell access
- must delegate to `limactl shell <instance> [command...]`
- must support interactive shell entry when no command is provided
- must support command execution when trailing arguments are provided

### 5.4 Patch Commands

Required commands:

- `patch propose`
- `patch list`
- `patch show`
- `patch approve`
- `patch apply`
- `patch reject`

#### `patch propose`

Behavior:

- validates source and target adjacency
- resolves the immutable base snapshot shared by the lineage edge
- captures a current source snapshot and a current target snapshot
- computes a Git-style patch representing `base -> source`
- stores the patch bundle and summary
- creates a proposal in `submitted` state

Rules:

- an empty diff must fail with a no-op error
- rename detection is optional; add/delete representation is acceptable in Milestone 1
- binary changes must either be encoded in a Git-compatible binary-safe patch bundle or fail explicitly before proposal creation

#### `patch approve`

Behavior:

- records an explicit approval actor, timestamp, and optional note
- transitions only from `submitted` to `approved`

#### `patch apply`

Behavior:

- requires `approved`
- refreshes the target workspace view
- performs a checked apply against the current target using the proposal's base snapshot
- writes no target files if preflight conflict detection fails
- records detailed conflict metadata on failure
- records result metadata and captures a `post_apply` snapshot on success

Rules:

- apply is limited to one direct lineage target
- full dry-run validation must occur before any target mutation
- if promotion from staging to target fails after dry-run success, the proposal must transition to `failed` and preserve recovery metadata
- silent overwrite is forbidden

#### `patch reject`

Behavior:

- transitions `submitted` or `approved` proposals to `rejected`
- preserves the patch bundle and review history

### 5.5 Shell Command

The canonical shell surface is:

```text
codelima shell <node> [-- <command...>]
```

Rules:

- `codelima shell <node>` and `codelima node shell <node>` may both be provided, but `codelima shell` is the canonical public surface
- for the Lima instance named `default`, the implementation may use the `lima` shorthand when behavior is equivalent
- the implementation must not create its own shell transport, browser terminal abstraction, or websocket tunnel

## 6. Configuration Specification

### 6.1 Source Precedence and Resolution

Configuration precedence, highest to lowest:

1. CLI flags
2. environment variables
3. `CODELIMA_HOME/_config/config.yaml`
4. built-in defaults

Required environment variables:

- `CODELIMA_HOME`

Pass-through environment variables:

- `LIMA_HOME`
- profile-specific environment references used by `AgentProfile`

Resolution rules:

- missing `CODELIMA_HOME` defaults to `~/.codelima`
- `CODELIMA_HOME` must resolve to a local writable path
- Lima command templates resolve with precedence built-in defaults, then `config.yaml`, then project metadata, then node metadata
- node-scoped create-time overrides may be supplied before `node.yaml` exists through an explicit Lima command override file
- node creation freezes effective bootstrap inputs into node metadata

### 6.2 Dynamic Reload and Change Semantics

- there is no daemon, so no live config reload protocol exists in Milestone 1
- each CLI invocation reads configuration fresh
- config edits affect only future commands
- existing nodes are not retroactively mutated by config changes

### 6.3 Preflight Validation

`doctor` and all mutating commands must validate:

- `CODELIMA_HOME` exists or can be created
- metadata directories are writable
- `limactl` is on `PATH`
- `limactl list --json` succeeds
- the configured patch engine is available
- required agent profiles are well-formed
- configured workspace paths are local and canonicalizable

Warnings should be emitted for:

- overly long `CODELIMA_HOME` paths
- non-local filesystems
- unsupported runtime/provider defaults
- orphaned Lima instances or orphaned metadata entries

### 6.4 Config Summary Cheat Sheet

Illustrative global config shape:

```yaml
metadata_root: ~/.codelima
default_runtime: vm
default_provider: lima
default_agent_profile: codex-cli
default_lima_template: template:default
snapshot:
  ignore:
    - .git/**
    - .codelima/**
agent_profiles_dir: ~/.codelima/_config/agent-profiles
lima_commands:
  create: "{{binary}} create -y --name {{instance_name}} --cpus=2 --memory=4 --disk=20 {{template_path}}"
  start: "{{binary}} start -y {{instance_name}}"
  clone: "{{binary}} clone -y {{source_instance}} {{target_instance}}"
  workspace_seed_prepare: "sudo rm -rf {{target_path}} && sudo mkdir -p {{target_parent}} && sudo chown \"$(id -un)\":\"$(id -gn)\" {{target_parent}}"
  copy: "{{binary}} copy{{recursive_flag}} {{source_path}} {{copy_target}}"
  shell: "{{binary}} shell{{workdir_flag}} {{instance_name}}{{command_args}}"
```

`config` command surface for Milestone 1:

- `config show`
- `config path`
- `config validate`

Editing config files is performed outside the CLI in Milestone 1.

## 7. State Machine and Lifecycle

### 7.1 Internal States

#### NodeStatus

- `created`
  - metadata exists and Lima instance was created, but the node has not completed start/validation
- `provisioning`
  - boot, first-run setup, or validation is actively in progress
- `registering`
  - reserved, not emitted in Milestone 1
- `running`
  - Lima reports the instance running and the last bootstrap/validation completed successfully
- `stopped`
  - instance exists and is not running
- `failed`
  - a create/start/clone/bootstrap/reconcile/apply-related node operation failed
- `terminating`
  - delete has been requested and is in progress
- `terminated`
  - instance has been deleted and node metadata is tombstoned

#### PatchStatus

- `draft`
  - reserved transient state before proposal finalization
- `submitted`
  - reviewable and awaiting disposition
- `approved`
  - explicitly approved but not yet applied
- `applied`
  - applied successfully
- `rejected`
  - intentionally rejected
- `failed`
  - proposal generation or apply failed

### 7.2 Attempt and Command Lifecycle

All mutating commands must follow this lifecycle:

1. resolve config
2. acquire lock(s)
3. load metadata
4. reconcile relevant Lima state
5. validate preconditions
6. execute external operation if required
7. persist metadata atomically
8. append event log entries
9. release lock(s)

### 7.3 Transition Triggers

- `node create`: absent -> `created`
- `node start`: `created | stopped | failed` -> `provisioning` -> `running | failed`
- `node stop`: `running | failed` -> `stopped`
- `node delete`: any non-terminated state -> `terminating` -> `terminated`
- `patch propose`: absent -> `submitted`
- `patch approve`: `submitted` -> `approved`
- `patch reject`: `submitted | approved` -> `rejected`
- `patch apply`: `approved` -> `applied | failed`

### 7.4 Idempotency and Recovery Rules

- repeated `node start` on a running instance must succeed after reconciliation
- repeated `node stop` on a stopped instance must succeed after reconciliation
- repeated `node delete` on a terminated node must succeed as a no-op
- if Lima state and metadata disagree, the reconciled external observation wins for runtime presence, while CodeLima metadata remains authoritative for lineage and identity
- if a command fails after creating an external instance but before metadata finalization, the next command or `doctor` run must surface an orphan and provide enough information to repair or delete it

## 8. Scheduling, Coordination, and Reconciliation

Milestone 1 has no daemon. Coordination is per-command and local.

Required coordination rules:

- mutating commands must use advisory file locks under `CODELIMA_HOME/_locks`
- operations affecting multiple metadata namespaces, such as `patch propose` or `node clone`, must lock them in a deterministic order to avoid deadlock
- indexes must be updated within the same critical section as the owning metadata

Required reconciliation rules:

- runtime inspection must use Lima command output, not cached CodeLima status alone
- `node list`, `node show`, and `node status` must reconcile stale runtime fields before returning
- the in-memory model must be fully rebuildable from:
  - filesystem metadata under `CODELIMA_HOME`
  - current Lima instance inspection under `LIMA_HOME`

No background reconciliation loop is required in Milestone 1.

## 9. Execution Environment, Workspace, and Filesystem Layout

### 9.1 Host Environment Requirements

- the control plane runs on the host only
- all Lima instances are created by the host control plane
- `CODELIMA_HOME` and managed workspaces must be on local filesystems
- nested VM creation from within guest instances is out of scope

### 9.2 Required Filesystem Layout

The required layout under `CODELIMA_HOME` is:

```text
~/.codelima/
  _config/
    config.yaml
    agent-profiles/
      codex-cli.yaml
      claude-code.yaml
  _locks/
  _index/
    projects/
      by-slug/
    nodes/
      by-instance/
    patches/
      by-status/
  projects/
    <project-id>/
      project.yaml
      events.jsonl
      snapshots/
        <snapshot-id>/
          manifest.json
          tree/
  nodes/
    <node-id>/
      node.yaml
      events.jsonl
      context.jsonl
      instance.lima.yaml
      bootstrap.json
      lima-instance.ref
  patches/
    <patch-id>/
      proposal.yaml
      events.jsonl
      patch.diff
      summary.json
      conflicts.json
      apply-result.json
```

Rules:

- metadata files may be YAML or JSON where specified above; names are normative
- JSONL event logs must be append-only
- `context.jsonl` is reserved for future richer reporting and may be absent or empty in Milestone 1
- `lima-instance.ref` must store the Lima instance name and may be accompanied by a symlink to the actual Lima instance directory when the filesystem supports it
- indexes may be symlinks or small ref files; behavior is normative, representation is implementation-defined

### 9.3 Workspace Capture Rules

Snapshot capture rules:

- include all regular files, directories, and supported symlinks under the workspace root
- exclude `.codelima/` within the workspace if present
- excluding `.git/` is recommended and may be the default; the effective ignore set must be visible in config output
- preserve relative path, file mode, and symlink target
- preserve file contents exactly

Fork materialization rules:

- create the destination workspace from the stored snapshot tree
- preserve executable bits and symlinks
- fail if the destination path exists and is non-empty

### 9.4 Lima Command Override Model

Required command-template fields:

- `create`
- `clone`
- `start`
- `stop`
- `delete`
- `copy`
- `shell`

Rules:

- default CPU, memory, disk, and other advanced Lima behavior live in the effective command templates rather than typed resource fields
- project and node metadata may override any subset of the global command templates
- node-scoped create-time overrides may be supplied before persistence through an explicit override file

## 10. Integration Contract

### 10.1 Required Lima Operations

The implementation must delegate these behaviors to Lima:

- `limactl create`
  - initial instance creation for `node create`
- `limactl start`
  - starting an existing instance
- `limactl stop`
  - stopping an instance
- `limactl delete`
  - deleting an instance
- `limactl clone`
  - cloning an instance for `node clone`
- `limactl list --json`
  - runtime inspection and reconciliation
- `limactl shell`
  - guest command execution and interactive shell access

Optional but permitted internal use:

- `limactl copy`
- `limactl snapshot create`
- `limactl snapshot list`
- `limactl template copy`

### 10.2 Query and Request Semantics

External command rules:

- capture exit code, stdout, stderr, and duration for every external command
- parse structured Lima output when available, especially from `limactl list --json`
- use non-interactive invocation modes for automation where appropriate
- interactive shell handoff must preserve stdio for the calling user

Timeout guidance:

- inspection commands should use short timeouts
- create, start, stop, delete, and clone should use long but finite timeouts
- interactive shell sessions must not be subject to fixed automation timeouts

### 10.3 Lima Template Normalization Rules

For each node, CodeLima must materialize a generated Lima template containing:

- effective base template
- deterministic instance name
- workspace mount
- bootstrap provision steps
- agent profile environment

The generated template is part of CodeLima metadata and must be stored at `nodes/<id>/instance.lima.yaml`.

VM resource flags and other `limactl create` or `limactl clone` options are expected to live in the effective command templates instead of the generated template YAML.

### 10.4 Error Handling Contract

Required normalized error categories:

- `InvalidArgument`
- `NotFound`
- `PreconditionFailed`
- `UnsupportedFeature`
- `DependencyUnavailable`
- `ExternalCommandFailed`
- `PatchConflict`
- `StateDrift`
- `MetadataCorruption`

All external command failures must preserve:

- invoked command
- exit status
- stderr summary
- associated entity IDs

### 10.5 Important Boundary Notes

- CodeLima must not directly mutate Lima internal files under `LIMA_HOME`
- CodeLima may store references to Lima instance names and paths, but Lima remains authoritative for instance runtime internals
- CodeLima must not emulate or replace Lima shell semantics

## 11. Agent Bootstrap Contract

### 11.1 Profile Resolution

Resolution order:

1. explicit node creation override
2. project default agent profile
3. global default agent profile

The resolved profile must be copied into node bootstrap metadata at create time.

### 11.2 Install and Validation Semantics

Required first-boot order:

1. agent `install_commands`
2. project `setup_commands`
3. agent `validation_command`

Rules:

- commands are executed inside the guest
- command order is preserved
- the first failing command terminates bootstrap
- failures mark the node `failed`

### 11.3 Launch Command Contract

- `launch_command` is required metadata even though Milestone 1 does not auto-run a persistent agent
- the launch command must be shell-ready and suitable for future clients
- Milestone 1 may optionally perform a non-persistent readiness check derived from the launch command, but this is not required for conformance

### 11.4 Environment Variable Handling

- literal environment values stored in agent profiles must be treated as sensitive configuration
- secret values should be referenced indirectly from host environment variables when possible
- CLI output and event logs must redact configured secret values

## 12. Logging, Status, and Observability

### 12.1 Logging Conventions

- structured JSONL logs are required for events
- each mutating command must emit start and result events
- human-readable CLI logs are allowed but secondary to structured logs

### 12.2 Outputs and Sinks

Required observability sinks:

- stdout/stderr for the active CLI invocation
- entity-local JSONL event files
- normalized `--json` command responses

### 12.3 Runtime Snapshot and Monitoring Interface

Required status surfaces:

- `doctor`
- `project show`
- `node show`
- `node status`
- `patch show`

There is no metrics endpoint in Milestone 1.

### 12.4 Human-Readable Status Surface

Human-readable output should include:

- stable IDs and slugs
- workspace paths
- node runtime status plus last reconciliation time
- patch status and conflict summary when present

### 12.5 Metrics, Accounting, and Rate Limits

- no long-lived metrics system is required
- per-command duration should be captured in logs
- rate limiting is not required in Milestone 1 because all operations are local CLI invocations

## 13. Failure Model and Recovery Strategy

### 13.1 Failure Classes

- invalid user input
- missing or malformed configuration
- unavailable Lima dependency
- workspace snapshot failure
- unsupported filesystem object or external symlink
- Lima command failure
- patch generation failure
- patch conflict on apply
- metadata corruption or missing index entries

### 13.2 Recovery Behavior

- failed create/start/clone/delete/apply operations must emit structured failure events
- partial metadata writes must be cleaned up or ignored safely on the next run
- failed bootstrap leaves the node available for inspection via `node shell` unless Lima startup itself failed

### 13.3 Partial State Recovery

Required recoverable states:

- metadata exists but Lima instance is missing
- Lima instance exists but metadata finalization failed
- patch dry-run succeeded but promotion to target workspace failed
- index entry is missing but canonical entity metadata exists

`doctor` must detect and report these states.

### 13.4 Operator Intervention Points

The operator must be able to:

- inspect reconciled node state
- inspect stored patch and conflict metadata
- delete orphaned metadata or Lima instances via guided commands in a future extension
- rerun idempotent commands after correcting dependencies or workspace issues

Manual metadata editing is possible but not part of the supported operational model.

## 14. Security and Operational Safety

### 14.1 Trust Boundary Assumption

- the host control plane is trusted
- guest nodes are less trusted than the host
- node bootstrap commands are powerful and may execute arbitrary guest commands

### 14.2 Filesystem, Network, and Resource Safety Requirements

- `CODELIMA_HOME` permissions should default to user-private access
- host workspace mounts must be explicit and visible in generated node metadata
- CodeLima must not expose a network control API in Milestone 1
- unsupported runtime/provider selections must fail early

### 14.3 Secret Handling

- secrets must not be printed in CLI output
- secrets must be redacted from event logs
- storing plaintext secrets in project metadata is discouraged

### 14.4 Unsafe Extension Points

- arbitrary bootstrap shell commands
- arbitrary agent install commands
- future daemon/RPC surfaces
- future container provider integrations

Each unsafe extension must remain explicit and opt-in.

### 14.5 Hardening Guidance

- prefer short local metadata paths, similar to Lima's own path-length guidance
- validate all external command arguments before invocation
- reject workspace snapshots containing unsupported filesystem objects
- never follow symlinks outside the workspace root during snapshot, fork, or patch operations

## 15. Reference Algorithms

### 15.1 Startup and Command Preflight

```text
load_config()
resolve_metadata_root()
ensure_directories_exist()
validate_dependencies()
acquire_required_locks()
load_entities()
reconcile_lima_state_if_needed()
```

### 15.2 Project Fork

```text
source = load_project(source_id)
assert source.deleted_at is nil
base = capture_snapshot(source.workspace_path, kind="fork_base")
assert destination_path is empty_or_missing
materialize_snapshot(base, destination_path)
child = new_project(
  parent_project_id=source.id,
  fork_base_snapshot_id=base.id,
  workspace_path=destination_path,
)
persist_project(child)
append_event(child, "project.forked")
```

### 15.3 Node Create and Start

```text
project = load_project(project_id)
profile = resolve_agent_profile(project, override)
assert runtime == "vm"
assert provider == "lima"
instance_name = generate_instance_name(project, node_slug, node_id)
template = render_lima_template(project, profile, resources, instance_name)
persist_template_artifacts(node_id, template)
run("limactl", "create", "--name", instance_name, template.path)
persist_node(status="created", template=template)

run("limactl", "start", instance_name)
mark_node("provisioning")
run("limactl", "shell", instance_name, "--", "sh", "-lc", profile.validation_command)
if validation_failed:
  mark_node("failed")
else:
  mark_node("running")
```

### 15.4 Node Clone

```text
source_node = load_node(source_id)
assert source_node.status != "running"
child_project = fork_project(source_node.project_id, destination_workspace)
new_instance = generate_instance_name(child_project, child_slug, child_id)
run("limactl", "clone", source_node.lima_instance_name, new_instance, ...)
rewrite_clone_mounts_to(child_project.workspace_path)
persist_child_node(parent_node_id=source_node.id, status="created")
```

### 15.5 Patch Propose and Apply

```text
edge = resolve_direct_lineage_edge(source_project, target_project, direction)
base = load_snapshot(edge.base_snapshot_id)
source_snap = capture_snapshot(source_project.workspace_path, kind="patch_source")
target_snap = capture_snapshot(target_project.workspace_path, kind="patch_target")
patch = diff_git_style(base.tree_root, source_snap.tree_root)
assert patch is not empty
proposal = persist_patch(status="submitted", patch=patch)

assert proposal.status == "approved"
current_target = capture_snapshot(target_project.workspace_path, kind="patch_target")
result = apply_patch_checked(base.tree_root, proposal.patch_path, current_target.tree_root)
if result.conflicts:
  persist_conflicts(proposal, result)
  mark_patch("failed")
else:
  promote_staging_tree(result.staging_tree, target_project.workspace_path)
  post = capture_snapshot(target_project.workspace_path, kind="post_apply")
  persist_apply_result(proposal, post)
  mark_patch("applied")
```

## 16. Test and Validation Matrix

### 16.1 Core Conformance

- project create produces the expected directory layout and initial snapshot
- project fork records immutable base snapshot lineage and materializes the child workspace correctly
- node create persists metadata and uses `limactl create`
- node start/stop/delete delegate to Lima and reconcile status correctly
- `codelima shell` delegates to `limactl shell`
- patch propose creates a Git-style diff relative to the correct base snapshot
- patch approve and apply enforce separate transitions
- patch apply refuses conflicts without mutating the target workspace

### 16.2 Extension Conformance

- `runtime=container` is rejected with `UnsupportedFeature`
- `provider=colima` is rejected with `UnsupportedFeature`
- reserved `registering` state is never emitted in Milestone 1

### 16.3 Real Integration Profile

- macOS smoke test with Lima installed
- Linux smoke test with Lima installed
- rebuild the entire in-memory view from `CODELIMA_HOME` plus Lima inspection
- verify agent bootstrap using initial `codex-cli` and `claude-code` profiles

## 17. Implementation Checklist

### 17.1 Required for Conformance

- implement the filesystem metadata store under `CODELIMA_HOME`
- implement project CRUD plus fork and lineage tree
- implement node create/list/show/start/stop/clone/delete/status/logs/shell
- implement patch propose/list/show/approve/apply/reject
- implement snapshot capture and fork materialization
- implement Lima delegation and runtime reconciliation
- implement agent profile resolution plus first-boot validation
- implement JSONL event logging and `doctor`

### 17.2 Recommended Extensions

- symlink-based indexes for faster lookups
- machine-readable `--json` output for every command, not just status surfaces
- optional use of `git` CLI for binary-safe diff/apply compatibility
- richer recovery commands for orphan cleanup

### 17.3 Operational Validation Before Production

- verify `CODELIMA_HOME` path length and permissions
- verify Lima delegation on both supported operating systems
- verify patch conflict handling with target drift
- verify clone behavior updates workspace mounts correctly
- verify bootstrap failures leave enough logs for diagnosis
