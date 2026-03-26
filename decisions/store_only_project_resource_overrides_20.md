# Store only project resource overrides

## Context and Problem Statement

CodeLima already moved VM CPU, memory, and disk application out of generated `instance.lima.yaml` and into `limactl create` and `limactl clone` flag expansion. After that change, new `project.yaml` files still persisted `default_resources` even when the values only matched the global defaults from `config.yaml`, which made fresh project metadata look like it owned settings that were really inherited.

## Decision Drivers

* keep project metadata focused on project-specific overrides
* preserve effective VM resource defaults for node creation and clone flows
* simplify the relationship between `config.yaml`, `project.yaml`, and `{{create_resource_flags}}`
* make legacy project files migrate forward without breaking existing nodes

## Considered Options

* keep persisting fully materialized project resource defaults in every `project.yaml`
* remove project-level resource defaults entirely and rely only on global config plus per-node overrides
* store only project resource overrides, then apply global defaults when resolving effective node resources

## Decision Outcome

Chosen option: "store only project resource overrides, then apply global defaults when resolving effective node resources", because it removes redundant inherited values from project metadata without losing per-project resource customization or the runtime flag expansion path.

### Positive Consequences

* brand new projects no longer write `default_resources` when they inherit the global defaults unchanged
* legacy project files that only duplicated global defaults normalize forward to the override-only shape
* project metadata can now represent partial overrides, such as only overriding CPU while inheriting global memory and disk defaults
* node creation and clone still receive full effective resource values through the existing typed metadata plus `{{create_resource_flags}}` and `{{clone_resource_flags}}`

### Negative Consequences

* `project show` and `project.yaml` now surface only project-local resource overrides, not the fully materialized effective defaults
* effective project defaults are one step more indirect because they are resolved from `config.yaml` plus project overrides at runtime

## Pros and Cons of the Options

### Keep persisting fully materialized project resource defaults in every `project.yaml`

Continue storing CPU, memory, and disk on each project even when they simply mirror the global defaults.

* Good, because every project file shows a fully expanded effective resource set
* Good, because effective values do not change if global defaults are edited later
* Bad, because fresh project metadata duplicates inherited state
* Bad, because `project.yaml` looks authoritative for values that are really global defaults

### Remove project-level resource defaults entirely and rely only on global config plus per-node overrides

Eliminate project-scoped resource defaults and keep only global config plus node-specific requests.

* Good, because the storage model becomes simpler
* Good, because there is a single place for default VM sizing
* Bad, because projects lose a useful customization point
* Bad, because existing CLI flags and workflows for project-level sizing would no longer map cleanly to stored metadata

### Store only project resource overrides, then apply global defaults when resolving effective node resources

Persist only the fields that differ from `config.yaml`, then apply defaults when computing the node's requested resources.

* Good, because project metadata now cleanly represents only project-local intent
* Good, because partial overrides can inherit future global changes for untouched dimensions
* Good, because runtime resource handoff still uses the existing typed metadata and Lima flag placeholders
* Bad, because operators need `config show` plus `project show` to see the fully effective project resource picture

## Links

* Refines [ADR 19](/Users/brianrackle/personal/codelima/decisions/apply_vm_resources_via_limactl_create_flags_19.md)
