# Use compact unnumbered shell tab defaults

## Context and Problem Statement

ADR 143 displays terminal-reported titles verbatim after sanitization and
truncation. Ordinary shell titles repeat the username, hostname and project
path across tabs. The node/position fallback is also redundant because tabs
are already scoped to the selected node. The requested defaults are `shell`
and `host`, with no numbering, while retaining meaningful application titles.

## Decision Drivers

* Keep ordinary tab names short, including when several names coincide.
* Preserve application titles, host identification and status indicators.
* Keep presentation independent of terminal identity and lifecycle.

## Considered Options

* Recognize common shell titles in the frontend and use compact defaults.
* Always display `shell` or `host`, including for applications.
* Install shell hooks to identify prompt titles explicitly.

## Decision Outcome

Chosen option: "Recognize common shell titles in the frontend and use compact
defaults", because it addresses existing shell titles without changing shell
startup or daemon metadata. Empty titles, common shell executable names
(including login-shell names), and `user[@host]: /path` or
`user[@host]: ~/path` titles display `shell` for guests and `host` for hosts.
The identity contains letters, digits, dots, underscores or hyphens, with at
most one `@` separator. URIs and titles containing `|` or `·` are retained.

Sanitize before classification and classify before the existing 40-character
display limit. This preserves a task suffix beyond that limit and recognizes
long identity prefixes. Other titles retain ADR 143 behavior, including the
`host:` prefix for host application titles. Append progress, notification and
bell indicators independently. A cleared title or a subsequent ordinary shell
title restores the default. Never append position numbers or resolve duplicate
names by renaming tabs. Session IDs, ordering and persistence are unchanged.

### Positive Consequences

* Ordinary shells have short, predictable labels.
* Application task names and background status remain useful.
* Existing sessions adopt the presentation when the new TUI opens.

### Negative Consequences

* OSC titles do not identify their source. Unfamiliar custom prompt formats
  remain visible; an application title matching the shell convention is
  treated as a default shell title.
* Identical names rely on position and active highlighting for distinction.

## Pros and Cons of the Options

### Recognize common shell titles in the frontend

* Good, because existing sessions benefit without modifying their shells.
* Bad, because title classification is necessarily heuristic.

### Always display compact defaults

* Good, because it needs no classification.
* Bad, because it discards meaningful application titles.

### Install shell hooks

* Good, because supported shells could explicitly identify prompt state.
* Bad, because this requires shell-specific integration and does not cover
  already running shells or customized prompt hooks automatically.

## Links

* Refines [ADR 143](prefer_reported_terminal_titles_in_tab_labels_143.md).
* Retains [ADR 144](acknowledge_terminal_bells_on_visible_tab_visits_144.md).
