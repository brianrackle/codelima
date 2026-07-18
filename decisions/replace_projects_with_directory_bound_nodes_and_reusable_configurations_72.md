# Replace projects with directory-bound nodes and reusable configurations

## Context and Problem Statement

The previous project model coupled a reusable VM setup to one host workspace, forced a project row above every node, and made it hard to reuse the same setup across unrelated directories. CodeLima needs reusable VM recipes while keeping the directory association specific to each runtime node.

## Decision Drivers

* Reuse one VM recipe across any number of directories.
* Allow several independent nodes for the same directory.
* Make `codelima .` describe the current directory tree directly.
* Keep existing nodes stable when a reusable recipe changes.
* Remove project hierarchy, snapshots, and project terminals from the user model.

## Considered Options

* Replace projects with global configurations and directory-bound nodes.
* Keep projects and add a second reusable template layer above them.
* Make configurations directory-bound and infer one node per directory.

## Decision Outcome

Chosen option: "Replace projects with global configurations and directory-bound nodes", because it separates reusable VM policy from local runtime identity without adding another hierarchy.

Schema v3 contains configurations, environments, and nodes. A configuration owns image, agent profile, referenced environments, direct bootstrap commands, vCPUs, memory, and disk. The reserved editable `default` configuration is seeded for each home and cannot be renamed or deleted. Creating a configuration copies the current default once.

A node owns its canonical host directory and references the configuration that created it. Creation freezes all configuration-owned values into node and bootstrap metadata; later configuration edits affect future nodes only. Several nodes may use the same directory. A clone requires a new slug and retains the source directory, configuration reference, and frozen state.

The CLI uses `settings`, `environment`, `configuration`, and `node`; it has no project group. The TUI displays flat node rows and scopes a path launch to nodes in that directory and its descendants. Guest and host terminals are node tabs (`node-shell` and `node-host-shell`). Schema-v2 homes are rejected instead of migrated automatically.

### Positive Consequences

* Configurations can be reused across unrelated repositories and directories.
* Node identity and directory scope are explicit and easy to query.
* Existing nodes are reproducible because their effective configuration is immutable.
* The TUI and terminal target model no longer require project rows or project shells.

### Negative Consequences

* Existing schema-v2 homes cannot be opened by the new binary.
* Configuration changes do not roll through to existing nodes automatically.
* Node metadata duplicates effective configuration values intentionally.

## Pros and Cons of the Options

### Global configurations and directory-bound nodes

* Good, because reusable policy and directory identity have separate lifecycles.
* Good, because multiple nodes per directory are natural.
* Good, because directory scoping is a simple containment query.
* Bad, because frozen effective values consume additional metadata and require explicit recreation for updates.

### Projects plus a reusable template layer

* Good, because it could preserve existing project metadata.
* Bad, because users would need to understand both projects and configurations for every node.
* Bad, because it preserves project hierarchy and terminal concepts that no longer add value.

### Directory-bound configurations

* Good, because lookup by current directory is direct.
* Bad, because the same recipe would need to be duplicated across directories.
* Bad, because multiple nodes in one directory would have ambiguous ownership.

## Links

* Supersedes the project/workspace portions of the original schema design.
* Complements ADR 71, which selects the Microsandbox Go SDK as the sole runtime integration.
