# Project-scoped Lima command templates

## Context and Problem Statement

Some projects need non-default Lima flags during VM lifecycle operations, for example a different VM type or nested virtualization settings on `limactl start`. The previous design hardcoded those invocations in Go and exposed direct project command editing in the TUI, which made advanced per-project runtime overrides impossible without adding more UI surface for every Lima operation.

## Decision Drivers

* support project-specific `limactl` flags without forking CodeLima
* keep advanced runtime overrides durable in project metadata
* avoid expanding the TUI into a full editor for every project-local command list
* preserve existing defaults for projects that do not opt in

## Considered Options

* keep hardcoded Lima invocations and add more TUI controls
* add dedicated CLI flags for every overridable Lima operation
* store project-scoped Lima command templates in project metadata and treat them as manual-edit advanced settings

## Decision Outcome

Chosen option: "store project-scoped Lima command templates in project metadata and treat them as manual-edit advanced settings", because it supports the full range of per-project Lima customization without growing the CLI and TUI around every possible `limactl` flag combination.

### Positive Consequences

* projects can override `template copy`, `create`, `start`, `stop`, `clone`, `delete`, `copy`, and `shell` independently
* the overrides live with the rest of the project metadata and survive CLI or TUI updates
* the TUI can stay focused on navigation and common mutations while still showing the exact metadata file path operators need for advanced edits

### Negative Consequences

* advanced overrides now depend on shell command templates and placeholder expansion, which is more flexible but less structured than strongly typed flags
* malformed project command templates fail at runtime when the affected action runs
* the TUI no longer provides inline editing for project-specific bootstrap commands

## Pros and Cons of the Options

### Keep hardcoded Lima invocations and add more TUI controls

The runtime behavior stays strongly typed in Go and the TUI keeps owning project-local edits.

* Good, because the runtime surface stays narrow and constrained
* Good, because invalid combinations can be rejected earlier
* Bad, because every new Lima flag or command variant needs more product surface
* Bad, because the TUI becomes the wrong place to manage many low-level command permutations

### Add dedicated CLI flags for every overridable Lima operation

Expose project-local runtime customization through `project create` and `project update`.

* Good, because the feature would remain discoverable from the CLI
* Good, because command templates could be validated during mutations
* Bad, because the CLI grows quickly as more Lima operations become configurable
* Bad, because users still need a durable on-disk representation for later manual adjustments

### Store project-scoped Lima command templates in project metadata and treat them as manual-edit advanced settings

Persist shell command templates under `lima_commands` in `project.yaml` and expand placeholders at execution time.

* Good, because it covers the full set of project-local Lima invocations with one stable storage shape
* Good, because the TUI only needs to surface the project metadata path instead of becoming a specialized editor
* Bad, because the runtime loses some static structure compared with dedicated typed flags
* Bad, because syntax errors in advanced templates are not caught until the affected action is executed
