# Scope TUI Project Trees and Show Created Node Terminal Pane

## Context and Problem Statement

The TUI previously always loaded the full project tree, even when the operator launched CodeLima from a specific workspace context. Creating a node also selected the new node but left the split pane in its previous mode, so a stopped node could be selected without making its terminal placeholder immediately visible.

## Decision Drivers

* Operators should be able to focus the TUI on projects registered under a specific workspace directory.
* Path scoping must survive both initial load and automatic refresh.
* Creating a node should make the node's terminal surface available without starting a shell for a node that is not running.
* The existing `project tree` CLI output should keep its unscoped top-level behavior.

## Considered Options

* Add a separate workspace-root project tree service method and thread the root through TUI startup and refresh.
* Overload `Service.ProjectTree` so its root query can mean either a project identifier or a filesystem path.
* Filter only in the TUI after loading the full tree.

## Decision Outcome

Chosen option: "Add a separate workspace-root project tree service method and thread the root through TUI startup and refresh", because it keeps project identifier lookups and filesystem scoping explicit while letting the TUI use one consistent tree source for startup, reloads, and automatic refreshes.

Creating a node from the TUI now returns an operation result that requests terminal-pane mode after the tree reload selects the new node. This exposes the stopped-node terminal placeholder and start guidance immediately, but still avoids opening a node shell until the node is running or the operator explicitly starts it.

### Positive Consequences

* `codelima PATH` opens the TUI with only projects under that directory and its subdirectories.
* Automatic refresh preserves the same path scope instead of repopulating unrelated projects.
* The existing CLI `project tree` behavior remains separate from path-scoped TUI behavior.
* New nodes surface their terminal placeholder immediately without violating explicit terminal-session startup rules.

### Negative Consequences

* The TUI runner interface now carries a workspace-root argument, so test runners and future runners must preserve it.
* There are two project-tree entry points to keep aligned: unscoped/project-rooted tree loading and workspace-root filtering.

## Pros and Cons of the Options

### Separate workspace-root project tree service method

`Service.ProjectTreeByWorkspaceRoot` filters registered projects by canonical workspace path, promotes in-scope projects whose parent is outside the scope to visible roots, and then builds the normal project/node tree shape.

* Good, because filesystem scoping is explicit and testable at the service boundary.
* Good, because the TUI can reuse the same method for initial load and refresh.
* Good, because `project tree <project>` semantics do not become ambiguous.
* Bad, because it adds another tree-loading API.

### Overload `Service.ProjectTree`

The existing root query could try project ID/slug lookup first, then fall back to a path.

* Good, because it would avoid a new service method.
* Bad, because project slugs and filesystem paths would share one argument with ambiguous failure behavior.
* Bad, because CLI `project tree` and TUI path scoping would become harder to reason about independently.

### Filter only in the TUI

The TUI could load the full tree and remove out-of-scope projects before rendering.

* Good, because it would keep service APIs unchanged.
* Bad, because refresh, tests, and future non-TUI callers could drift from the same filtering rule.
* Bad, because it keeps more project data in the UI layer than the scoped surface needs.

## Links

* Relates to roadmap items 0.13 and 0.14 in `ROADMAP.md`.
