# Acknowledge terminal bells on visible tab visits

## Context and Problem Statement

The tab bar showed `· bell` forever after receiving any terminal bell. Users
need an alert that clears when they visit its tab. Hidden tabs defer full
screen snapshots, so their metadata also needs lightweight delivery for new
alerts to appear while the user is elsewhere.

## Decision Drivers

* Show an unseparated `🔔` suffix for unacknowledged bells.
* Clear alerts on visits and show subsequent background bells.
* Preserve idle efficiency and avoid fetching hidden terminal grids.
* Keep independent windows' acknowledgement state separate.

## Considered Options

* Track acknowledged counts locally and push metadata with dirty events.
* Reset the daemon's shared bell counter when a client visits.
* Poll hidden terminal snapshots for new bells.

## Decision Outcome

Chosen option: "Track acknowledged counts locally and push metadata with dirty
events", because it gives timely alerts without shared dismissal or grid pulls.
Daemon protocol 7 adds terminal metadata to the existing dirty-event payload.
The frontend caches that metadata with its snapshot sequence, ignores older
updates, and redraws only when metadata changes. A newer metadata event remains
authoritative over a delayed screen response. Authoritative synchronization
resets the metadata sequence for a replacement daemon and fences outstanding
screen requests so an old daemon's delayed reply cannot undo the reset.

Each TUI session records the last acknowledged cumulative bell count. A tab
acknowledges the current count when its terminal is visible in the focused
window, including the split-pane preview. Info panes, overlays and unfocused
windows do not acknowledge hidden bells. Focus-eventless terminals use the
existing assumed-focused fallback. Bells arriving while the tab is viewed are
acknowledged immediately. A decreasing counter resets the local baseline.
Session identity preserves acknowledgement across reorder and reconnect; close
discards it. Daemon counts and other clients' acknowledgement remain unchanged.

### Positive Consequences

* A new background bell appears as `Title 🔔` and clears on visit.
* Background titles also stay current without full snapshot traffic.
* The same acknowledged count cannot resurrect an alert on another redraw.

### Negative Consequences

* Dirty events carry more metadata, requiring an exact daemon protocol bump.
* A fresh TUI process has no persisted acknowledgement history.
* Hosts without focus reporting cannot distinguish an unfocused window.

## Pros and Cons of the Options

### Local acknowledgement with metadata pushes

* Good, because alerts update promptly without polling or cross-window state.
* Bad, because sequence handling must cover delayed screen replies and reconnect.

### Reset the shared daemon counter

* Good, because every window would show the same dismissal.
* Bad, because visiting one window would hide alerts in another.

### Poll hidden snapshots

* Good, because it could reuse the screen-read path.
* Bad, because grid traffic and CPU use would grow with hidden tab count.

## Links

* Refines [ADR 143](prefer_reported_terminal_titles_in_tab_labels_143.md).
* Preserves [ADR 90](replace_idle_terminal_polling_and_broken_stream_spins_90.md).
