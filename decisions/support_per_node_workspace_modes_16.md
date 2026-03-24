# Support per-node copied and mounted workspaces

## Context and Problem Statement

CodeLima previously created every node with the same isolated workspace model: no host mount, and a first-start copy of the host workspace into the guest. That keeps guest edits isolated, but it blocks workflows that need a node to work directly against the live host workspace. We need node creation to support both isolated copied workspaces and writable mounted workspaces without forcing that choice at the whole-project level.

## Decision Drivers

* Preserve the existing isolated default behavior
* Support a writable host-backed workflow for selected nodes
* Keep the choice explicit per node so one project can mix both modes
* Preserve clone, shell, and details behavior without inferring mode from live runtime state

## Considered Options

* Keep copy-only workspaces for all nodes
* Add a per-node workspace mode with `copy` and `mounted`
* Make workspace binding a project-level default only

## Decision Outcome

Chosen option: "Add a per-node workspace mode with `copy` and `mounted`", because it preserves the safe existing default while letting operators opt specific nodes into a writable host mount when they need live host/guest sharing.

### Positive Consequences

* Operators can choose isolation or live host sharing per node
* Existing nodes and workflows remain compatible because `copy` stays the default
* Node metadata explicitly records the workspace binding strategy, which keeps clone, shell, and UI rendering stable

### Negative Consequences

* Node metadata and rendering now carry another lifecycle-relevant field
* Mounted nodes can modify the host workspace directly, which is less isolated than the default model
* QA and docs need to cover two workspace behaviors instead of one

## Pros and Cons of the Options

### Keep copy-only workspaces for all nodes

Leave all node creation behavior unchanged and always seed an isolated guest copy.

* Good, because it is simple and already proven
* Good, because all guest edits stay isolated by default
* Bad, because it cannot support live shared-workspace workflows
* Bad, because users must work around the product for host-reflecting edits

### Add a per-node workspace mode with `copy` and `mounted`

Store an explicit workspace mode on each node and render either an empty mount list plus guest copy or a writable Lima mount at the host path.

* Good, because one project can mix isolated and shared nodes
* Good, because the default remains safe and compatible
* Bad, because node creation, clone, and UI flows become slightly more complex
* Bad, because mounted nodes reduce isolation and can surprise users if chosen carelessly

### Make workspace binding a project-level default only

Choose one workspace strategy for all nodes in a project.

* Good, because it simplifies node creation once the project is configured
* Good, because the decision is centralized
* Bad, because it removes useful per-node flexibility
* Bad, because changing the project default later creates mixed expectations for existing and future nodes

## Links

* Refines [defer_project_snapshots_and_runtime_validation_5.md](defer_project_snapshots_and_runtime_validation_5.md)
