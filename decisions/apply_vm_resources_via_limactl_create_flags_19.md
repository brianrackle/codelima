# Apply VM resources via `limactl create` flags

## Context and Problem Statement

CodeLima already stores requested CPU, memory, and disk values in project and node metadata, but node creation also wrote those values into the generated `instance.lima.yaml`. That duplicated the same settings across metadata, generated YAML, and the `limactl` command surface, and it made the advanced global `create` template less representative of the full Lima invocation that actually matters at runtime.

## Decision Drivers

* keep VM resource settings aligned with the configurable `limactl create` command surface
* avoid duplicating the same resource values in generated Lima YAML and CodeLima metadata
* preserve per-project defaults and per-node overrides for requested resources
* keep generated templates focused on template content such as mounts and bootstrap metadata

## Considered Options

* keep injecting CPU, memory, and disk into generated `instance.lima.yaml`
* remove typed resource handling and hardcode resource values directly in the global `create` command template
* keep typed resource metadata, but expand it into `limactl create` flags through the configurable create command template

## Decision Outcome

Chosen option: "keep typed resource metadata, but expand it into `limactl create` flags through the configurable create command template", because it preserves project and node resource overrides while making the actual Lima create command, rather than generated YAML, the authoritative runtime handoff point.

### Positive Consequences

* generated `instance.lima.yaml` files no longer carry `cpus`, `memory`, or `disk` keys copied from CodeLima metadata
* the default global `create` command template now documents the resource flag path explicitly through `{{create_resource_flags}}`
* project defaults, node create overrides, and clone overrides still work without inventing separate static command strings for each resource combination

### Negative Consequences

* advanced custom `create` templates that omit `{{create_resource_flags}}` will also omit CodeLima-managed resource overrides
* resource application is now one step more indirect because it depends on placeholder expansion inside shell command templates

## Pros and Cons of the Options

### Keep injecting CPU, memory, and disk into generated `instance.lima.yaml`

Continue mutating the rendered template with CodeLima-managed resource keys before calling `limactl create`.

* Good, because the generated template shows all requested VM settings in one file
* Good, because `limactl create` command templates stay simpler
* Bad, because the runtime resource handoff is split between command templates and YAML mutation
* Bad, because generated templates duplicate state that already exists in CodeLima metadata

### Remove typed resource handling and hardcode resource values directly in the global `create` command template

Move the default resource values entirely into `config.yaml` command strings.

* Good, because the runtime command becomes fully explicit in one place
* Good, because no placeholder expansion is needed for resource flags
* Bad, because project defaults and per-node overrides would be much harder to represent
* Bad, because typed validation and structured metadata for requested resources would be weakened

### Keep typed resource metadata, but expand it into `limactl create` flags through the configurable create command template

Persist requested resources as structured metadata, then expand them into `{{create_resource_flags}}` during `limactl create`.

* Good, because resource intent stays typed and validated in metadata
* Good, because the actual Lima runtime handoff now happens through command flags instead of generated YAML mutation
* Good, because the same resource values can still drive clone overrides through `{{clone_resource_flags}}`
* Bad, because operators customizing the command template need to preserve the resource placeholder when they want CodeLima-managed overrides

## Links

* Refines [ADR 17](/Users/brianrackle/personal/codelima/decisions/project_scoped_lima_command_templates_17.md)
* Refines [ADR 18](/Users/brianrackle/personal/codelima/decisions/global_lima_command_defaults_with_project_overrides_18.md)
