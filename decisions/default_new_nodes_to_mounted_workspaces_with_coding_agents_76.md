# Default New Nodes to Mounted Workspaces with Coding Agents

## Context and Problem Statement

CodeLima supported live mounted and isolated copied workspaces, but every creation surface defaulted to `copy`, and a fresh reserved `default` configuration referenced no environments. The common interactive workflow therefore required operators to opt into live host synchronization and separately assign agent installers before a default node was ready for Codex and Claude Code.

## Decision Drivers

* A node created without extra flags should work directly against its host directory.
* Fresh default nodes should include the two built-in coding-agent CLIs.
* CLI, TUI, and direct service callers must resolve the same creation defaults.
* Operators must retain an explicit isolated-copy option.
* Existing node metadata and explicitly edited default configurations must not be silently reinterpreted or overwritten.

## Considered Options

* Keep `copy` and an empty environment list as defaults.
* Default all new nodes to `mounted` and seed `codex` plus `claude-code` into only a fresh default configuration.
* Rewrite existing homes and nodes to the new defaults during repair.

## Decision Outcome

Chosen option: "Default all new nodes to `mounted` and seed `codex` plus `claude-code` into only a fresh default configuration", because it makes the common path immediately useful while preserving explicit isolation and durable user choices.

### Positive Consequences

* Service, CLI, and TUI creation paths all default new nodes to a writable mount at the canonical host directory.
* `--workspace-mode copy` remains available for isolated experiments.
* Fresh default configurations reference `codex` and `claude-code` in that order, so bootstrap installs both CLIs.
* New configurations continue to snapshot the current default, including its environment list.
* Seed and repair leave an existing default configuration untouched.
* Blank workspace-mode metadata from older records continues to resolve as `copy`; only new creation requests use the mounted default.

### Negative Consequences

* A default node can modify host files directly, reducing isolation compared with the former default.
* First bootstrap takes longer and requires network access because it installs two coding agents.
* Users who want isolation or a smaller bootstrap must explicitly select `copy` or edit the default configuration.

## Pros and Cons of the Options

### Keep `copy` and an empty environment list as defaults

* Good, because it maximizes isolation and minimizes initial bootstrap work.
* Bad, because it adds setup to the primary live-development workflow.
* Bad, because a nominal default node is not ready for the supported coding agents.

### Default new nodes to `mounted` and seed coding-agent environments only for fresh homes

* Good, because host and guest changes are immediately shared.
* Good, because both built-in agents are ready after bootstrap.
* Good, because explicit persisted choices remain authoritative.
* Bad, because the convenient default has a broader host-write boundary.

### Rewrite existing homes and nodes during repair

* Good, because every home converges on one set of defaults.
* Bad, because repair would mutate user configuration and could change existing runtime behavior.
* Bad, because blank legacy workspace metadata historically means `copy`.

## Links

* Refines [Support per-node copied and mounted workspaces](support_per_node_workspace_modes_16.md)
* Refines [Seed default environment configs](seed_default_environment_configs_6.md)
* Refines [Replace projects with directory-bound nodes and reusable configurations](replace_projects_with_directory_bound_nodes_and_reusable_configurations_72.md)
