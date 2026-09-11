# Prefer reported terminal titles in tab labels

## Context and Problem Statement

The tab bar appended terminal metadata to the node label and positional number.
Programs that set meaningful titles produced redundant labels such as
`codelima 1 · Test this | codelima`, consuming space in the tab bar.

## Decision Drivers

* Use the running program's title when available.
* Preserve useful names for untitled shells and identify host shells.
* Keep mutable terminal text separate from durable session identity.

## Considered Options

* Prefer the reported title, falling back to the node label and position.
* Continue appending the reported title to the node label and position.

## Decision Outcome

Chosen option: "Prefer the reported title, falling back to the node label and
position", because it removes redundant names while preserving untitled tabs.
Sanitize and bound titles with the existing 40-character metadata formatter.
An empty sanitized title uses the existing fallback. Apply `host:` to either
name and append bell, progress, and notification badges separately. Read one
metadata snapshot per rendered tab. Title changes never change session IDs,
selection, or ordering. Bell acknowledgement and presentation are refined by
ADR 144.

### Positive Consequences

* Agent session titles remain readable with less tab-bar clutter.
* Untitled and host shells remain identifiable.
* Existing metadata sanitization and status indicators are preserved.

### Negative Consequences

* Programs can report identical titles; those tabs have identical names, with
  position and active brackets still available to distinguish them.

## Pros and Cons of the Options

### Prefer reported titles

* Good, because the most descriptive text occupies the tab name.
* Bad, because duplicate reported titles have no automatic numeric suffix.

### Append reported titles

* Good, because every tab retains a visible positional label.
* Bad, because repeated node names and numbers crowd out meaningful titles.

## Links

* Refines [ADR 52](scope_tui_terminal_tabs_to_focused_target_with_explicit_option_controls_52.md).
