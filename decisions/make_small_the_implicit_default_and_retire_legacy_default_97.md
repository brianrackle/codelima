# Make Small the Implicit Default and Retire the Legacy Default Configuration

## Context and Problem Statement

After adding size-based configurations, the separately named `default` configuration remained visible and retained its older 2-vCPU, 4-GiB, 20-GiB resources. This created five choices and made the relationship between `default` and the new sizes ambiguous. How should CodeLima expose only the size vocabulary while preserving existing nodes created from `default`?

## Decision Drivers

* Configuration lists and selectors should contain only meaningful size names.
* Omitting a configuration should choose the smallest built-in size.
* Existing nodes must retain frozen resources and a resolvable historical association.
* The implicit default must remain editable but protected from rename and deletion.
* Homes that deleted `small` while it was optional must still gain a live implicit default.
* The former `default` slug must not be reusable.

## Considered Options

* Make `small` the implicit default and soft-retire the former `default` record.
* Rename or rebind every existing `default` node to `small`.
* Keep both `default` and the four size configurations.

## Decision Outcome

Chosen option: "Make `small` the implicit default and soft-retire the former `default` record", because it removes the redundant live choice without changing the frozen values or stored configuration IDs of existing nodes.

Fresh homes seed only `small`, `medium`, `large`, and `xlarge`. The `small` configuration supplies defaults for node creation and configuration creation, and it cannot be renamed or deleted. If an older home soft-deleted `small` while it was an optional preset, seed and repair restore that record in place without overwriting its customized values. On upgraded homes, seed and repair soft-delete the former `default` configuration and reserve its slug permanently. Existing nodes still resolve that tombstoned record by ID and continue to display their historical `default` association with unchanged frozen runtime values.

### Positive Consequences

* CLI and TUI lists contain exactly four naturally ordered size configurations.
* New nodes use 1 vCPU, 1 GiB memory, and 10 GiB disk unless another configuration is selected.
* Existing nodes created from `default` keep their resource values and metadata association.
* Configuration creation consistently copies `small`.
* Every upgraded home has a live configuration for the implicit default.
* The legacy slug cannot unexpectedly reappear.

### Negative Consequences

* Existing nodes may still display the historical label `default` even though it is absent from live configuration lists.
* An edited legacy `default` configuration becomes hidden after upgrade.
* A `small` configuration deleted before it became the default is restored, although its customized values are retained.
* `small` is now protected and cannot be deleted like the other size configurations.

## Pros and Cons of the Options

### Make `small` the implicit default and soft-retire the former `default` record

* Good, because live product vocabulary contains only size names.
* Good, because tombstoning preserves ID lookup for existing nodes.
* Good, because no node metadata or runtime values need rewriting.
* Bad, because hidden historical metadata remains until its nodes are removed.

### Rename or rebind every existing `default` node to `small`

* Good, because every node would display a live configuration name.
* Bad, because nodes with 2/4/20 or customized frozen values would misleadingly appear to have been created from the 1/1/10 `small` configuration.
* Bad, because rewriting durable node associations creates unnecessary migration risk.

### Keep both `default` and the four size configurations

* Good, because no migration behavior is required.
* Bad, because users continue to see an unexplained fifth choice.
* Bad, because implicit selection remains disconnected from the size vocabulary.

## Links

* Supersedes the reserved `default` portions of [Replace projects with directory-bound nodes and reusable configurations](replace_projects_with_directory_bound_nodes_and_reusable_configurations_72.md)
* Supersedes the five-configuration outcome in [Seed built-in configuration size presets](seed_built_in_configuration_size_presets_96.md)
