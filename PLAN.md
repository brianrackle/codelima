• Proposed Plan


  # CodeLima CLI-First Spec Rewrite

  ## Summary

  - Rewrite SPEC.md as a Go-based, Lima-native control plane with no web frontend and no browser terminal.
  - Milestone 1 stays CLI-only. The future TUI is just another client over the same local metadata/control model.
  - The spec should treat codelima as a thin orchestration layer over limactl and lima, not a reimplementation of Lima lifecycle or shell behavior.

  ## Key Changes

  - Metadata and state
      - Replace SQLite with a Lima-style filesystem store rooted at CODELIMA_HOME, defaulting to ~/.codelima.
      - Model the layout after Lima’s ~/.lima structure: global config under _config, per-project directories, per-node directories, JSONL event logs, YAML/JSON metadata files, patch bundles, and symlink/index directories where useful.
      - Keep the filesystem store as the source of truth for CodeLima metadata; treat Lima’s own instance directory under ~/.lima/<instance> as the source of truth for VM-specific runtime state.
  - Core model
      - Project: hierarchical code lineage, host workspace path, optional parent project, setup commands, agent install profile, default Lima template/settings, and snapshot lineage metadata.
      - Node: logical runtime unit linked to a project, optional parent node, provider/runtime kind, Lima instance name, requested resources, registration state, status, and paths to its CodeLima and Lima metadata.
      - Replace one-way merge objects with bidirectional PatchProposal objects.
      - PatchProposal must support both child_to_parent and parent_to_child patching, with source project/node, target project/node, base snapshot reference, status, conflict summary, and apply result metadata.
      - NodeEvent and ContextReport remain persisted as files/logs rather than database rows.
  - Lima-native control behavior
      - State clearly that CodeLima must use limactl and lima commands under the hood whenever Lima already provides the behavior.
      - Milestone 1 management flows should delegate to limactl create/start/stop/delete/clone/snapshot/list/shell/copy instead of using custom VM lifecycle code.
      - Shell access should mirror Lima: codelima shell <node> execs into limactl shell <instance>; for the default instance, the spec can allow a shorthand that mirrors lima.
      - CodeLima should not introduce a custom shell transport, terminal bridge, or browser terminal abstraction.
  - Topology and control-plane rules
      - Keep a single host control plane and no nested VMs.
      - A node may have VM children logically, but VM children are always created on the host machine through Lima by the control plane.
      - Container support stays out of Milestone 1, but the data model should keep container as a reserved runtime kind so the future Colima work fits without redesign.
      - Daemon mode and direct CLI mode both remain in scope:
          - Daemon mode enables node self-service and node-initiated requests.
          - Direct CLI mode is user-operated only; no node-initiated control API is available.
  - Agent support
      - Remove the adapter-heavy “multi-agent runtime” concept.
      - Define multi-agent support as install/bootstrap configuration: the selected agent is installed into the VM and invoked via configured commands.
      - The spec should model this as an AgentProfile containing install commands, validation command, default launch command, and optional environment variables.
      - Call out Codex CLI and Claude Code as initial profile examples, but keep the mechanism generic so any installable agent can be used.
  - Branching and patching
      - Project forks are snapshot-based, not Git-based.
      - Forking records an immutable base snapshot manifest for the source project.
      - Patch proposals are computed against that base snapshot and may flow either upward to a parent or downward to a child.
      - Limit Milestone 1 apply semantics to one source and one target at a time along an existing lineage edge; no subtree fan-out in v1.
      - Approval and apply remain separate steps, with explicit conflict reporting and no silent overwrite.

  ## Public Interfaces and Types

  - CLI groups
      - daemon, doctor, config, project, node, patch, and shell.
      - Use patch instead of merge so the spec reflects bidirectional patching.
  - Command surface
      - project create/list/show/update/delete/tree/fork
      - node create/list/show/start/stop/clone/delete/status/logs/shell
      - patch propose/list/show/approve/apply/reject
      - Define node clone as “fork the source node’s project lineage and create a child node from that fork.”
  - Enums and statuses
      - NodeRuntime = vm | container
      - NodeProvider = lima | colima
      - NodeStatus = created | provisioning | registering | running | stopped | failed | terminating | terminated
      - PatchDirection = child_to_parent | parent_to_child
      - PatchStatus = draft | submitted | approved | applied | rejected | failed
  - Filesystem layout
      - Explicitly define the required directories and metadata files under ~/.codelima, including _config, projects/<id>, nodes/<id>, patches/<id>, and event/context logs.
      - Include a link field or symlink from each node to its Lima instance name/path instead of duplicating Lima runtime internals.

  ## Test Plan

  - Filesystem metadata
      - Creating, updating, and deleting projects and nodes produces the expected directory layout and metadata files under ~/.codelima.
      - CodeLima can rebuild its in-memory view entirely from the filesystem store plus Lima instance inspection.
  - Lima delegation
      - Starting, stopping, cloning, shelling into, snapshotting, and deleting nodes go through limactl/lima, not custom VM control paths.
      - codelima shell behaves like limactl shell, including interactive shell entry and optional command execution.
  - Control modes
      - Daemon mode allows node registration, status/context reports, node creation requests, resource requests, patch proposal submission, and termination requests.
      - Direct CLI mode supports operator management only and rejects node-initiated flows.
  - Patch flows
      - Child-to-parent patch proposal: create, review, approve, apply, and conflict handling.
      - Parent-to-child patch proposal: create, review, approve, apply, and conflict handling.
      - Patch application is limited to direct lineage targets in Milestone 1.
  - Agent profiles
      - A node boots with the selected install profile, validates the installed agent, and launches the configured agent command successfully.
  - Platform coverage
      - Packaging and smoke tests on macOS and Linux.

  ## Assumptions and Defaults

  - Default metadata root is ~/.codelima, mirroring Lima’s preference for a short local path rather than XDG/Application Support.
  - CodeLima is authoritative for hierarchy, projects, patch proposals, and node registration metadata; Lima is authoritative for VM instance mechanics.
  - Container support is modeled but not implemented in Milestone 1.
  - Official Lima references used for grounding:
      - Usage and lima shell shorthand: https://lima-vm.io/docs/usage/
      - limactl command surface: https://lima-vm.io/docs/reference/limactl/
      - limactl shell behavior: https://lima-vm.io/docs/reference/limactl_shell/
      - Lima home and instance metadata layout: https://lima-vm.io/docs/dev/internals/
      - limactl clone: https://lima-vm.io/docs/reference/limactl_clone/
      - limactl snapshot: https://lima-vm.io/docs/reference/limactl_snapshot/