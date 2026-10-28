# Separate VM status from setup presentation

## Context and Problem Statement

A VM can remain running after an installer or validation step fails. The fleet
replaced the observed VM state with `failed`, obscuring whether the VM was up.
In-flight operations also replaced Status with lifecycle phases.

## Decision Drivers

* Display VM state independently of setup, including in compact layouts.
* Preserve failed-step retries, lifecycle ownership and guest-shell readiness.
* Keep durable metadata and active daemon sessions compatible.

## Considered Options

* Continue merging setup failures into VM status.
* Replace internal lifecycle state with the runtime observation.
* Derive independent VM and setup states at the presentation boundary.

## Decision Outcome

Chosen option: "Derive independent VM and setup states at the presentation
boundary", because users need both facts and lifecycle actions need the saved
failure and in-flight states.

The fleet Status column, counts and `status:` filter use runtime observations.
A separate sortable Setup column and `setup:` filter use `pending`,
`provisioning`, `registering`, `ready` or `failed`. Current starts and existing
revision-fenced interrupted-start evidence refine setup only. Other operations
retain their separate phase/evidence context. Preview, Overview, failure details
and CLI node list show setup separately. CLI node show/list JSON and YAML project
`status` as VM state and add `setup_status`.

Runtime observations take precedence during setup and failures. Explicit absence
displays `missing`, and an unavailable state displays `unknown`. Legacy
running/stopped values remain a fallback only without an observation. The
daemon's internal Node transport and node.yaml lifecycle format stay compatible;
presentation copies the Node and does not modify durable state.

### Positive Consequences

* A running VM with failed setup displays both `running` and `failed`.
* Retry history and unfinished-start shell gates remain intact.
* No metadata migration or daemon protocol change is needed.

### Negative Consequences

* Internal Node.Status remains lifecycle-oriented, so presentation must use the
  shared helpers. CLI consumers of setup state must switch to `setup_status`.

## Pros and Cons of the Options

### Continue merging setup failures into VM status

* Good, because no new column is needed.
* Bad, because users cannot tell whether failed setup left a running VM.

### Replace internal lifecycle state with the runtime observation

* Good, because the internal status vocabulary becomes runtime-only.
* Bad, because this requires migrating retry, readiness and daemon contracts.

### Derive independent VM and setup states at the presentation boundary

* Good, because both facts are visible with existing records and daemon sessions.
* Bad, because lifecycle and presentation must remain distinct.

## Links

* Refines [ADR 37](use_lima_as_runtime_status_source_for_read_surfaces_37.md).
