# Single terminal launch contract via Service.TerminalLaunchSpec

## Context and Problem Statement

Embedded terminals were spawned from two divergent command-building paths inside the TUI store: `OpenProjectTab` assembled an interactive host-shell command rooted at the project workspace, while `OpenNodeTab` assembled a `codelima shell <node>` re-invocation with a store-cached executable path. Any future front end (the planned daemon, a tmux sidebar) would have had to re-derive the same commands, and the invariant that "every managed terminal enters the VM through CodeLima" was enforced only by convention. The daemon work (Track 3) and the runtime-actor refactor (Track 2.1) both need a single, testable place that decides how a terminal is launched.

## Decision Drivers

* One authoritative place builds a terminal's argv/dir/env, so every front end launches terminals identically.
* The existing tab/session behavior pinned by the identity characterization suite must not change.
* The seam must serve the daemon later, where entity resolution happens under the daemon's own lock discipline.
* Fix the read-only-`$HOME` INPUTRC failure (TODO #18) at the one launch site rather than in each front end.

## Considered Options

* Keep the two front-end command builders.
* `TerminalLaunchSpec(target, kind)` that resolves the project/node from the store.
* `TerminalLaunchSpec(target, kind, workspacePath)` — a pure, store-free spec builder to which the caller supplies the already-resolved workspace path.

## Decision Outcome

Chosen option: "`TerminalLaunchSpec(target, kind, workspacePath)` — pure, store-free spec builder", returning `LaunchSpec{Argv, Dir, Env}`. The TUI store's `OpenProjectTab`/`OpenNodeTab` now call it and spawn from the returned spec via one shared helper; they no longer build commands. `NodeShell` returns `{codelimaExe, "--home", metadataRoot, "shell", nodeID}` with the executable resolved in the Service (`os.Executable` + `resolveCodelimaExecutablePath`); `ProjectHostShell` validates the supplied workspace path and returns `interactiveShellLaunchCommand()` rooted there.

The store-resolving variant was rejected because it is incompatible with the frozen behavior: `OpenProjectTab` already receives a fully-resolved `Project` (it never resolved from the store), and the characterization suite opens `project:p1` twice in one store with contradictory workspace paths — the first must fail validation, the second must succeed — which a store lookup keyed by id cannot reproduce. The caller therefore resolves the entity (the TUI holds it; the daemon will resolve it under its own locks) and passes the workspace path; the Service validates it and stays a pure spec builder returning typed `AppError`s. This preserves observable behavior exactly, keeps the characterization suite green and unmodified, and gives the daemon the same seam.

### Positive Consequences

* The Service is the single source of terminal commands; the "enter the VM through CodeLima" invariant is now structural, not conventional.
* Terminal launch is unit-testable without a PTY (`TerminalLaunchSpec` is a pure function over its inputs).
* TODO #18 is fixed once, at the launch site: the INPUTRC temp file probes `$HOME`, then a project-rooted `tmp`, then `$TMPDIR`, and skips the customization if none is writable.
* The daemon and any future front end reuse the same contract.

### Negative Consequences

* Callers must resolve the project/node and pass the workspace path — a small responsibility left with the front end rather than centralized.
* `TerminalLaunchSpec`'s signature diverges from the plan's `(target, kind)` sketch (an intentional, documented divergence).

## Links

* Builds on the terminal identity model (ADR 61) — `TargetKey`/`TerminalKind`.
* Consumed by the runtime-actor refactor (IMPROVEMENT_PLAN Track 2.1) and the daemon (Track 3).
* Resolves TODO #18.
