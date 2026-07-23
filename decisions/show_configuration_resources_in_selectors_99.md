# Show Configuration Resources in Selectors

## Context and Problem Statement

The TUI configuration selector showed only configuration slugs. Operators choosing a configuration for a node had to leave the selector and inspect each configuration separately to compare CPU, RAM, and disk. How should the selector expose those resources without changing the value submitted by the form?

## Decision Drivers

* Resource differences should be visible at the point of selection.
* Selector submission must continue using the stable configuration slug.
* Built-in size labels should remain compact enough for the right pane.
* Custom configurations with partial-GiB values must remain exact.

## Considered Options

* Append vCPU, RAM, and disk to each display label while retaining the slug as the option value.
* Put resources in a selector description block separate from the options.
* Keep slug-only selector rows and require configuration inspection.

## Decision Outcome

Chosen option: "Append resources to display labels while retaining slug values", because every choice becomes self-describing without changing node-creation or management inputs. Whole-GiB memory and disk values use compact GiB units; other values remain exact in MiB.

### Positive Consequences

* Operators can compare configurations without leaving the selector.
* Node creation and configuration management still submit the same slug values.
* Built-in rows use concise, familiar GiB values.
* Custom partial-GiB values are not rounded.

### Negative Consequences

* Selector rows are wider and may truncate in unusually narrow terminals.
* Display labels now require a shared resource-formatting rule.

## Pros and Cons of the Options

### Append resources to display labels while retaining slug values

* Good, because the relevant decision data appears on every row.
* Good, because display text and submitted identity remain separate.
* Good, because no selector protocol or persistence behavior changes.
* Bad, because rows consume more horizontal space.

### Put resources in a separate description block

* Good, because option rows remain short.
* Bad, because matching descriptions to the active row is less direct.
* Bad, because the description would need to change during navigation.

### Keep slug-only selector rows

* Good, because it preserves the smallest possible layout.
* Bad, because it does not provide the requested comparison data.
