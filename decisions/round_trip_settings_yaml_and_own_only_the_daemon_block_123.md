# Round-trip settings.yaml and own only the daemon block

Status: Accepted

## Context and Problem Statement

`LoadConfig` unmarshals the whole `Config` from `_config/settings.yaml`, so
operators legitimately hand-edit keys such as `default_image` and
`runtime_commands` there and CodeLima honors them. The writer did not agree: it
serialized a private struct holding only the daemon block and rewrote the file
whenever `configFileNeedsRefresh` reported a missing daemon key. Shipping a new
daemon key therefore deleted every user-authored key in the file, silently, on
the next command that ran the seed-and-repair pass. Retiring
`virtiofs_reclaim_threshold_percent` would have triggered exactly that wipe on
every existing home, so reader and writer had to be reconciled before the key
could be removed.

## Decision Drivers

* A settings refresh must never destroy state the reader accepts.
* The daemon must still be able to add, change, and retire the keys it owns.
* Retired keys must not fail a load, and must not linger forever either.
* A settings file written by a newer CodeLima must survive an older one.
* A malformed settings file must not make the daemon unusable.

## Considered Options

* Serialize the full `Config` from the writer.
* Narrow the reader to the daemon block so writer and reader own the same keys.
* Round-trip the document as a `yaml.Node` and replace only the `daemon` key.

## Decision Outcome

Chosen option: "round-trip the document as a `yaml.Node` and replace only the
`daemon` key", because it is the only option under which the set of keys the
writer can destroy is exactly the set it defines.

`configYAMLBytes` parses the existing file into a `yaml.Node` mapping and
replaces the value of `daemon` with a freshly encoded block, leaving every other
key, its comments, and the file's key order untouched. Within `daemon` the
writer is authoritative and rewrites the block wholesale, which is how a retired
key is dropped: `configFileNeedsRefresh` lists retired keys alongside missing
ones, so a home carrying `virtiofs_reclaim_threshold_percent` is refreshed
exactly once and then stops qualifying. The reader ignores keys it does not
model, which covers both retired keys and keys a newer CodeLima wrote. A
missing, empty, or unparseable file yields a fresh mapping rather than an error,
because refusing to write the daemon's own settings would leave the daemon
unable to start over a file the operator can trivially restore.

### Positive Consequences

* Adding or retiring a daemon key no longer risks user-authored settings.
* Comments and key order survive a refresh, so a hand-maintained file stays
  readable after CodeLima writes to it.
* Downgrades are non-destructive: an older binary preserves keys it cannot
  interpret instead of deleting them.
* Retired keys disappear on their own, without a bespoke migration step.

### Negative Consequences

* The writer is one step further from "marshal a struct"; a future key that must
  live outside `daemon` needs an explicit decision about who owns it.
* Nested indentation is normalized to the YAML marshaler's style on any refresh,
  so a rewrite is not byte-identical to the operator's formatting.
* A settings file that is not a YAML mapping is replaced rather than repaired.
* The one-time rewrite that drops a retired key only reaches a home whose
  seed-and-repair pass runs, so retiring a key still requires bumping
  `seedRevision`.

## Pros and Cons of the Options

### Serialize the full `Config` from the writer

* Good, because reader and writer would share one struct.
* Bad, because it materializes every internal and defaulted field into the
  file — `metadata_root`, `agent_profiles_dir`, and the full built-in
  `runtime_commands` table — turning derived values into pinned settings.
* Bad, because it still deletes any key the struct does not model.

### Narrow the reader to the daemon block

* Good, because the file would have exactly one owner.
* Bad, because it silently breaks operators who already configure
  `default_image` and `runtime_commands` there.

### Round-trip as a `yaml.Node`

* Good, because the writer can only destroy keys it defines.
* Good, because comments and unknown keys survive, which is what makes the file
  safe to hand-edit.
* Bad, because it is more machinery than a struct marshal.

## Links

* Retires the `virtiofs_reclaim_threshold_percent` setting removed with the
  VirtioFS reclaim ticker.
