# Persist manager preferences and launch managed shells

## Context and Problem Statement

The manager design allows settings edits and opening daemon-owned shells in
another terminal window or tmux. Settings may contain operator comments and
keys from a different release; replacing a whole configuration would lose them.
Long-lived services also need to observe changes without racing shared fields.

## Decision Drivers

* Preserve authored YAML, session identity and the selected metadata home.
* Apply new-node defaults only to subsequently created nodes.
* Keep external commands and filesystem writes outside the UI event loop.
* Support spaces and metacharacters in paths without shell interpretation.

## Considered Options

* Rewrite settings from the Config struct and mutate each service instance.
* Patch supported YAML leaves under a settings lock and return new snapshots.
* Launch independent SSH processes in new windows.

## Decision Outcome

Patch individual supported leaves in a yaml.Node under the cross-process
`settings` lock, then atomically replace the document. Keep key order, comments
and unknown fields at each level. Invalid values and malformed documents are
errors without writes. Seed/repair takes the same lock. Service configuration
remains immutable; new node creation reads the current credential-import
default and freezes it. A frontend applies returned preferences on its event
loop and refreshes them when opening System.

Shell preferences select attach, window, tmux or embedded for `x`. The chooser
allows explicit choices; Shift saves a choice as the default. Every external
launch resolves or creates a daemon session first and invokes `terminal attach`
with that session's identity and explicit CODELIMA_HOME. The window template
is a quoted argv list with one standalone `{cmd}` splice. It runs directly,
without shell evaluation; custom shell logic uses an executable wrapper.
tmux uses `new-window` with separate arguments and requires an existing session.
Window launchers are reaped asynchronously and outlive the frontend.

Stop confirmation defaults to busy sessions. Shell-integration prompt state
identifies idle sessions; unknown activity is treated as busy. Explicit always
and never settings are available. Grouping applies to all-node views; ANSI and
truecolor choices affect manager chrome, independently of guest colors.

### Positive Consequences

* Concurrent supported settings edits do not lose one another.
* An existing daemon observes new-node defaults without a restart.
* Windows share managed session ownership and attachment behavior.
* Paths and user-authored command arguments remain literal.

### Negative Consequences

* Shells without prompt integration require conservative stop confirmation.
* The configured terminal executable must be installed; desktop window
  behavior requires platform qualification.
* Arbitrary shell expressions require a wrapper rather than inline templates.

## Pros and Cons of the Options

Whole-struct serialization is simpler but drops unknown fields and comments;
mutable shared configuration introduces synchronization and propagation work.
Leaf patches preserve the authored document while keeping snapshots immutable.
Independent SSH windows avoid attachment code but lose persistent session
identity and consistent detach semantics.

## Links

* [Physical attachment](attach_physical_terminals_to_daemon_sessions_151.md)
* [Delivery ledger](../plans/tui-vnext/README.md)
