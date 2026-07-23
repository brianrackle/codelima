# Split XSmall from the Small Default

## Context and Problem Statement

The original `small` preset combined the implicit-default role with the minimum 1-vCPU, 1-GiB, 10-GiB footprint. The desired size vocabulary now needs `xsmall` for that minimum footprint and a more practical 2-vCPU, 4-GiB, 25-GiB `small`, while `small` remains the protected implicit default. How should fresh and existing homes adopt the new presets without rewriting existing nodes or overwriting user customizations?

## Decision Drivers

* `small` must remain the implicit default selected when no configuration is supplied.
* The minimum 1/1/10 footprint must remain available as `xsmall`.
* Existing nodes must retain their frozen CPU, memory, and disk values.
* Customized `small` configurations must not be overwritten during upgrade.
* Fresh and upgraded homes must present the same natural size ordering.

## Considered Options

* Add `xsmall`, resize the existing `small` record in place only when it still matches the untouched legacy preset, and preserve customized records.
* Rename the existing `small` record to `xsmall` and create a new `small` record.
* Make `xsmall` the implicit default and leave `small` unchanged.

## Decision Outcome

Chosen option: "Add `xsmall` and conditionally resize `small` in place", because it preserves the established default slug and configuration identity while giving fresh and unmodified upgraded homes the requested resources.

Fresh homes seed `xsmall` at 1 vCPU, 1 GiB RAM, and 10 GiB disk, followed by `small` at 2 vCPU, 4 GiB RAM, and 25 GiB disk. `small` remains protected from rename and deletion; `xsmall` is an ordinary editable and deletable built-in configuration.

Seed revision 4 adds `xsmall` to existing homes. It updates `small` in place only when its resources, image, agent profile, environments, and bootstrap commands still match the former stock preset. Any customization leaves the existing `small` record untouched. Existing nodes are not rewritten and retain the resources frozen when they were created.

### Positive Consequences

* The selector offers `xsmall`, `small`, `medium`, `large`, and `xlarge` in ascending order.
* Omitting a configuration continues to choose `small`, now with 2/4/25 resources.
* Existing node runtime sizing does not change.
* Customized defaults survive seed and repair.
* Untouched homes upgrade without manual configuration edits.

### Negative Consequences

* An existing node created from the old `small` may retain 1/1/10 while displaying its historical `small` association.
* A user customization that exactly reproduces every former stock field is indistinguishable from an untouched preset and will be migrated.
* The seed pass owns one more targeted historical migration rule.

## Pros and Cons of the Options

### Add `xsmall` and conditionally resize `small` in place

* Good, because the stable default slug and configuration ID remain unchanged.
* Good, because frozen nodes require no migration.
* Good, because content matching protects customized records.
* Bad, because exact stock-equivalent customizations cannot be detected.

### Rename the existing `small` record to `xsmall`

* Good, because old minimum-size records would retain their resource/name relationship.
* Bad, because existing nodes would point at a renamed configuration and display a changed association.
* Bad, because `small` identity and default semantics would be replaced rather than evolved.

### Make `xsmall` the implicit default

* Good, because the smallest footprint remains the no-argument choice.
* Bad, because it reverses the prior decision that `small` is the protected default.
* Bad, because it does not deliver the requested new `small` resources as the default.

## Links

* Refines [Make Small the Implicit Default and Retire the Legacy Default Configuration](make_small_the_implicit_default_and_retire_legacy_default_97.md)
* Refines [Seed Built-In Configuration Size Presets](seed_built_in_configuration_size_presets_96.md)
