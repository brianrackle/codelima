# Store Lima Actions As Command Lists And Move Project Bootstrap Under `lima_commands.bootstrap`

## Context and Problem Statement

CodeLima already exposed Lima runtime customization through `lima_commands`, but each action accepted only a single command string while project bootstrap still lived in separate `environment_commands` metadata. That split made the configuration surface inconsistent and prevented operators from expressing multi-step Lima actions or keeping all bootstrap sequencing in one override hierarchy.

## Decision Drivers

* Lima overrides should support multi-step actions without hardcoding wrapper scripts in Go.
* Project, node, and global bootstrap behavior should live under one precedence model.
* New metadata writes should stop emitting the older `environment_commands` shape.
* Existing metadata should remain readable so upgrades do not require manual migration.

## Considered Options

* Keep single-string `lima_commands` entries and retain separate bootstrap metadata.
* Add a second multi-command runtime block just for bootstrap while keeping other Lima actions scalar.
* Make every `lima_commands` action an ordered list and move project bootstrap to `lima_commands.bootstrap`.

## Decision Outcome

Chosen option: "Make every `lima_commands` action an ordered list and move project bootstrap to `lima_commands.bootstrap`", because it unifies runtime customization under one precedence model, supports multi-step actions consistently, and removes the last project bootstrap path that lived outside the Lima command surface.

### Positive Consequences

* Global, project, and node Lima overrides now share one consistent list-based schema.
* First-start bootstrap can be customized through the same precedence chain as other Lima actions.
* Reusable environment configs still compose into node bootstrap, but their stored command field is now explicitly named `bootstrap_commands`.
* Existing metadata remains loadable through legacy read compatibility for scalar command fields and old bootstrap field names.

### Negative Consequences

* The metadata schema becomes broader, so docs and tests must spell out list semantics clearly.
* Project bootstrap overrides now live deeper under `lima_commands`, which is a migration cost for operators editing metadata by hand.
* Runtime execution needs special handling for list-based `template_copy` and interactive `shell` actions.

## Pros and Cons of the Options

### Keep single-string `lima_commands` entries and retain separate bootstrap metadata

Keep the existing scalar Lima command templates and top-level project bootstrap commands.

* Good, because it avoids metadata churn.
* Good, because it keeps runtime execution logic simple.
* Bad, because it cannot express multi-step Lima actions.
* Bad, because bootstrap remains outside the authoritative Lima override surface.

### Add a second multi-command runtime block just for bootstrap while keeping other Lima actions scalar

Introduce a new bootstrap-specific list while leaving other Lima actions unchanged.

* Good, because it would solve bootstrap sequencing directly.
* Good, because it would minimize churn for existing non-bootstrap overrides.
* Bad, because it preserves two different override models for closely related runtime actions.
* Bad, because operators still cannot express multi-step `create`, `start`, `clone`, or `copy` actions.

### Make every `lima_commands` action an ordered list and move project bootstrap to `lima_commands.bootstrap`

Store all Lima actions as ordered command lists and resolve project bootstrap through `lima_commands.bootstrap`.

* Good, because it gives every Lima action the same data shape and execution model.
* Good, because it lets bootstrap participate in the existing global -> project -> node override chain.
* Good, because it keeps reusable environment config bootstrap commands composable without a second project-specific field.
* Bad, because it requires test, documentation, and metadata fixture updates across the repo.

## Links

* Refined by [project_scoped_lima_command_templates_17.md](project_scoped_lima_command_templates_17.md)
* Refined by [global_lima_command_defaults_with_project_overrides_18.md](global_lima_command_defaults_with_project_overrides_18.md)
* Refined by [configurable_workspace_seed_commands_21.md](configurable_workspace_seed_commands_21.md)
* Refined by [command_template_first_lima_overrides_22.md](command_template_first_lima_overrides_22.md)
