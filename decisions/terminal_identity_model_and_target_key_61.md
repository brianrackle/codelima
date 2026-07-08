# Model terminal identity explicitly and parse target keys through one chokepoint

## Context and Problem Statement

The TUI identifies the project or node a terminal belongs to with ad-hoc strings: `"project:" + id` and `"node:" + id` were hand-built at roughly thirty call sites and hand-parsed with `strings.HasPrefix`/`strings.TrimPrefix` at roughly twenty more, spread across `tui_model.go` and `tui_vaxis.go`. Nothing named the concept, no single place owned parsing, and the durable identity of a *live terminal* (its PTY, child process, and emulator state) was conflated with the *view key* used to look it up. As CodeLima moves toward a daemon that owns terminal runtimes outside the UI (IMPROVEMENT_PLAN Tracks 1–4), that identity has to become explicit and stop being derivable from wherever a terminal happens to be shown.

## Decision Drivers

* Live terminal runtimes must eventually be owned outside view state and keyed by a durable identity that survives any change in how terminals are arranged, displayed, or owned.
* The on-disk and on-screen `"project:<id>"`/`"node:<id>"` strings must not change — this is a mechanical migration with no user-visible effect.
* Parsing of those keys should happen in exactly one place so future format questions have a single owner.
* The new identity code must not depend on the parent `codelima` package, so the dependency arrow points inward and the daemon can reuse it.

## Considered Options

* Leave the stringly-typed prefixes in place and keep hand-building/hand-parsing them.
* Introduce a `TargetKey` value type plus an opaque `TerminalID`, with one `ParseTargetKey` chokepoint, in a leaf `terminal` package.
* Replace both the target key and the session (`#n`) key with a single richer identifier now.

## Decision Outcome

Chosen option: "Introduce a `TargetKey` value type plus an opaque `TerminalID`, with one `ParseTargetKey` chokepoint, in a leaf `terminal` package", because it makes identity explicit and testable without changing any observable string, and it stages cleanly into the later runtime-registry and daemon work.

`internal/codelima/terminal` now defines `TargetKind` (`TargetProject`, `TargetNode`), `TargetKey{Kind, ID}` whose `String()` renders byte-identically to today's `"project:<id>"`/`"node:<id>"`, and `ParseTargetKey(string) (TargetKey, error)` as the sole parser. Every hand-built prefix construction became `terminal.ProjectTarget(id).String()` / `terminal.NodeTarget(id).String()`, and every prefix parse became `ParseTargetKey` (directly, or through the thin `isTargetKind` boolean helper that delegates to it). `TargetKey` is comparable and may be used as a map key directly; where a string is still required at an edge (the string-typed `terminalTarget`/`hostTerminalReturnKey` fields, the `activeTabKeys`/`sessionErrors` map keys, and the mixed `ResourceKeys`/`EntryKeys` lists that also carry non-target resource keys such as `"projects"`), `.String()` is called and the field stays a string.

The opaque `TerminalID` type is defined now and documented with the rule that it is **never** derived from a tab index, pane, layout position, or the `TargetKey` — this is the herdr lesson (an agent multiplexer over the same libghostty-vt terminal library), reimplemented from the plan's description rather than from herdr's AGPL source. `TerminalID` is only *declared* in this change; it is wired into `tuiSession` in Track 1 PR2 alongside the runtime registry. The `"<target>#<n>"` session-key format is unchanged; nothing parses its `#n` suffix (session→target resolution uses the stored `session.target` field), and production and the test fake now share one `formatSessionKey` formatter.

### Positive Consequences

* Terminal-belonging is a named, comparable value with one parser, so the format has a single owner and malformed keys are rejected in one place.
* The identity types live in a leaf package that imports nothing from the parent, so the future daemon and runtime registry can depend on them without a cycle.
* No persisted or displayed string changed; the pre-existing TUI test suite passes with unchanged assertions, and new characterization tests pin the session-key, ordering, active-tab-fallback, and target-keyed error-lifecycle behavior against regression.

### Negative Consequences

* Some string-typed fields and maps at the UI edge still hold rendered `.String()` values rather than `TargetKey`s; tightening those into typed keys is deferred to Track 1 PR2/PR3 where the session and tab-bookkeeping structs are restructured.
* `TerminalID` exists without a consumer until PR2, so for one PR the package carries a type that nothing wires in yet.

## Pros and Cons of the Options

### Leave the stringly-typed prefixes in place

Keep constructing and parsing `"project:"`/`"node:"` inline everywhere.

* Good, because it requires no new code and no migration.
* Bad, because identity stays implicit and derivable from view context, blocking the daemon's runtime-registry design.
* Bad, because ~50 scattered construct/parse sites remain, each a place the format can drift.

### Introduce `TargetKey` + opaque `TerminalID` with one `ParseTargetKey` chokepoint

Add a leaf `terminal` package; migrate construction to `.String()` and parsing to `ParseTargetKey`.

* Good, because identity becomes explicit, comparable, and testable while every observable string stays the same.
* Good, because it stages incrementally: types now, session wiring and registry in later PRs.
* Bad, because it introduces a type (`TerminalID`) ahead of its consumer.

### Replace target and session keys with one richer identifier now

Collapse `"project:<id>"` and `"<target>#<n>"` into a single new scheme immediately.

* Good, because it would unify identity in one step.
* Bad, because it would change observable strings and force test assertions to change, violating the no-UX-change contract.
* Bad, because it front-loads registry/session decisions that Tracks 1–2 sequence deliberately.

## Links

* Implements Track 1 PR1 of `plans/IMPROVEMENT_PLAN.md` (Part E, "Terminal identity and runtime registry").
* Followed by Track 1 PR2 (wire `TerminalID` into `tuiSession` + runtime registry) and PR3 (move tab bookkeeping into `TargetTerminalState`).
