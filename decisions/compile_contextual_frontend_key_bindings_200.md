# Compile contextual frontend key bindings

## Context and Problem Statement

The Help design promises `settings.yaml › keys`, but fixed shortcuts were spread
across manager, detail and overlay controllers. Help, footer and button strings
duplicated those shortcuts, and several mouse controls simulated a default key.
Replacing physical events would also risk sending a shortcut's repeat or release
into a newly focused input or embedded shell.

## Decision Drivers

* Preserve printable case, native text editing and focused terminal payload.
* Validate collisions against actual, mutually exclusive focused surfaces.
* Keep destructive reviews, captured identities and action guards authoritative.
* Keep effective dispatch and displayed hints consistent after settings changes.
* Recover invalid frontend preferences without disabling daemon/lifecycle CLI.

## Considered Options

* Translate configured events into the former default keys.
* Compile an immutable action/context registry and dispatch semantic action IDs.
* Let each widget parse independent settings and render independent hints.

## Decision Outcome

Choose the shared immutable registry. Store only authored overrides in
`_config/settings.yaml`, keyed by stable dotted action IDs. Compile strict named
keys, Unicode characters and modifiers into context indexes. Omitted actions
inherit defaults; empty lists disable optional actions. Required quit, cancel
and focus actions retain bindings. Reject overlapping contexts, equivalent
aliases, unknown IDs/spellings and printable shortcuts that consume text entry.

Resolve actions without rewriting `vaxis.Key`. Existing press/repeat guards and
physical key claims continue to own the original gesture across transitions and
map replacement. Pointer controls invoke semantic actions directly. Text widgets
retain editing keys; raw attachment keeps its separate fixed Ctrl+] contract.
Only configured focus bindings are intercepted from an embedded shell.

Settings writes validate the complete prospective map under the existing lock
and preserve comments and unknown fields. Inspection reports defaults, effective
bindings and validation errors. Reset can remove invalid or unknown overrides
without compiling them first, while refusing malformed YAML. Config decoding
records errors confined to `keys` separately so unrelated commands still start.
The initial frontend falls back to defaults; invalid refreshes retain its last
valid map and report a deduplicated diagnostic. Refresh performs no event-path
disk reads. Help and legend rendering consume the same effective snapshot.

### Positive Consequences

* Rebinding does not bypass captured reviews or change physical input ownership.
* Disabled shortcuts leave mouse and palette actions available.
* Strict parsing prevents unknown names silently becoming a first-character key.
* Optional frontend settings need no schema, daemon or renderer protocol bump.

### Negative Consequences

* Every new shortcut must declare its actual contexts and update display metadata.
* Terminal protocols can collapse aliases; validation conservatively treats
  common Ctrl+i/m/[/h aliases as Tab/Enter/Esc/Backspace.
* Keyboard transport and emulator behavior still require native qualification;
  unit tests cannot establish every host's delivery behavior.

## Pros and Cons of the Options

Semantic action dispatch preserves input and separates pointer invocation from
keyboard configuration, at the cost of migrating every dispatcher. Synthetic
default events would be smaller initially but obscure modifier intent and
gesture identity. Independent widget maps would simplify local edits but allow
collisions, recovery behavior and visible help to diverge.

## Links

* [Specification](../plans/KEY_BINDINGS_PLAN.md)
* [Contextual help](use_contextual_read_only_key_help_192.md)
