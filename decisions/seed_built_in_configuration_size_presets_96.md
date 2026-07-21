# Seed Built-In Configuration Size Presets

## Context and Problem Statement

CodeLima previously seeded only the reserved `default` configuration, so users had to create common VM resource sizes before they could select them from the CLI or TUI. How should `small`, `medium`, `large`, and `xlarge` configurations become available out of the box without creating separate behavior for built-in and user-created records?

## Decision Drivers

* Fresh homes should expose useful resource sizes immediately.
* Existing schema-v4 homes should gain presets without a schema migration.
* CLI and TUI configuration lists and selectors must use the same persisted records.
* User edits and soft-deletions must remain authoritative.
* T-shirt sizes should be presented in natural size order.

## Considered Options

* Seed normal persisted configurations during the versioned store repair pass.
* Generate synthetic presets only in CLI and TUI read paths.
* Document commands for users to create the presets manually.

## Decision Outcome

Chosen option: "Seed normal persisted configurations during the versioned store repair pass", because it makes the presets available through every existing configuration workflow while preserving the store as the single source of truth.

The seeded resource sizes are:

| Slug | vCPUs | Memory MiB | Disk MiB |
| --- | ---: | ---: | ---: |
| `small` | 1 | 1024 | 10240 |
| `medium` | 4 | 8192 | 51200 |
| `large` | 6 | 16384 | 76800 |
| `xlarge` | 8 | 32768 | 102400 |

Each preset starts with the configured default image and agent profile plus the built-in `codex` and `claude-code` environments. Only the reserved `default` configuration retains rename/delete protection; size presets otherwise behave like ordinary configurations.

### Positive Consequences

* Fresh and existing schema-v4 homes receive all four presets.
* Configuration list and selector consumers need no synthetic-data branches.
* Presets can be inspected, edited, cloned, referenced, and deleted through existing workflows.
* Lists place `default` first, followed by `small`, `medium`, `large`, and `xlarge`, then user configurations alphabetically.
* Customized and soft-deleted presets remain untouched on later repair passes.

### Negative Consequences

* The built-in slugs become part of the long-lived product vocabulary.
* Seed and repair now own another set of persisted records.
* A new home contains five configurations instead of only `default`.

## Pros and Cons of the Options

### Seed normal persisted configurations during the versioned store repair pass

Store each preset exactly like a user configuration when its slug has no live or deleted record. Bump the seed revision so existing schema-v4 homes run the additive pass once.

* Good, because every product surface reads the same records.
* Good, because normal update, clone, reference, and delete rules continue to apply.
* Good, because tombstones naturally prevent deleted presets from being resurrected.
* Bad, because seeding must maintain stable preset slugs and defaults.

### Generate synthetic presets only in CLI and TUI read paths

Merge hardcoded rows into list and selector results without storing them.

* Good, because no persisted records are added during repair.
* Bad, because mutation and reference flows would need special handling.
* Bad, because CLI, TUI, and direct service callers could disagree.

### Document commands for users to create the presets manually

Leave store behavior unchanged and publish four `configuration create` examples.

* Good, because it adds no seeding logic.
* Bad, because the presets are not actually available out of the box.
* Bad, because every home repeats the same setup.

## Links

* Refines [Replace projects with directory-bound nodes and reusable configurations](replace_projects_with_directory_bound_nodes_and_reusable_configurations_72.md)
* Refines [Default new nodes to mounted workspaces with coding agents](default_new_nodes_to_mounted_workspaces_with_coding_agents_76.md)
* Superseded in part by [Make small the implicit default and retire the legacy default configuration](make_small_the_implicit_default_and_retire_legacy_default_97.md)
