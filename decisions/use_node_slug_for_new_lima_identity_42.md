# Use Node Slug for New Lima Identity

## Context and Problem Statement

CodeLima previously generated Lima instance names from the project slug, node slug, and a UUID prefix. That made terminal prompts long and repetitive, especially because Lima defaults guest hostnames to `lima-<instance>`. The question was how new nodes should be named so runtime identity matches the operator-facing node name.

## Decision Drivers

* Keep `limactl` instance names short and recognizable
* Align guest shell prompts with CodeLima node names
* Use Lima-supported template fields only
* Preserve existing node metadata without an implicit migration
* Keep uniqueness checks local to the existing metadata store

## Considered Options

* Continue using project-node-UUID instance names
* Use the normalized node slug for new Lima instance names and guest hostname provisioning
* Migrate all existing nodes to slug-only instance names

## Decision Outcome

Chosen option: "Use the normalized node slug for new Lima instance names and guest hostname provisioning", because node slugs are already globally unique for active nodes and this gives new VMs concise names without rewriting existing Lima instances.

### Positive Consequences

* New `limactl` instance names match the CodeLima node slug.
* Generated Lima templates use a supported system provisioning script to set the guest hostname to the same concise identity, shortening guest prompts.
* Existing nodes continue to use their persisted `lima_instance_name`.

### Negative Consequences

* Long node slugs may still be constrained by Lima hostname limits.
* Existing long-named nodes require explicit recreation or a future migration if operators want shorter names.

## Pros and Cons of the Options

### Continue using project-node-UUID instance names

Keep the previous generated naming scheme.

* Good, because it avoids behavior changes.
* Good, because collisions are extremely unlikely.
* Bad, because prompts remain long and duplicate project/node terms.

### Use the normalized node slug for new Lima instance names and guest hostname provisioning

Generate new runtime identity from the node slug and set the same value with a system provisioning script.

* Good, because it aligns runtime names with user-facing node names.
* Good, because active node slug uniqueness already protects the common collision case.
* Good, because it avoids unsupported Lima YAML fields.
* Bad, because it depends on node slugs staying suitable for Lima instance and hostname use.

### Migrate all existing nodes to slug-only instance names

Rename or recreate existing Lima instances and metadata.

* Good, because every node would use the new concise identity.
* Bad, because live VM renaming is risky and outside the requested scope.
* Bad, because it could orphan existing runtime state if interrupted.

## Links

* Template [ADR_TEMPLATE.md](/Users/brianrackle/projects/codelima/ADR_TEMPLATE.md)
