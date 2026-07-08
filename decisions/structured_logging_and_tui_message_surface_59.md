# Structured logging and a TUI message surface

## Context and Problem Statement

CodeLima had no observability. The `--log-level` flag was parsed into `options.LogLevel` in `parseGlobalOptions` and then read nowhere — it was dead. There was no logger anywhere in the process: service operations, runtime (`limactl`) invocations, terminal open/close, and TUI auto-refresh failures all happened silently, and auto-refresh errors were discarded outright (`finishDataRefresh` returned on error without a trace). The libghostty terminal library writes parser warnings to process stderr, which we contained by dup2'ing `/dev/null` over fd 2 around every bridge call — the warnings were thrown away, so a rendering bug left no evidence. The TUI's user-facing status was a single overwritable string (`a.status`): the newest message clobbered the previous one, and completed or failed background-operation output (capped at 200 lines) was discarded the moment the operation finished, so a failed `node start` left nothing to inspect. How do we add real observability across both run modes without new dependencies and without changing the TUI's look?

## Decision Drivers

* One logging story for CLI and TUI (and later the daemon), driven by the existing `--log-level` flag.
* TUI logs must never corrupt the rendered chrome — they cannot go to stderr while Vaxis owns the screen.
* libghostty stderr warnings must become inspectable without spilling on screen and without reintroducing the concurrency hazard of the process-global dup2.
* Log at seams only (operation start/finish, runtime invocations, terminal lifecycle, refresh errors) — no per-tick success spam.
* No new third-party dependencies (`log/slog` is stdlib; rotation is hand-rolled).
* The TUI message surface must retain history (especially failed background operations) while the status line looks exactly as it does today.
* Keep the existing TUI test suite passing unmodified (the `a.status` field is asserted directly in several tests).

## Considered Options

* **Logger delivery:** constructor parameter on `NewService` vs. a `SetLogger` method with a sane default.
* **TUI sink:** stderr (like CLI) vs. an append-only file under `CODELIMA_HOME/_logs/`.
* **libghostty capture logger:** plumb a `Service` logger into the cgo layer vs. a process-global package logger.
* **Message surface:** replace `a.status` wholesale with a ring (footer renders `ring.Latest()`) vs. keep `a.status` as the transient footer and add a parallel retained ring.

## Decision Outcome

**Logger delivery — `SetLogger`, not a constructor parameter.** `Service` gains a plain `logger *slog.Logger` field (defaulting to a discard logger in `NewService`) plus `logLevel slog.Level`, set via `SetLogger(logger, level)`. This keeps `NewService`'s signature — and its ~one production and one test call site — untouched, and the pointer field is shared by `withIO`'s shallow clone exactly like the `ready` pointer from ADR 57 (no `go vet` copylocks concern; `*slog.Logger` holds no lock). A nil-safe `s.log()` accessor makes every seam call safe on a Service built without a logger (all the fakes).

**Per-mode sinks.** `Run` (cli.go) wires the previously-ignored `--log-level` into `parseLogLevel` and installs a `slog.NewTextHandler` on **stderr** for CLI commands. TUI mode swaps this, via `Service.enableFileLogging()` called at the top of `vaxisTUIRunner.Run`, for an append-only **file sink** at `CODELIMA_HOME/_logs/codelima.log`. Rotation is deliberately dumb and dependency-free: `rotatingLogWriter` renames the file to `codelima.log.1` once a write would cross ~5 MB, keeping exactly one generation. It is mutex-guarded so the service logger and the libghostty capture can share one writer.

**libghostty capture — a package-global logger, justified.** `withGhosttyStderrSuppressed` is a generic free function with no `Service` in scope, called from ~20 bridge sites; threading a `Service` through all of them (or the cgo backend) is disproportionate. Instead a process-global `packageLogger` (default discard) is pointed at the active TUI file sink by `enableFileLogging`. The `/dev/null` sink becomes an `os.Pipe()` whose write end is dup2'd over fd 2; a single reader goroutine drains it and forwards each non-empty line to `packageLog().Debug(line, "source", "libghostty")`. The `sync.Once`+mutex structure is preserved, and the process-global caveat is documented in the function: the mutex serializing every bridge call is what makes the dup2 safe when two terminals initialize at once (this also closes the prior data race). The reader must keep draining — a stalled reader would let a full pipe buffer block libghostty's writes to fd 2 and hang the bridge call.

**Seam-only logging, leveled to keep default CLI quiet.** Service operation start/finish log at **debug** (`node start/stop/create/clone`, via a deferred `logOperation` carrying op name, duration, and error; failures escalate to **error**). Runtime invocations log the **verb only, never argv** (argv can hold host paths) at debug. Terminal open/close and libghostty lines log at debug; TUI refresh failures at **warn**. Because operation start/finish is debug, a default (`info`) CLI run stays silent while `--log-level debug` shows the full trace.

**Message surface — keep `a.status`, add a retained ring.** Replacing `a.status` wholesale would either break the several tests that assert it or lose the "clear on success" footer behavior (a ring only appends). So `a.status` stays as the transient footer (byte-for-byte identical rendering), and a new `tuiMessageLog` ring (cap 200, entries `{time, level, text}`) becomes the durable record. Rather than reroute all ~40 `a.status =` sites, a draw-time chokepoint (`captureStatusMessage`) mirrors each *changed, non-empty* status into the ring at info level; clearing to `""` records nothing and re-arms capture. Background-operation output — previously discarded on completion — is now retained into the ring at its true level in `finishOperation` (failures at error, with the captured output tail), which then suppresses the duplicate chokepoint capture. A read-only, scrollable **messages view** overlay (`tuiMessagesView`) reuses the existing border/scroll building blocks and opens with **`m`** from the tree (mirroring the `[i]` info toggle); Esc closes it, Ctrl+C/q quit, consistent with the dialog/menu/selector overlays.

**Absorbed item 0.7.2.** The three `NodeStart` failure-path rollbacks (`_ = s.store.SaveNode(...)` / `_ = s.store.AppendNodeEvent(...)`) route through `recordNodeStartRollback`, which checks each write and logs failures at error level. Behavior is otherwise unchanged — the caller still returns the original start failure.

### Positive Consequences

* `--log-level` is live; `codelima --log-level debug node create …` prints structured seam logs to stderr, and higher levels suppress them.
* The TUI writes a rotating `_logs/codelima.log` and never corrupts the screen.
* libghostty warnings are captured to that log at debug instead of being discarded, and the capture is now concurrency-safe by construction.
* Failed background operations remain inspectable in a scrollable messages view after they finish; the status line is visually unchanged.
* The existing TUI test suite passes unmodified (`a.status` semantics preserved).

### Negative Consequences

* Message levels captured from the status chokepoint are all info; only explicitly-recorded seams (operation results, rollbacks, refresh errors) carry their true level. A status string that happened to be an error is retained at info.
* The package-global `packageLogger` is a second logger handle alongside the per-Service one; in TUI mode both point at the same file, but in CLI mode the package logger stays at discard (libghostty is TUI-only, so this is harmless).
* Rotation keeps a single generation; a burst larger than ~10 MB between reads can still lose the oldest lines.
* Per-operation retained output is capped to its tail (40 lines) so one noisy operation cannot evict the whole ring — very long output is truncated in the messages view.

## Pros and Cons of the Options

### Logger via constructor parameter

* Good, because the logger is mandatory and explicit at construction.
* Bad, because it changes `NewService`'s signature and every call site (production and tests), and forces a logger decision before the run mode (CLI vs TUI) is known.

### Logger via `SetLogger` with a discard default (chosen)

* Good, because construction is unchanged and mode-specific sinks are installed once the mode is known (`Run` for CLI, `enableFileLogging` for TUI).
* Good, because the pointer field follows the established `withIO`-safe pattern.
* Bad, because a Service is briefly in a discard state before `SetLogger`; mitigated by the nil-safe accessor.

### TUI logs to stderr

* Bad, because stderr is the rendered screen under Vaxis; logs would corrupt the chrome.

### TUI logs to a rotating file (chosen)

* Good, because logs never touch the screen and survive the session for inspection.
* Bad, because a naive size-rotation without a dependency keeps only one generation.

### Plumb a Service logger into the libghostty capture

* Good, because it avoids a package global.
* Bad, because `withGhosttyStderrSuppressed` is a generic free function reached from ~20 sites with no Service in scope; the plumbing cost is disproportionate for one debug seam.

### Package-global capture logger (chosen)

* Good, because it reaches the cgo seam with no plumbing and is pointed at the real file sink at TUI start.
* Bad, because it is a second, process-global logger handle.

### Replace `a.status` wholesale with a ring

* Good, because there is a single source of truth for the status surface.
* Bad, because the footer's clear-on-success behavior is lost (a ring only appends), and the several tests asserting `a.status` would have to change — which the no-semantic-change contract forbids.

### Keep `a.status`, add a retained ring (chosen)

* Good, because the footer is byte-for-byte unchanged and the existing tests pass unmodified, while history (including failed operations) is retained.
* Bad, because status-derived ring entries are recorded at info regardless of their real severity.

## Links

* Refines the readiness/`withIO` pointer-field pattern from ADR 57 (read surfaces do not write).
* Work item 0.5 ("Real observability") and absorbed item 0.7.2 (swallowed rollback errors) in `plans/IMPROVEMENT_PLAN.md`.
