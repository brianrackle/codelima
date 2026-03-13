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

## TUI Session Reuse

The TUI launches shells by spawning `codelima shell <node>` inside one PTY-backed terminal emulator per opened node.
Changing the selected node only swaps which emulator is visible, so scrollback, alternate screens, cursor state, and unsubmitted input stay attached to that node until the TUI exits.

## Guest Workspace Seed

New nodes do not mount the host project workspace into the guest.
Instead, the first successful start copies the host workspace into the guest at the same absolute path, which keeps guest edits isolated while preserving the expected path layout for setup commands and shell entry.

## Clone Is VM Copy

Node clone uses `limactl clone` as a full-VM copy.
If the source node is running, CodeLima stops it, clones it, and starts it again. The child node keeps the same guest workspace path and bootstrap state as the source VM; the clone command's `--workspace` flag only defines the child project's host workspace.

## Workspace Rebind Guard

Project workspace paths are treated as durable host bindings.
Rebinding a project to a new workspace path is allowed only after all nodes for that project are terminated, which keeps seed inputs, node metadata, and project metadata from drifting out of sync.

## Default List Tables

Human-facing list commands should render concise tables by default instead of dumping full YAML records.
Keep the column contract stable for quick scanning. When a list represents live infrastructure, include the runtime state directly in the table instead of forcing a second `show` command. Reserve `--json` for full structured output used by automation.

## Project Tree Includes Nodes

Lineage views should expose both projects and their attached nodes.
Keep nodes scoped under their owning project in both the human tree render and the structured JSON tree so operators can see the full topology from one command.
