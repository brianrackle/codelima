# Surface and clean incomplete node metadata directories

## Context and Problem Statement

Older failed `node create` attempts could leave partial directories under `CODELIMA_HOME/nodes/` without a `node.yaml`. The runtime was updated to ignore those directories during normal node enumeration, but operators still needed a supported way to discover and remove the stale metadata instead of reverse-engineering the on-disk store.

## Decision Drivers

* Operators need a supported repair path for existing broken homes.
* Normal CLI and TUI reads should stay resilient when stale partial node directories exist.
* Cleanup should be safe by default and scriptable.

## Considered Options

* Ignore incomplete node directories silently and rely on manual filesystem cleanup.
* Surface incomplete node directories in `doctor` and add a dry-run-first cleanup command.
* Treat incomplete node directories as hard errors that block startup until they are removed.

## Decision Outcome

Chosen option: "Surface incomplete node directories in `doctor` and add a dry-run-first cleanup command", because it keeps the runtime resilient while giving operators an explicit discovery and repair workflow.

### Positive Consequences

* `doctor` now explains why startup recovered from older failed node creation attempts.
* `node cleanup-incomplete` gives operators a supported way to inspect and remove stale partial metadata.
* The cleanup workflow is non-destructive by default and only removes metadata after an explicit `--apply`.

### Negative Consequences

* The store now has another health-reporting path to maintain.
* Cleanup output can usually recover only the node-directory id and any saved Lima instance name, not the full node slug or project slug.
* Operators still need to decide whether the reported directories are safe to remove before applying cleanup.

## Pros and Cons of the Options

### Ignore incomplete node directories silently and rely on manual filesystem cleanup

Recover at runtime, but do not surface the issue through supported commands.

* Good, because the implementation is minimal.
* Good, because normal reads stay resilient.
* Bad, because operators do not learn why their home was broken.
* Bad, because cleanup remains an undocumented filesystem exercise.

### Surface incomplete node directories in `doctor` and add a dry-run-first cleanup command

Teach `doctor` to warn on missing-`node.yaml` directories and add `node cleanup-incomplete` with explicit `--apply`.

* Good, because it gives operators a supported diagnosis and repair flow.
* Good, because the command is safe by default and easy to automate.
* Bad, because it adds more CLI and store-health surface area.
* Bad, because partial directories rarely contain enough metadata to recover full slugs.

### Treat incomplete node directories as hard errors that block startup until they are removed

Refuse to list nodes, build trees, or start the TUI while incomplete directories exist.

* Good, because it forces operators to resolve store corruption immediately.
* Good, because it avoids silently skipping any metadata.
* Bad, because it recreates the original user-facing failure mode.
* Bad, because one stale directory can block unrelated healthy projects and nodes.
