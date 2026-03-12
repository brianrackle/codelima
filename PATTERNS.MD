# Patterns

## Filesystem Metadata Store

The CLI treats `CODELIMA_HOME` as the authoritative store for projects, nodes, snapshots, patches, and indexes.
Every write goes through temp-file-plus-rename semantics so metadata stays recoverable after partial failures.

## External Command Boundary

Lima interactions are isolated behind `LimaClient`.
Tests use a fake implementation to assert delegation behavior without creating real VMs, while the production path uses `limactl`.

## Snapshot Then Promote

Fork and patch flows never mutate a target workspace before an immutable snapshot exists.
Patch apply stages changes in a temporary tree, validates with `git apply --check`, then syncs managed files back into the live workspace.

## Bootstrap State Freeze

Node creation resolves an `AgentProfile` once and stores the concrete bootstrap inputs in `bootstrap.json`.
Later config changes do not retroactively mutate existing nodes.

## Workspace-Aware Shell Delegation

Shell entry always normalizes the documented `--` separator before delegating to Lima.
Interactive shells and one-off commands pass the node workspace through `limactl shell --workdir` so shell entry is stable regardless of the host directory where `codelima` is launched.

## Workspace Rebind Guard

Project workspace paths are treated as durable host bindings.
Rebinding a project to a new workspace path is allowed only after all nodes for that project are terminated, which keeps runtime mounts, node metadata, and project metadata from drifting out of sync.
