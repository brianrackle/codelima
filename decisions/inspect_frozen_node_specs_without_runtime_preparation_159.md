# Inspect frozen node specs without runtime preparation

## Context and Problem Statement

The Spec design compares a node's frozen recipe with the current template and
shows the rendered Lima configuration. Existing configuration reads prepare
the runtime, which can make inspection fail precisely when diagnostics are
needed. Reconstructing host settings from today's defaults would also report
different values from the YAML used to create the instance.

## Considered Options

* Read stored node/bootstrap, template/environment/profile and rendered YAML.
* Reconstruct the recipe through the runtime's current template resolution.
* Add a daemon protocol for local metadata inspection.

## Decision Outcome

Use a read-only local service snapshot, without initialization, reconciliation
or runtime calls. Compare resources, image, agent, ordered environments and
resolved commands, plus the frozen profile's validation, launch and environment.
Show changed environment names without exposing their values. Bind templates
by their durable ID; deleting or losing a template leaves the node readable.

Read `nodes/<id>/instance.lima.yaml` directly. Require a regular file and cap
inspection at 2 MiB; invalid, missing and oversized YAML remain explicit errors
beside the usable frozen recipe. Never fill missing YAML fields from host
defaults. Full-file paging uses the same literal-argv `$PAGER` path as Logs.

Refresh outside the UI loop once per second while a node detail is open. Fence
responses by request serial, navigation generation and node ID. Keep the drift
indicator available on every detail tab. Use separate scroll positions for
frozen values, drift and Lima settings; compact terminals show one panel.
Wrap on grapheme boundaries and strip terminal controls from metadata text.

### Positive Consequences

* Inspection works for stopped or unavailable guests and never changes them.
* Environment edits appear as drift even when the template file is unchanged.
* Wide and compact layouts retain access to full values and long commands.

### Negative Consequences

* Separate metadata files are eventually consistent during concurrent edits.
* Existing bootstrap metadata lacks per-step provenance and timing; the UI
  states that limitation instead of deriving history from current recipes.
* Remote frontends would need a transport for this local read contract.

## Pros and Cons of the Options

Resolving through Lima could supply current defaults but would conflate them
with creation-time settings and require a working runtime. A daemon endpoint
would support remote clients but adds a protocol for files already local to
the current manager. Direct bounded reads preserve the stored evidence.

## Links

* [Spec reference](../plans/tui-vnext/reference/NodeSpec.dc.html)
* [Local diagnostic reads and paging](bounded_local_node_logs_and_bootstrap_capture_157.md)
