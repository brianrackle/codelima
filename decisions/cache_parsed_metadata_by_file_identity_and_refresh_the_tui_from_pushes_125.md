# Cache parsed metadata by file identity and refresh the TUI from pushes

Status: Accepted

## Context and Problem Statement

Node and configuration state was reconciled between processes by polling. The
daemon forwarder called `Service.NodeList` once a second, and every `NodeList`
re-read and re-YAML-parsed every `node.yaml` in the home plus one
`configuration.yaml` per node to hydrate its slug. Each TUI window polled
`node.list` once a second on top of that — even under a daemon that already
broadcast `node.status_changed` the moment a lifecycle operation landed, a push
the TUI answered with a bare redraw of the same stale list. Every posted event
then triggered a full-screen rebuild, and a burst of the redraw-only events that
terminal damage produces at up to 20Hz cost one repaint each. So the whole
metadata home was parsed at >=1Hz, and the screen was rebuilt N times for N
identical frames, to observe state that changes a few times an hour.

## Decision Drivers

* Reads must never serve stale metadata: a write followed by a list, in the same
  process or from another one, must show the write.
* The skip-and-warn and quarantine semantics for unreadable records
  ([ADR 122](quarantine_unreadable_node_records_instead_of_failing_list_paths_122.md))
  must survive unchanged, including the guarantee that repairing a file is
  picked up on the next call.
* A cached record must not be mutable by the callers it is handed to;
  `Service.NodeList` writes the hydrated configuration slug into the records it
  is given.
* Push-driven refresh must not introduce a latched failure state: a dropped push
  must not leave the tree stale forever (invariant I2).
* Dropping a queued event must never drop a state change — the one-shot
  completion and handshake events are latches, not frames.

## Considered Options

* Keep polling and make the parse cheaper (a faster decoder, a smaller record).
* Cache parsed records with a time-based expiry.
* Cache parsed records keyed by the identity of the file they came from.
* Push-only TUI refresh with no fallback poll.
* Push-driven TUI refresh with a slow fallback poll.
* For the live usage numbers the same reply carries: keep polling `node.list`
  once a second for them, or push each sample as its own per-node event.

## Decision Outcome

Chosen option: "cache parsed records keyed by the identity of the file they came
from", plus "push-driven TUI refresh with a slow fallback poll", because
together they remove the per-second work without introducing any window in which
a read can answer with something other than what is on disk.

`Store` keeps a per-record parse cache for node and configuration metadata. Every
read stats the file — the win is skipping the open, the read, and the unmarshal,
not skipping the stat — and serves the cached record only when the stat reports
the same file (`os.SameFile`: device plus inode), the same size, and the same
modification time. The identity leg is what makes this safe when timestamps are
coarse: every metadata write goes through `internal/atomicfile`, which renames a
fresh temp file over the destination, so a rewritten record always lands on a
different inode even when its size and its second-resolution mtime match what it
replaced. Store's own writes additionally drop the entry outright, so a
write-then-list inside one process cannot serve the previous content even if some
future write path stops going through rename. The identity is sampled *before*
the read, never after: stamping a record with an identity taken beforehand can
only cost one extra parse on the next pass, while stamping it afterwards could
bind newer content to an older identity and then serve it until the file changes
again. Records are cloned on the way out, because callers mutate them. Failures
are never cached — a record that will not parse is re-read every pass, which is
what keeps one warning per list, keeps `doctor --repair` offered, and makes a
repair visible on the next call rather than after an expiry.

The TUI now treats the daemon's `node.status_changed` as the reload trigger. The
push schedules one debounced (250ms, trailing edge) reload request, so a
lifecycle command against several nodes finishes emitting before the reload runs
and the burst costs one load. The periodic ticker stays, at 10s under a daemon,
purely as the safety net for a dropped push; with no daemon there is nothing to
push, so the tick is the whole freshness budget and runs at 3s. Both intervals
are named constants carrying that reasoning.

The event loop collapses the redraw-only events queued behind the one it is
handling: they render identical frames, so one draw stands for the run. Only
`vaxis.Redraw` is collapsed. Everything else is applied, in order — a channel
cannot be un-read, so the first non-redraw event the collapse encounters is
carried to the next iteration rather than consumed.

Live usage is pushed rather than polled. `node.list` was also the only transport
for the daemon's per-node CPU, memory, and disk numbers, which the forwarder
samples once a second, so a slow list poll would have made those numbers as slow
as the poll. The forwarder now publishes a `node.usage_changed` event carrying
one node's whole sample — exactly the fields `addNodeUsage` merges into a
`node.list` reply, so a pushed sample and a polled one are the same value and
are comparable by their `sampled_at`. Dropping a reading is published the same
way, because a subscriber holding the previous numbers has to be told to stop
showing them. The forwarder does not know about the server: the host hands it
the same gated publisher it uses itself, wired in `wireServerLinks` because
publishing is only safe once the server exists.

The TUI applies a usage push in place, on the event loop that owns the node
records, and repaints through the queue so a burst of per-node samples collapses
into one frame. It deliberately does not reload: a 1Hz sample that scheduled a
`node.list` round trip would rebuild the polling this decision removed. Two
races follow from having two sources for the same value, and both are settled by
rule rather than by timing. Membership belongs to the reload: a sample for a
node this window does not hold is dropped, so a push can never resurrect a node
a reload removed nor add one from outside the window's directory scope. Freshness
belongs to whichever sample is newer: both timestamps are minted by the
forwarder at sample time, so a push older than the last reload is ignored and a
reload whose embedded usage is older than the last push is corrected from the
kept sample. The kept samples are pruned to the reloaded node set.

### Positive Consequences

* An unchanged home costs one stat per record per list instead of an open, a
  read, and a YAML unmarshal, for the daemon forwarder and every TUI window.
* A node's state change reaches the tree on the push rather than on the next
  tick, so the tree is more current than it was at a tenth of the poll rate.
* A damage burst repaints once instead of once per event.
* The cache is self-correcting across processes: a write from a CLI invocation
  or another daemon is detected by the next stat, with no invalidation protocol.
* The live usage numbers keep their documented one-second freshness (SPEC.md
  §"node properties", README.md, QA.md) while costing one small event per
  running node instead of a full `node.list` round trip per window per second.

### Negative Consequences

* A corrupt record is re-read on every list pass, so a home with damaged records
  keeps paying for them until `doctor --repair` runs.
* Cache entries are never evicted. Node and configuration records are tombstoned
  rather than deleted, so the cache is bounded by what the home holds, but a
  record removed out of band leaves an entry behind for the process lifetime.
* Store now holds mutable state, so its reads take a mutex that its callers did
  not previously contend on. The lock is held around map access only; no I/O
  happens under it.
* Live usage now has two sources instead of one, and the rules that reconcile
  them are the TUI's to keep correct. A future field added to the usage set has
  to be added to both `addNodeUsage` and `nodeUsageEvent` or the two will
  disagree.
* Usage events are emitted per running node per second, so a daemon with many
  running nodes broadcasts proportionally more. The payload is a handful of
  numbers and the TUI collapses the repaints, but the event rate is no longer
  independent of the node count.
* Without a running forwarder there are no usage events. Daemon mode still
  carries usage on the 10s `node.list` enrichment in that case, and no-daemon
  mode has no forwarder and therefore no usage at all — as before this change,
  where the tree renders `--` for CPU, memory, and disk.

## Pros and Cons of the Options

### Keep polling and make the parse cheaper

* Good, because it changes no contract.
* Bad, because the work is proportional to the home size no matter how fast the
  decoder is, and the home is re-parsed at >=1Hz forever.

### Time-based expiry

* Good, because it is trivial to implement.
* Bad, because it trades correctness for cost: within the window a read can
  answer with content that is demonstrably not what is on disk.
* Bad, because there is no expiry short enough to be correct and long enough to
  be worth having.

### Identity-keyed cache

* Good, because a read is either the parsed file or a fresh parse of it, never a
  guess about how old the copy is.
* Good, because cross-process writes need no invalidation protocol.
* Bad, because it depends on the write path preserving the identity property;
  the explicit invalidation on Store's own writes is the belt to that braces.

### Push-only TUI refresh

* Good, because it is the cheapest possible steady state.
* Bad, because it makes a dropped push a latched stale view, which is exactly
  the failure class invariant I2 forbids.

### Push-driven refresh with a slow fallback poll

* Good, because the common case is event-driven and the failure case is
  self-healing.
* Bad, because the fallback interval sets the cadence of anything else the same
  call happens to carry, which is what forced the usage decision below.

### Keep polling `node.list` once a second for the usage numbers

* Good, because it needs no new event and no reconciliation rules.
* Bad, because it reinstates exactly the per-second round trip per TUI window
  this decision set out to remove, for a payload that is mostly the node records
  that did not change.

### Push each usage sample as its own per-node event

* Good, because the transport now matches the data: a small per-node value that
  changes every second travels on its own, and the list stays a list.
* Good, because the sample is applied in place, so it costs no reload and its
  repaint collapses with every other queued redraw.
* Bad, because the same value now arrives by two routes and the ordering between
  them has to be decided explicitly rather than by there being only one.

## Links

* Implements `plans/ISSUES_PLAN.md` §2c (polling economics).
* Preserves the read-path contract established by
  [ADR 122](quarantine_unreadable_node_records_instead_of_failing_list_paths_122.md).
* The runtime-observation cache that the daemon's `sandbox.List` already reads
  through is a separate cache with different rules
  ([ADR 37](use_lima_as_runtime_status_source_for_read_surfaces_37.md)); this
  decision does not change what may consult it.
