# Global Lima command defaults with project overrides

## Context and Problem Statement

Project-scoped Lima command templates made advanced overrides flexible, but brand new projects did not show any `lima_commands` block until an operator discovered the feature in the docs or TUI copy. The default command set itself also lived only in Go code, which made the authoritative application-wide values harder to inspect and edit.

## Decision Drivers

* make the default Lima command templates discoverable on disk
* preserve per-project overrides for repos that need custom `limactl` flags
* keep the runtime precedence explicit and stable
* help operators learn the project override shape without reading external docs first

## Considered Options

* keep the defaults hardcoded in Go and only document project-local overrides
* move all Lima command templates to global config with no project-level overrides
* store global defaults in `config.yaml` and let projects override individual commands in `project.yaml`

## Decision Outcome

Chosen option: "store global defaults in `config.yaml` and let projects override individual commands in `project.yaml`", because it keeps one authoritative on-disk default set for the application while preserving the existing project-scoped escape hatch.

### Positive Consequences

* operators can inspect and edit the global Lima command defaults in `CODELIMA_HOME/_config/config.yaml`
* projects still override only the commands they need under `lima_commands`
* project metadata now shows a commented example override block even before a project opts in
* runtime precedence is clearer: built-in fallback, then global config, then project override

### Negative Consequences

* metadata writes now manage a more opinionated YAML shape, including comment backfill
* the application may rewrite legacy metadata files to add the new documented command sections
* there are now two places to inspect when diagnosing a custom Lima invocation

## Pros and Cons of the Options

### Keep the defaults hardcoded in Go and only document project-local overrides

Continue using built-in defaults with optional project metadata overrides.

* Good, because the implementation stays simpler
* Good, because there is no metadata migration or backfill work
* Bad, because the real default command set stays hidden from operators
* Bad, because new project files still give no in-place hint about the override format

### Move all Lima command templates to global config with no project-level overrides

Require every project to inherit the same Lima command set.

* Good, because the command source of truth becomes singular
* Good, because the runtime precedence becomes simpler
* Bad, because a single repo can no longer opt into special `limactl` flags without changing every project
* Bad, because it removes the flexibility that motivated the original project-scoped design

### Store global defaults in `config.yaml` and let projects override individual commands in `project.yaml`

Persist the application-wide defaults in `CODELIMA_HOME/_config/config.yaml`, then resolve per-project overrides from `CODELIMA_HOME/projects/<project-id>/project.yaml`.

* Good, because the authoritative defaults become visible and editable on disk
* Good, because project-local customization still works without expanding the CLI or TUI
* Good, because commented project metadata examples improve discoverability for first-time operators
* Bad, because the storage layer now owns some YAML comment rendering

## Links

* Refines [ADR 17](/Users/brianrackle/personal/codelima/decisions/project_scoped_lima_command_templates_17.md)
