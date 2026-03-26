# Command-template-first Lima overrides

## Context and Problem Statement

CodeLima already exposed configurable Lima command templates, but VM resources still lived in typed config and node metadata while node-level command overrides did not exist. That split limited how far advanced operators could push Lima itself, and it made the highest-power runtime surface inconsistent across global, project, and node scopes.

## Decision Drivers

* let operators exploit the full Lima command surface without waiting for new typed flags
* keep override precedence consistent across global, project, and node metadata
* remove the remaining typed resource metadata path now that command templates are the authoritative runtime handoff
* preserve a durable on-disk model for advanced edits

## Considered Options

* keep typed resource metadata and project-only command overrides
* keep project-level command overrides but add only partial node-level command support
* go all-in on command-template overrides with global, project, and node precedence

## Decision Outcome

Chosen option: "go all-in on command-template overrides with global, project, and node precedence", because it gives operators one consistent escape hatch for the full Lima surface and removes the remaining split between typed resource handling and shell command templating.

### Positive Consequences

* global, project, and node metadata now all participate in the same `lima_commands` precedence chain
* default CPU, memory, and disk settings now live directly in the default `create` template instead of separate typed metadata
* node metadata can carry durable overrides for start, stop, shell, copy, workspace seeding, and future create/clone reuse
* first-create node-specific overrides are possible through a YAML file passed to `node create --lima-commands-file`, `node clone --lima-commands-file`, or the matching TUI dialog field

### Negative Consequences

* resource validation is now implicit in the command templates instead of explicit typed fields
* operators now own more responsibility for keeping their custom Lima command templates coherent

## Pros and Cons of the Options

### Keep typed resource metadata and project-only command overrides

Continue splitting resource intent from command templates and keep node metadata out of the override chain.

* Good, because typed resource validation stays explicit
* Good, because the CLI remains narrower
* Bad, because operators still cannot express the full Lima surface through one consistent override model
* Bad, because node-level control stays weaker than project-level control

### Keep project-level command overrides but add only partial node-level command support

Add node metadata overrides for post-create actions only, while leaving initial create and clone behavior outside the node override model.

* Good, because it is smaller than a full architecture shift
* Good, because existing project override behavior remains familiar
* Bad, because node-level `create` and `clone` would remain second-class
* Bad, because the override model would still be inconsistent at the exact moment advanced users most need it

### Go all-in on command-template overrides with global, project, and node precedence

Make `lima_commands` the authoritative Lima runtime surface at all scopes and use pre-create node YAML input files when a node-specific create-time override is needed.

* Good, because the full Lima surface is available through one durable override model
* Good, because precedence is explicit and stable: config, then project, then node
* Bad, because incorrect shell templates fail later than typed field validation would
* Bad, because the CLI, TUI, and docs need to explain the pre-create node override path clearly

## Links

* Supersedes [ADR 19](/Users/brianrackle/personal/codelima/decisions/apply_vm_resources_via_limactl_create_flags_19.md)
* Supersedes [ADR 20](/Users/brianrackle/personal/codelima/decisions/store_only_project_resource_overrides_20.md)
* Refines [ADR 17](/Users/brianrackle/personal/codelima/decisions/project_scoped_lima_command_templates_17.md)
* Refines [ADR 21](/Users/brianrackle/personal/codelima/decisions/configurable_workspace_seed_commands_21.md)
