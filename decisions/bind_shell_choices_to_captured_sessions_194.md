# Bind shell choices to captured nodes and sessions

## Context and Problem Statement

The ShellChooser artboard separates new sessions from running sessions, shows
launcher availability and lets users save a default. Session events and node
navigation can change while this overlay or a preference save is pending.
Using row positions or the current node could launch or attach the wrong shell.

## Considered Options

* Extend the flat selection-dependent menu.
* Use a dedicated chooser with captured identities and existing shell owners.
* Add a separate shell/session execution service.

## Decision Outcome

Use a dedicated themed modal with NEW SESSION and ATTACH TO A RUNNING SESSION
groups. Reuse the existing overlay host, session metadata and launcher/attach
owners. Compact rows wrap bounded context; keyboard and mouse scrolling keep
the selected row visible, including sessions beyond the nine numeric shortcuts.
Unavailable choices remain visible with a reason and cannot execute or save.
Embedded mode must be enabled in Settings before the chooser offers it.

Capture the canonical node ID when opening. Revalidate before execution and
again after asynchronous settings saves. Raw launches do not change navigation;
embedded launches select the captured node and open a fresh tab. Share manager
node lookup/navigation with the palette instead of duplicating targeting code.

Assign session numbers once per chooser lifetime, retain ended entries as
unavailable, and append arrivals. Mouse regions carry terminal IDs, and execution
rechecks those identities. Exact-session resolution cannot create a replacement.
New-session choices explicitly request a fresh session; ordinary `x` retains
its existing reuse behavior. Shift+Enter/W/T persist the selected mode through
the existing settings writer and use the captured node on completion.

Cache launcher probes per window command and TMUX environment; execution repeats
preflight before creating a terminal. Render observed activity, unacknowledged
bells and creation age. Show unknown evidence honestly and classify only the
known host/guest shell kinds; a title is not evidence of agent identity.
No persistence, daemon or protocol changes are required (27/2/6/7 remain).

### Positive Consequences

* Live refresh and delayed input cannot silently redirect a shell choice.
* The artboard's groups, availability and default controls reflect real state.
* Session ownership and attach/detach behavior remain with established services.

### Negative Consequences

* Ended rows occupy space until the chooser is reopened.
* Compact context may be truncated, and local probes cannot guarantee a later
  launcher will succeed; launch errors still use the existing status path.
* A successfully saved default remains saved if a subsequent launch fails.

## Pros and Cons of the Options

The flat menu is simpler but cannot reliably preserve targets during refresh.
A dedicated identity-based chooser needs rendering and event regressions but
keeps all execution in existing owners. A separate execution service would add
session and compatibility complexity without improving this presentation path.

## Links

* [Shell chooser design](../plans/tui-vnext/reference/ShellChooser.dc.html)
* [Delivery ledger](../plans/tui-vnext/README.md)
