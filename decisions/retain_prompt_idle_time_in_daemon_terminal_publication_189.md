# 189. Retain prompt idle time in daemon terminal publication

## Context and Problem Statement

The fleet artboards show an idle duration beside sessions and wholly idle marked
targets. A frontend clock would restart when a window reopened. A quiet output
stream, terminal title or missing progress signal cannot establish idleness.

## Decision Drivers

* Use positive shell integration evidence and accepted input.
* Preserve a common interval across frontends, renderer recovery and live update.
* Keep rendering and metadata publication free of guest probes and extra polls.

## Considered Options

* Retain a prompt clock in the daemon snapshot publisher.
* Start an independent timer in each frontend.
* Infer inactivity from titles or lack of output.

## Decision Outcome

The daemon's existing publisher retains the start of a continuous observed
prompt interval. Native cursor-at-prompt evidence with no progress state starts
the clock; leaving the prompt or entering progress/error/paused state clears it.
The isolated PTY owner records accepted raw input, encoded input and semantic
paste. A more recent input timestamp advances the idle start. Empty, rejected,
duplicate and renderer-generated query responses do not count as input.
Raw physical-terminal traffic is conservatively counted as input because that
byte stream does not distinguish user input from terminal replies.

The publisher supplies the timestamp and staleness in shared terminal metadata;
it ignores renderer-provided values for those fields. A stale renderer suppresses
the current activity claim while retaining the interval for recovery. Unsupported
terminal backends show at-prompt/unknown without inventing a duration. Clock
rollback starts a new observed interval instead of emitting a future timestamp.

Handoff version 7 includes this clock in the existing bounded Activity baseline.
The importer continues accepting Activity version 6, recovery version 5 and the
previous supported transports. Version 6 transfers its queue and notice baselines
unchanged, but has no retained prompt interval. Ordinary protocol 26 and renderer
protocol 6 carry the shared metadata shape; persisted session version 2 is unchanged.

Fleet session rows calculate elapsed time from the shared timestamp. Marked
targets show an idle duration only when every session has a current recorded
prompt interval, using the most recent interval so the aggregate does not overstate
idle time. Unknown, stale, disconnected, working, paused and error sessions cannot
produce an all-idle target claim.
Fleet response failure or expiry also suppresses current activity claims before
the slower connection heartbeat declares the daemon disconnected. A healthy
idle shell needs no metadata traffic; freshness comes from the existing fleet
request rather than the age of its last prompt event.

### Positive Consequences

* New frontends and replacement daemons retain the same observed interval.
* Hidden sessions need no screen pulls or per-second daemon publications.
* Idle labels have an explicit source and conservative unknown behavior.

### Negative Consequences

* The interval measures prompt inactivity, not process scheduling or user intent.
* Input accepted by a raw attachment can include physical-terminal replies.
* Legacy daemon updates establish a new interval after importing their terminal.

## Pros and Cons of the Options

### Daemon publisher clock

Uses the existing observation and handoff owners, with one scalar per terminal;
requires input accounting and wire compatibility coverage.

### Frontend clock

Simple locally but disagrees across windows and loses elapsed time on reconnect.

### Heuristic inactivity

Would work without shell integration but confuses silent work with idleness.

## Links

* [Fleet evidence](read_bounded_fleet_evidence_independently_of_activity_pages_187.md)
* [Design ledger](../plans/tui-vnext/README.md)
* [QA](../QA.md)
