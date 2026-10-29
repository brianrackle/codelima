# Keep fleet resource columns during setup failures

## Context and Problem Statement

Failed or in-progress setup replaced the CPU-through-end table cells with one
message while their metric headers remained visible. Compact rows also lost
their template cell. Users read `setup failed`, `Enter details` and `l logs` as
CPU, memory and disk values, and running VMs lost available observations.

## Decision Drivers

* Preserve the meaning of every table header in every lifecycle state.
* Keep observed metrics and unavailable readings visible during failed setup.
* Retain failure evidence, live progress and shortcut discoverability.

## Considered Options

* Keep replacing resource cells with lifecycle text.
* Add variable-height evidence rows for each node.
* Keep all table cells and show selected-node context below the table.

## Decision Outcome

Chosen option: "Keep all table cells and show selected-node context below the
table", because it preserves column meaning and the existing one-row viewport,
selection and mouse model.

The selected-node summary carries the same operation/evidence precedence in
compact and wide layouts. Wide layouts use the existing gap between table and
preview. Healthy nodes keep the existing preview or compact uptime/ports summary.
Marked-node summaries continue to take priority. Status and Setup remain separate.
No daemon, telemetry, storage or lifecycle behavior changes.

### Positive Consequences

* Resource cells remain observations or explicit `—` values.
* Template, session, directory/port and uptime cells remain available.
* New operations still supersede retained failure evidence.

### Negative Consequences

* Detailed operation text is shown for the selected node. Other rows retain
  Status/Setup; Activity continues to list concurrent operations.
* Long context is clipped to the available width, as other summaries are.

## Pros and Cons of the Options

### Keep replacing resource cells with lifecycle text

* Good, because several failures can show their text at once.
* Bad, because headings become misleading and valid metrics disappear.

### Add variable-height evidence rows for each node

* Good, because every node can retain both metrics and detailed context.
* Bad, because it reduces fleet capacity and changes scrolling and hit testing.

### Keep all table cells and show selected-node context below the table

* Good, because all columns and the existing viewport retain their meaning.
* Bad, because users select a node to see its detailed operation context.

## Links

* Refines [ADR 208](separate_vm_status_from_setup_presentation_208.md).
