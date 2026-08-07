# Quarantine unreadable node records instead of failing list paths

Status: Accepted

## Context and Problem Statement

`Store.ListNodes` aborted on the first `node.yaml` it could not parse. The TUI,
the daemon forwarder, `doctor`, and every mutating command's seed-and-repair
pass all route through that enumeration, so a single truncated or hand-edited
record made the entire tool unusable with no repair path short of deleting the
directory by hand. The events log had the same shape of fragility: `readEvents`
returned an error for one undecodable line, and its `bufio.Scanner` kept the
default 64KiB token limit that the Lima output parser had already outgrown, so a
torn final append or one long record broke a node's history permanently.

## Decision Drivers

* One damaged record must never hide the healthy ones.
* Nothing the user might still want must be deleted to recover.
* A record that exists but cannot be read must not be reported as absent.
* The by-slug and by-instance indexes must never point at a directory that is
  gone.
* Refusing to adopt an unrecognized `CODELIMA_HOME` is a safety property and
  must survive the fix.

## Considered Options

* Keep failing the listing and require manual recovery.
* Skip unreadable records silently.
* Delete unreadable records during repair.
* Skip-and-warn in list paths, and quarantine by moving the directory during
  `doctor --repair`.

## Decision Outcome

Chosen option: "skip-and-warn in list paths, and quarantine by moving the
directory during `doctor --repair`", because it separates *tolerating* damage,
which every read path must do unconditionally, from *acting* on it, which stays
an explicit, non-destructive user command.

List paths skip a record that cannot be read or parsed, emit one warning naming
the path and the remedy, and continue; only a failure to enumerate the `nodes/`
directory itself is fatal. `Store.CorruptNodeRecords` returns the same skipped
set so `doctor` can report it without a second scan. An empty or truncated
`node.yaml` unmarshals to a valid zero value, so a record with no `id` is
classified as corruption rather than returned as a node with no identity.

Point lookups keep failing loudly. `NodeByID`, `NodeByIDOrSlug`, and
`NodeBySandboxName` return `MetadataCorruption` for a damaged record. The by-slug
path specifically stops falling through to a full scan when its index resolves to
an unparseable record: the scan skips corrupt records, so falling through would
answer "no such node" for a node that is demonstrably present.

`doctor --repair` quarantines before it seeds. Each corrupt record's index
entries are removed first — they are the only pointers that can resolve a slug or
instance name to the directory about to move, so a crash between the two steps
leaves a skipped-but-present record, a state the tool already tolerates, rather
than an index aimed at nothing. The whole `nodes/<id>/` directory is then renamed
to `_quarantine/<timestamp>-<id>/`, and a `quarantine.yaml` manifest recording
the source path, the reason, and the removed index entries is written beside the
moved files. The move runs under the global nodes lock and the per-node lock of
every record it touches. Recovery is moving the directory back.

The events reader was rewritten around a `bufio.Reader` rather than a
`bufio.Scanner`, because a scanner that hits its token limit cannot resume. Its
retention ceiling now mirrors the Lima parser's, an unterminated final record is
reported at debug level as ordinary crash debris, a fully written record that
will not decode is warned about as real damage, and a record past the ceiling is
dropped while the reader consumes it to the next newline so later records
survive.

Separately, `.DS_Store` and `.localized` no longer count against a fresh home.
Any other unrecognized entry still refuses adoption.

### Positive Consequences

* One damaged file costs the user exactly that node, not the tool.
* Damaged state is preserved, self-describing, and outside every node surface.
* Indexes stay consistent with what is on disk at every step of the move.
* A node's history survives the torn write that ends it.
* First run stops failing on a macOS-first tool because Finder visited the
  directory.

### Negative Consequences

* A quarantined node's Lima instance becomes an orphan that `doctor` reports
  separately; the runtime is not cleaned up by the move.
* `_quarantine/` grows until a human empties it.
* Read paths now depend on a warning being seen for the user to learn a record
  was skipped, so `doctor` is the surface that must be run to find out.
* Records past the retention ceiling are dropped rather than streamed.

## Pros and Cons of the Options

### Keep failing the listing

* Good, because damage is impossible to overlook.
* Bad, because the failure surface is every command, including the ones that
  would have told the user what was wrong.
* Bad, because recovery requires knowing the on-disk layout.

### Skip unreadable records silently

* Good, because the tool keeps working.
* Bad, because a node disappearing with no explanation reads as data loss.
* Bad, because nothing ever prompts a repair.

### Delete unreadable records during repair

* Good, because the home returns to a clean state in one step.
* Bad, because "unreadable" is a guess about a file the user may be able to
  salvage by hand.
* Bad, because a parser bug would become permanent data loss.

### Skip-and-warn plus quarantine by moving

* Good, because tolerance is unconditional and destruction is never on the
  table.
* Good, because the quarantined directory is a complete, inspectable record.
* Good, because the repair is idempotent — a second run finds nothing to move.
* Bad, because quarantined directories accumulate until removed by hand.

## Links

* Implements item 10 of the codebase review's fix order (`plans/ISSUES_PLAN.md`
  §3, §7).
* Extends the stale-claim recovery contract from
  [ADR 86](use_the_daemon_lock_for_shutdown_and_startup_recovery_86.md), which
  established `doctor --repair` as the non-destructive recovery surface.
* Storage layout and the `_quarantine/` path are specified in SPEC.md's storage
  contract.
