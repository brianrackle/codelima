# Read surfaces do not write metadata

## Context and Problem Statement

CodeLima read surfaces — `node list`, `node show`, the project tree, and the TUI auto-refresh tick that reloads it every two seconds — were persisting metadata as a side effect. Batch reconciliation (ADR 37) saved every `node.yaml` on every read with `persist=true`, and `EnsureReady(mutating=false)` ran the full `EnsureLayout` on every read: rewriting a stale `config.yaml`, re-seeding agent profiles and built-in environment configs, and refreshing project and node metadata files. Reads take no locks, so these writes raced foreground mutations, and two concurrent callers hitting an unseeded home could each create built-in environment configs with fresh IDs, producing duplicate `codex` and `claude-code` rows (TODO #20). How do reads stay live and correct without writing?

## Decision Drivers

* Reads must not mutate the metadata store: unlocked writes from read paths race locked mutations and duplicate seeded metadata.
* Keep read-time status live: `node list` and the project tree must still reflect the actual Lima VM state (ADR 37).
* A fresh `CODELIMA_HOME` must still be seeded without a dedicated setup command.
* Stale metadata from older versions still needs a supported upgrade path.

## Considered Options

* Keep persisting reconciliation results on reads, but take the `nodes` lock on read paths.
* Stop persisting from reads, and move seeding/repair to mutating operations plus an explicit `doctor --repair`.
* Stop persisting from reads, but keep first-run seeding on read paths behind a marker file.

## Decision Outcome

Chosen option: "Stop persisting from reads, and move seeding/repair to mutating operations plus an explicit `doctor --repair`", because it removes every write from read paths instead of slowing reads down with locks, fixes the duplicate-seeding race at its root by running seed-and-repair only under the `environment-configs`/`projects`/`nodes` flocks, and keeps the fresh-home experience intact because every mutating command seeds before it runs.

Concretely: `reconcileNodes`/`reconcileNode` callers on read surfaces pass `persist=false` and thread the caller's context; `persist=true` survives only inside lifecycle transitions that already hold the `nodes` flock (`node start`, `node stop`, `node clone`). `Store.EnsureLayout` is split into `ensureDirectories` (mkdirs only, run once per Service instance for reads) and `seedAndRepair` (config rewrite, profile and environment-config seeding, metadata refresh — always under locks). Readiness gains an explicit middle tier, `ensureReadyForWrite` (directories plus locked `seedAndRepair`, no runtime-dependency validation): metadata-only mutations (project and environment-config writes) call it directly so they keep working without `limactl`, and `EnsureReady(mutating=true)` layers dependency validation on top of it for runtime lifecycle operations. TUI startup also runs `ensureReadyForWrite` once before the runner starts: a user-initiated app launch is a session start, not a background read, so a fresh home shows built-in metadata in the TUI's pickers; the pass is idempotent, flock-guarded, and once per process, while the TUI's two-second auto-refresh path remains a pure read. `doctor` becomes read-only by default and gains `--repair` to run the same locked pass on demand.

This refines ADR 37 rather than reversing it: read surfaces still batch-read Lima observations once per surface and merge `LastReconciledAt`/`LastRuntimeObservation` into the returned nodes in memory — they just no longer persist the merge.

### Positive Consequences

* Read surfaces, including the two-second TUI auto-refresh, leave the metadata home byte-for-byte untouched.
* Concurrent seeding cannot duplicate built-in environment configs: the slug lookup and the create happen under the flocks (resolves TODO #20).
* Reads get cheaper (a per-instance `sync.Once` mkdir check instead of a full layout pass per call), and cancellation reaches `limactl list` because reads thread the real context.
* Read-time status remains live per ADR 37.

### Negative Consequences

* A fresh home driven purely through read-only CLI commands shows no built-in environment configs or agent profiles until the first mutating command, `doctor --repair`, or the first TUI launch runs.
* Stale metadata (for example an old `config.yaml`) is upgraded only by a mutating command or `doctor --repair`, no longer by any read.
* Persisted `last_runtime_observation` values grow staler between mutations; only in-memory results are current.

## Pros and Cons of the Options

### Keep persisting reconciliation results on reads, but take the `nodes` lock on read paths

Read surfaces keep calling `SaveNode` for every node, serialized against mutations by the existing flocks.

* Good, because persisted metadata stays continuously fresh.
* Good, because it is a small change to the read paths.
* Bad, because every read — including the TUI tick every two seconds — contends on the mutation lock and rewrites every `node.yaml` indefinitely.
* Bad, because it does nothing about `EnsureReady(false)` seeding on reads, which is the duplicate-seeding race (TODO #20).

### Stop persisting from reads, and move seeding/repair to mutating operations plus an explicit `doctor --repair`

Reads only create missing directories; seed-and-repair runs under the `environment-configs`/`projects`/`nodes` flocks from mutating readiness checks and from `doctor --repair`.

* Good, because reads become side-effect free without adding lock contention to the hot read path.
* Good, because seeding becomes race-free by construction: it only ever runs under the locks.
* Good, because the fresh-home flow still works — any first mutating command seeds before acting.
* Bad, because purely read-only usage of a fresh or stale home shows unseeded/unrepaired state until something mutates or `doctor --repair` runs.

### Stop persisting from reads, but keep first-run seeding on read paths behind a marker file

Reads check a "seeded" marker and run the full seeding pass once when it is absent.

* Good, because a fresh home looks fully populated even to a purely read-only first command.
* Bad, because reads still write in the first-run window, so the race window TODO #20 describes survives exactly where it bites today.
* Bad, because a marker file adds a second source of truth about home state that repair logic then has to reconcile.

## Links

* Refines [ADR 37](/Users/brianrackle/projects/codelima/decisions/use_lima_as_runtime_status_source_for_read_surfaces_37.md) — reads still merge live runtime observations in memory; they stop persisting the merge.
