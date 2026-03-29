# Copy-mode workspace airlock

## Context and Problem Statement

CodeLima's `copy` workspace mode gives users strong VM isolation, but until now it had no deliberate path for bringing sandboxed guest changes back to the host. A simple "copy guest tree over host tree" flow would break the isolation story because it could silently overwrite host edits that happened after the VM was seeded.

## Decision Drivers

* preserve `copy` mode as a safe default instead of turning it into a hidden mounted workflow
* give users an explicit guest-to-host promotion path that does not require Git history or project lineage
* detect host drift since the VM was seeded and fail safely instead of overwriting newer host edits
* keep Lima runtime customization aligned with the existing command-template override model

## Considered Options

* tell users to switch to `mounted` mode whenever they need files back on the host
* copy the current guest tree directly over the host workspace
* add a node-scoped workspace airlock that diffs guest changes against a recorded seed snapshot and applies them with conflict checks

## Decision Outcome

Chosen option: "add a node-scoped workspace airlock that diffs guest changes against a recorded seed snapshot and applies them with conflict checks", because it preserves the safety boundary of `copy` mode while still giving users an explicit, automatable way to promote sandboxed work back to the host.

### Positive Consequences

* `copy` mode now has a first-class return path for guest changes through `node sync`
* host edits made after the guest was seeded become apply conflicts instead of silent overwrites
* successful sync applies refresh the node's recorded seed snapshot so future syncs stay incremental
* Lima guest-to-host transfer remains customizable through the same `lima_commands` mechanism as the rest of the runtime boundary

### Negative Consequences

* node metadata now has to track a workspace-seed snapshot id for copy-mode safety
* CodeLima now relies on transient staging work under `CODELIMA_HOME/_tmp` during sync and patch operations
* nodes created before this feature may not have a usable seed snapshot for `node sync`

## Pros and Cons of the Options

### tell users to switch to `mounted` mode whenever they need files back on the host

Treat the problem as a workflow choice and leave `copy` mode one-way.

* Good, because it avoids adding new runtime or metadata behavior
* Good, because mounted mode already keeps host and guest aligned
* Bad, because it gives up the strongest isolation mode whenever the user wants results back
* Bad, because it makes `copy` mode much less useful for real agent workflows

### copy the current guest tree directly over the host workspace

Pull the VM tree and promote it over the host without a preserved merge base.

* Good, because it is simple to explain and implement
* Good, because it does not require snapshot bookkeeping
* Bad, because it can silently clobber host edits that happened after the node was seeded
* Bad, because it makes `copy` mode feel unsafe at the moment users care about their data most

### add a node-scoped workspace airlock that diffs guest changes against a recorded seed snapshot and applies them with conflict checks

Capture a seed snapshot when a copy-mode node is first populated, pull the current guest tree when the user runs `node sync`, build a patch from the seed snapshot to the guest tree, and apply that patch onto the current host workspace only if it still applies cleanly.

* Good, because it preserves copy-mode isolation while still letting users bring work back intentionally
* Good, because it turns host drift into an explicit conflict instead of silent data loss
* Bad, because it adds snapshot lifecycle state to node metadata
* Bad, because it needs scratch staging and a guest-to-host copy boundary in addition to the existing host-to-guest seed path

## Links

* Refines [support_per_node_workspace_modes_16.md](support_per_node_workspace_modes_16.md)
