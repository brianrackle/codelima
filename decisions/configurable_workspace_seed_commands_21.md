# Configurable workspace seed commands

## Context and Problem Statement

Copy-mode nodes already allowed `lima_commands.copy` overrides for the host-to-guest transfer itself, but the guest-side directory preparation step was still hardcoded in Go. Projects that needed a different ownership, cleanup, or directory-creation strategy for seeded workspaces had no durable way to change that behavior without patching CodeLima.

## Decision Drivers

* keep copy-mode workspace seeding configurable through the same metadata surface as other Lima-related commands
* preserve the existing default seed behavior for projects that do not opt in
* avoid adding a second configuration namespace for one adjacent step in the same workflow
* keep global defaults and project overrides consistent

## Considered Options

* keep the guest workspace prepare command hardcoded and only expose `lima_commands.copy`
* add a separate workspace-seed configuration block outside `lima_commands`
* extend `lima_commands` with a `workspace_seed_prepare` template

## Decision Outcome

Chosen option: "extend `lima_commands` with a `workspace_seed_prepare` template", because it keeps the full copy-mode seed flow configurable through the same global-default/project-override mechanism that already exists for the transfer command.

### Positive Consequences

* projects can now override both the guest-side workspace preparation step and the host-to-guest copy step
* operators only need to learn one durable metadata surface for advanced VM command customization
* the default behavior stays unchanged for existing configs that do not set the new field

### Negative Consequences

* `lima_commands` now contains one template that is executed inside the guest shell rather than as a direct `limactl` invocation
* malformed `workspace_seed_prepare` templates still fail at runtime when the seed flow runs
* the config and project metadata comment backfill logic has another field to maintain

## Pros and Cons of the Options

### Keep the guest workspace prepare command hardcoded and only expose `lima_commands.copy`

Continue mixing a fixed guest-side step with a configurable transfer step.

* Good, because the runtime surface stays slightly smaller
* Good, because there is no metadata migration or documentation work
* Bad, because projects cannot fully customize copy-mode seeding
* Bad, because adjacent parts of one workflow would keep using inconsistent configuration rules

### Add a separate workspace-seed configuration block outside `lima_commands`

Create a new metadata section just for copy-mode workspace seeding.

* Good, because the prepare step could be modeled more explicitly
* Good, because it would avoid stretching the meaning of `lima_commands`
* Bad, because operators would need to inspect and understand two configuration namespaces for one workflow
* Bad, because global-default/project-override precedence would be duplicated for little gain

### Extend `lima_commands` with a `workspace_seed_prepare` template

Store the guest-side prepare command next to the existing `copy` template under `lima_commands`.

* Good, because the whole copy-mode seed flow is configurable in one place
* Good, because existing config precedence and project comment examples can be reused
* Bad, because one field in `lima_commands` is now guest-shell-oriented instead of a direct `limactl` command
* Bad, because placeholder validation still happens at runtime instead of at metadata edit time

## Links

* Refines [ADR 18](/Users/brianrackle/personal/codelima/decisions/global_lima_command_defaults_with_project_overrides_18.md)
* Refines [ADR 16](/Users/brianrackle/personal/codelima/decisions/support_per_node_workspace_modes_16.md)
