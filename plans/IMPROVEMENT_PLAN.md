# CodeLima Improvement Plan — Implementation Handover

> Historical implementation handover. Project-oriented object-model and terminal examples below describe the pre-schema-v3 codebase. ADR 72 and `SPEC.md` now define reusable configurations, directory-bound nodes, and node-scoped host terminals; ADR 92 supersedes the Microsandbox backend material below with schema-v4 Lima. Do not implement new work from the superseded examples.

Status: Handover — implementation-ready
Audience: the implementing team. This document assumes you know Go. It does **not** assume prior knowledge of this codebase, the TUI internals, the Ghostty bridge, PTY/process-group mechanics, or the daemon design space — everything you need is either in here or pointed to from here.
Current backend document: `../LIMA_PLAN.md`. `MICROSANDBOX_MIGRATION_PLAN.md` and its spike are retained only as historical migration evidence.

---

# Part A — Orientation

## A.1 How to use this document

I am leaving this plan behind as a self-contained brief; I will not be available for questions. It covers four things: the **code** (structure, correctness, hygiene — Track 0), the **system** (a daemon-owned terminal runtime — Tracks 1–4), the **app** (agent awareness — Track 5), and the **experience** (keybindings, onboarding, polish — Track 6). Track 7 is the engineering system that keeps the rest honest.

The single most important idea, borrowed from studying [herdr](https://github.com/ogulcancelik/herdr) (a Rust agent multiplexer that vendors the same libghostty-vt terminal library we use): **live terminal runtimes must be owned outside view state, keyed by durable terminal identities, and eventually owned by a process that outlives the UI.** Everything in Tracks 1–4 builds toward that.

**Work item format.** Every work item below follows the same shape:

- **Read first** — files/functions/ADRs to open before writing any code.
- **Steps** — numbered, in order. Each step is separately committable unless marked otherwise.
- **Tests first** — tests to write *before* the production change (AGENTS.md mandates TDD).
- **Done when** — the checklist to verify before opening the PR.
- **Pitfalls** — mistakes we predict you will make. Read them twice.
- **Process** — the ADR / TODO.md / PATTERNS.MD / QA.md obligations for this item.

**When this document is wrong, trust the code.** File/line anchors were verified against commit `cb90e7a`; lines drift, names don't — anchor on names. If you find the code contradicting a claim here, do not force the code to match the document: verify what's true, note the deviation in your PR description, and update this document in the same PR. One work item already carries a known discrepancy flag (0.4).

**Sizes:** S ≈ under a day. M ≈ 1–3 days. L ≈ a week. XL ≈ multiple weeks, must be split into the sub-items given.

## A.2 Day-one setup

```sh
make init      # installs pinned toolchain into .tooling/<os>-<arch>/: Go 1.24.1, gopls, golangci-lint 1.64.8,
               # zig 0.15.2, and builds libghostty-vt from the pinned Ghostty commit with our patch applied
make verify    # fmt + lint + test + build — CI runs exactly this, on ubuntu-24.04 and macos-14
make build     # binary at bin/<os>-<arch>/codelima  (bin/codelima is a compatibility symlink)
make tui       # build + run the TUI
make smoke     # scripts/smoke_3_layers.sh
```

Things that will trip you up on day one:

- The Go **module path is `github.com/brianrackle/test_lima`**, not "codelima". Imports and `-ldflags -X` paths use the module path.
- Almost everything lives in one flat package: `internal/codelima/` (43 files). `cmd/codelima/main.go` is 12 lines; the real entrypoint is `codelima.Run(ctx, args, stdin, stdout, stderr) int`.
- The Ghostty cgo tests **skip silently** when `libghostty-vt.{so,dylib}` is not loadable (26 `t.Skipf("ghostty terminal unavailable...")` sites). A green `make verify` on a machine without `make init` proves nothing about the terminal layer. After setup, run `go test ./internal/codelima -run Ghostty -v` and confirm zero skips. (Track 7.2 makes CI fail on skips; until then, discipline.)
- Tool caches (`GOMODCACHE`, `GOCACHE`, lint cache) are repo-scoped under `.tooling/`. Temp work goes under project-rooted `./tmp/`, never system temp (AGENTS.md rule).
- `CODELIMA_HOME` defaults to `~/.codelima` (override: `--home` flag, then `CODELIMA_HOME` env). Use a throwaway home for all manual testing: `codelima --home ./tmp/qa-home ...`.

## A.3 The process you must follow

`AGENTS.md` is binding. The digest — treat this as your per-PR checklist:

1. **TDD**: write the failing test first. "Every change will be fully tested with automated tests, and the tests will pass for the change to be considered completed."
2. **Run it**: "When work is complete the code needs to be run and verified locally." For TUI-facing changes, also run the relevant `QA.md` flow (QA.md is a runbook of `## <Flow> Verification` sections with setup/steps/expected/cleanup).
3. **ADRs**: any change to how the system works internally (runtime integrations, rendering behavior, storage layout, architecture) needs a numbered ADR in `decisions/` from `ADR_TEMPLATE.md` (MADR format). Filenames are `<slug>_<n>.md`; pick the next free integer (there are ~58 files and some historical duplicate numbers — check `ls decisions/ | grep -o '[0-9]*\.md'` and go one past the max). Each track below says which ADRs it owes.
4. **TODO.md**: any scoped-out or deferred follow-up gets an entry *before you move on*, in the house format: `### N. Title` + Problem / Suggested solution / Advantages / Disadvantages.
5. **PATTERNS.MD**: reusable patterns get documented there; several existing entries are the authoritative spec for behavior you must preserve (especially "TUI Session Reuse").
6. **ROADMAP.md**: mark items complete/partially-complete as you deliver them.
7. `make verify` green locally before every PR. One work item = one PR unless the item says otherwise.

## A.4 Non-negotiable rules

1. **Never copy code from herdr.** It is AGPL-3.0 dual-licensed. We borrow architecture, protocol shapes, and constants-level design decisions — reimplemented from scratch in Go, from the descriptions in this document only. Nobody reads herdr source while implementing. Using libghostty-vt directly is fine; that is Ghostty's library under Ghostty's license, and we already vendor and patch it ourselves.
2. **Do the tracks in order within each dependency chain.** Do not start the daemon before the registry exists. Do not start live-update before the daemon is stable. The sequencing table in Part D is the contract.
3. **No-UX-change contract for Tracks 1–2.** The existing TUI test suite enforces semantics. If a test's *assertions* must change during Tracks 1–2, you broke semantics — stop and re-check. Constructor/plumbing changes are fine.

---

# Part B — Verified architecture map

Read this section fully before touching anything. Every claim was verified against the code at `cb90e7a`.

## B.1 Layering

```text
cmd/codelima/main.go            → codelima.Run(ctx, args, stdio…)        (12 lines)
internal/codelima/cli.go        → parseGlobalOptions → LoadConfig → NewService → dispatch
internal/codelima/service.go    → Service: the control plane (all business logic)
internal/codelima/store.go      → Store: pure filesystem I/O under CODELIMA_HOME
internal/codelima/lima.go       → LimaClient interface + ExecLimaClient (shells out to limactl)
internal/codelima/tui_*.go      → the Vaxis TUI + Ghostty embedded-terminal backend
```

`Service` struct (`service.go:18-27`): `cfg Config`, `store *Store`, `lima LimaClient`, `tui TUIRunner`, `stdin/stdout/stderr`, `now func() time.Time`. Constructor `NewService(cfg, lima, stdin, stdout, stderr)` — passing `lima=nil` gets the real `ExecLimaClient`; tests pass fakes. `withIO(stdout, stderr)` shallow-clones the Service for background-operation output capture (one caller, in the TUI).

The four injection seams — these are what make everything testable, and what every track builds on:

| Seam | Definition | Production impl | Test impl |
|---|---|---|---|
| `LimaClient` | `lima.go:22-32` — `BaseTemplate, List, Create, Start, Stop, Delete, Clone, CopyToGuest, Shell` | `ExecLimaClient` (shells out via `sh -lc` command templates) | `fakeLima` (`service_test.go:12`) |
| `TUIRunner` | `tui_model.go:10-12` — `Run(ctx, *Service, workspaceRoot) error` | `vaxisTUIRunner` | `fakeTUIRunner` |
| `tuiSessionManager` | `tui_model.go:35-40` — `HasSession, TargetSessionKeys, OpenProjectTab, OpenNodeTab` | `*tuiSessionStore` | `fakeTUISessionManager`, `sharedFakeTUISessionManager` |
| `newSessionTUITerminal` | package var (`tui_vaxis.go:70`), default `newTUITerminal` | Ghostty backend, Vaxis fallback | tests reassign it to return `fakeTUITerminal` (restore with defer) |

## B.2 The metadata store

`CODELIMA_HOME` layout (created by `Store.EnsureLayout`, `store.go:21-73`):

```text
_config/config.yaml               global config
_config/agent-profiles/           seeded: codex-cli.yaml, claude-code.yaml
_locks/<key>.lock                 advisory flocks; keys: projects, nodes, patches, environment-configs
_index/projects/by-slug/          slug → id ref files
_index/nodes/by-instance/         lima instance name → node id
_index/environment-configs/by-slug/
_index/patches/by-status/         DEAD (Track 0.6)
environment-configs/<id>/environment-config.yaml
projects/<id>/project.yaml  + snapshots/<id>/{manifest.json,tree/}
nodes/<id>/node.yaml, bootstrap.json, events.jsonl, context.jsonl,
           instance.lima.yaml, lima-instance.ref
patches/<id>/                     DEAD (Track 0.6)
```

Locking (`locks.go`): `acquireLocks(root, keys...)` sorts keys (deadlock prevention), takes blocking exclusive `flock` on `_locks/<key>.lock`. **Mutations lock; reads don't.** That would be fine, except reads currently *write* (see 0.3) — without locks. Write durability: all metadata goes through `atomicWriteFile` (`fsutil.go:40`): temp file + rename, **no fsync** (see 0.7).

Error taxonomy (`errors.go`): `AppError{Category, Message, Code, Fields, Err}` with exit codes — InvalidArgument=2, DependencyUnavailable=3, NotFound=4, PreconditionFailed=5 (also UnsupportedFeature, PatchConflict), ExternalCommandFailed=6, InternalFailure=7 (also MetadataCorruption). Use the matching constructor (`invalidArgument(...)`, `notFound(...)`, …) for every new user-facing error.

## B.3 How a terminal tab works today

This is the machinery Tracks 0–3 restructure. The narrative, verified:

1. **Open.** `tuiSessionStore.OpenNodeTab(node)` (`tui_vaxis.go:159`) builds `exec.CommandContext(ctx, codelimaExe, "--home", <MetadataRoot>, "shell", node.ID)` — the TUI re-invokes the codelima binary itself. `OpenProjectTab` (`:112`) instead runs `interactiveShellLaunchCommand()` (`service.go:1882` — a host shell wrapped with a temp `INPUTRC`, see TODO #18) with `Dir = project.WorkspacePath`. Both then: `key := nextSessionKey(targetKey)` → `terminal := newSessionTUITerminal(key, postEvent)` → `terminal.Start(cmd)` → `putSession(&tuiSession{...})`.
2. **Session keys.** Target keys are strings: `"project:<id>"` / `"node:<id>"`. Session keys are `fmt.Sprintf("%s#%d", targetKey, counter)` (`nextSessionKey`, `tui_vaxis.go:79-85`; per-target counter only ever increments). Important fact: **nothing ever parses the `#n` suffix** — session→target resolution uses the stored `session.target` field. The `project:`/`node:` prefixes, however, are hand-built at ~25 sites and hand-parsed (`strings.HasPrefix`/`TrimPrefix`) at ~15 sites across `tui_model.go`/`tui_vaxis.go`. Track 1 replaces those.
3. **Spawn.** The Ghostty backend (`tui_terminal_ghostty_cgo.go`, build tag `cgo && (darwin || linux)`) starts the child with `pty.StartWithAttrs(cmd, &pty.Winsize{...}, &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 1})` (`:1162-1170`) — the child is a **session leader**, so its process-group id equals its pid. It forces `TERM=xterm-256color`.
4. **I/O.** A `readLoop` goroutine (`:1202`) reads the PTY master (32 KiB buffer) and feeds bytes into the libghostty-vt terminal emulator via cgo (`ingestPTY` → `ghostty_bridge_terminal_write`); VT query responses are written back through the writer. Writes to the PTY go through `ghosttyPTYWriter` (`:247-431`): an unbounded `bytes.Buffer` guarded by a `sync.Cond`, drained by one goroutine, with EAGAIN handling that is supposed to `poll(POLLOUT)` — but the production constructor passes `waitWritable=nil`, so a would-block currently **busy-spins** (fixed in Track 2.1). All cgo calls are wrapped in `withGhosttyStderrSuppressed` (`:66`), which dup2's `/dev/null` over stderr — process-global, not concurrency-safe (fixed in 0.5).
5. **Teardown — the bug farm.** There are two near-duplicate teardown paths: `Close()` (`:1686`, user closes the tab: closes writer, closes PTY, `cmd.Process.Kill()` — **only the direct child** — then waits and frees cgo handles) and `finish(err)` (`:1799`, child exited on its own: same resource release, no kill, posts `tuiTerminalClosedEvent`). Killing only the direct child orphans grandchildren (Track 0.1). When the Ghostty library is unavailable, `newTUITerminal` falls back to a Vaxis widget terminal (`tui_terminal_vaxis.go`) which wraps `vaxis/widgets/term` and has its own quirks (reflection — 0.8).
6. **Auto-refresh.** `Run` starts a 2-second ticker (`tuiAutoRefreshInterval`, `tui_vaxis.go:386`) posting `tuiRefreshTickEvent`; `startDataRefresh` single-flights (drops overlapping ticks) and reloads the project tree in a goroutine, re-entering the event loop via `tuiRefreshCompleteEvent`. That reload path calls `Service.ProjectTree*` → `reconcileNodes(context.Background(), nodes, persist=true)` — which **writes every `node.yaml` to disk on every tick, holding no lock** (0.3).

One coupling fact that matters for Tracks 1–2: `tuiState.sessions` holds the `tuiSessionManager` *interface*, but `vaxisTUIApp.sessions` holds the *concrete* `*tuiSessionStore` and reaches into its maps directly (`a.sessions.sessionErrors[...]`, e.g. `tui_vaxis.go:1160`). Part of Track 1 is closing that gap.

## B.4 Test infrastructure you will reuse

~202 test functions in 15 `_test.go` files; the heavyweights are `tui_test.go` (~4,500 lines, 85 tests) and `service_test.go` (~2,200 lines). There is **no fake `limactl` binary and no integration-test tier yet** (Track 7.2 adds one) — everything runs through interface fakes:

| Fake | Where | What it does |
|---|---|---|
| `fakeLima` | `service_test.go:12`, ctor `newFakeLima()` | Full `LimaClient` fake. Records `calls`/`invocations`/`shellCalls`/`copyCalls`; injectable failures (`createErr`, `failCommand`, `listErr`); holds `observations map[string]RuntimeObservation`. Includes **concurrency gates** (`fakeLimaGate` + `awaitFakeLimaGate`) that block a lifecycle call until the test releases it — this is how async background-operation TUI flows are tested deterministically. |
| `fakeTUIRunner` | `tui_test.go:21` | Asserts CLI dispatch launches the TUI without a terminal. |
| `fakeVaxisConsole` | `tui_test.go:26-99` | Implements `containerd/console.Console`; feeds a canned DA1 reply so a **real** `vaxis.New` runs with no tty (`newRenderTestVaxis`). Use for render tests. |
| `fakeTUISessionManager` | `tui_test.go:183` | Pure in-memory `tuiSessionManager`; duplicates the `"%s#%d"` key format at `:314`. For `tuiState` model tests. |
| `sharedFakeTUISessionManager` | `tui_test.go:189` | Wraps a **real** `tuiSessionStore` but overrides `Open*Tab` to insert `fakeTUITerminal`s — model + store together, no PTY. |
| `fakeTUITerminal` | `tui_test.go:194` | Records Start/Resize/Focus/Blur/events; canned snapshot for Draw. Injected by reassigning the package var `newSessionTUITerminal` (restore with defer) — see `tui_test.go:1400` for the pattern. |

Helpers worth knowing: `newTestTUIApp` (`tui_test.go:4307`), `newAsyncTestTUIApp` (`:4330`, buffered event channel + shared fake store), `putTestSession`, `newRenderTestVaxis`.

## B.5 Glossary

- **PTY (pseudo-terminal)**: a kernel-provided pair of file descriptors; the *master* side is held by the terminal emulator (us), the *slave* side becomes the child's stdin/stdout/stderr and controlling terminal. Closing the master sends `SIGHUP` to the child's foreground process group.
- **Session / process group / pgid**: Unix processes belong to a process group; groups belong to a session. `Setsid: true` makes the child a new session leader, so `pgid == pid`, and its descendants inherit that pgid (unless they call setpgid themselves). `syscall.Kill(-pid, sig)` signals the *whole group* — that's the Track 0.1 fix.
- **flock**: advisory file lock. Only cooperating processes that also call flock are excluded — it does not stop a rogue writer.
- **Actor**: a goroutine that *owns* a piece of state exclusively; all other code interacts by sending commands over a channel. No mutexes on the hot path, no shared mutation.
- **JSON-lines**: one JSON object per `\n`-terminated line. Trivial to parse with `bufio.Scanner` (mind the token size limit) or `json.Decoder`.
- **Unix domain socket**: local socket addressed by filesystem path; supports credentials and **`SCM_RIGHTS`** — passing open file descriptors to another process (Track 4's PTY handoff).
- **OSC sequences**: in-band terminal escapes. OSC 52 = clipboard write (we forward it); OSC 8 = hyperlinks.
- **Characterization test**: a test written *before* a refactor that pins down current observable behavior — including behavior nobody designed — so the refactor can prove it changed nothing.
- **cgo seam**: the Ghostty backend is compiled only with `cgo && (darwin || linux)`; a stub (`tui_terminal_ghostty_stub.go`) errors out otherwise, triggering the Vaxis fallback.

---

# Part C — The destination

```text
codelima CLI (one binary)
  ├── direct commands (project/node/environment — unchanged)
  ├── daemon lifecycle: codelima daemon start|stop|status|update
  └── API client commands: codelima terminal list|read|send …

codelima daemon (same binary, `codelima daemon run`)
  ├── terminal runtime registry (owns PTYs, child processes, Ghostty terminal state)
  ├── per-target tab/session metadata + persistence
  ├── local API socket (JSON-lines request/response)
  ├── client attach socket (events + input + frames)
  ├── agent status detection
  └── live-update handoff (last)

codelima TUI (thin-ish client)
  ├── renders tree/chrome locally with Vaxis (unchanged look)
  ├── renders terminal viewports from daemon-provided cell state
  ├── sends input/focus/resize; subscribes to events
  └── detach/reattach without killing terminals
```

Why this is worth the effort, in product terms:

- **Quit the TUI without killing your agents.** Today every Codex/Claude session dies with the TUI process. This is the single biggest UX defect for the core use case.
- **`codelima terminal read/send` from the CLI** turns every embedded terminal into an automation surface — agents can orchestrate other agents' terminals.
- **Agent monitoring becomes cheap.** Once the daemon owns terminal screen state, blocked/working/done detection is a pure function over screen snapshots.
- **Upgrades stop being session-destroying.** Live handoff (Track 4) lets the daemon replace itself while PTYs survive.

What we deliberately keep: `CODELIMA_HOME` stays the metadata store; the `Service` layer stays the control plane (the daemon *hosts* it, it does not replace it); the current tab UX semantics are preserved exactly. ADR 55 originally selected an outright Lima replacement. The initial microsandbox close probe failed with its minimal agent as guest PID 1; the supported real-init configuration then passed the complete automated E1 matrix in the available environment. The decision remains reopened until the remaining Phase 0 hard gates and release qualifications pass. Continue to work against Lima as-is until then; do not read later `msb`/`SandboxClient` references as an approved implementation direction.

---

# Part D — Track 0: Stabilize (first; mostly parallelizable)

These are prerequisites. Several become ten times harder to retrofit after the daemon exists, and the daemon will amplify every lifecycle bug it inherits.

## 0.1 Kill process groups, not PIDs — **M**

**Read first:** `tui_terminal_ghostty_cgo.go` — `Start` (`:1144`), `Close` (`:1686`), `finish` (`:1799`), `wait` (`:1842`); `tui_terminal_vaxis.go`; glossary entries for PTY and process groups.

**Why.** For node tabs the child chain is `codelima shell <node>` → (inside `ExecLimaClient`) `sh -lc "limactl shell …"` → `ssh`. `Close` calls `cmd.Process.Kill()` on the top PID only; the descendants reparent to init and can keep the VM shell alive after the tab is gone. The child is already a session leader (`Setsid: true`), so its pgid == pid — group-kill is available for free.

**Steps.**

1. New file `tui_terminal_shutdown.go` (unix-only build tag): one shared helper, used by *both* backends:
   ```go
   // shutdownTerminalProcess closes writer+pty, then escalates on the child's
   // process group: SIGHUP (implicit, via pty close) → SIGTERM → SIGKILL.
   // done is closed when cmd.Wait has returned (the caller's reaper goroutine).
   func shutdownTerminalProcess(pid int, closeIO func(), done <-chan struct{}) error
   ```
   Sequence inside: `closeIO()` (writer first, then PTY master — closing the master delivers SIGHUP to the foreground group); wait ≤250ms on `done`; `syscall.Kill(-pid, syscall.SIGTERM)`; wait ≤250ms; `syscall.Kill(-pid, syscall.SIGKILL)`; wait ≤2s with ~20ms polling if `done` can't be relied on. Ignore `ESRCH` (already gone). The 250ms/20ms constants are herdr's field-tested values — reimplemented, not copied.
2. Reaping: the existing `wait()` (`waitOnce`-guarded `cmd.Wait()`) becomes the single reaper; have `Start` launch `go func() { t.waitErr = t.wait(); close(t.waitDone) }()` so both `Close` and `finish` can select on `waitDone`.
3. Rewrite `Close` and `finish` to share one internal `teardown(kill bool, postEvent bool, err error)` that calls the helper and then frees cgo handles exactly once. The duplicated resource-release block (ptyWriter.Close → pty.Close → `ghostty_bridge_terminal_free` → encoder closes → nil-out) is how this bug survived; it must exist in one place afterward.
4. Vaxis fallback: `vaxisTUITerminal.Close()` should call `model.Close()` then group-kill via the same helper using the `*exec.Cmd` it passed to `Start` (keep a reference).
5. Document (do not solve) the known limitation: a shell running job control can place jobs in their own process groups inside the session; group-kill of the leader's group covers our real chains today. Enumerating session members via `/proc` is later hardening — record it in TODO.md.

**Tests first.** In `tui_terminal_ghostty_cgo_test.go` (uses the existing skip-if-unavailable pattern):

```go
func TestCloseKillsGrandchildProcesses(t *testing.T)
```
Start the terminal with `/bin/sh -c 'sleep 300 & echo GRANDCHILD=$!; exec sleep 300'`. Poll the terminal's rendered content (`String()`/snapshot, as existing tests do) until `GRANDCHILD=<pid>` appears; parse the pid. Call `Close()`. Poll `syscall.Kill(pid, 0)` until it returns `ESRCH`, deadline 3s; fail on timeout. Add a companion test asserting the *direct* child is reaped (no zombie: `cmd.ProcessState != nil` after Close returns). This is currently the highest-value missing test in the repo.

**Done when:** both tests pass on Linux and macOS with the library present; `Close` and `finish` share one teardown body; the Vaxis backend routes through the same helper; `make verify` green.

**Pitfalls.** (1) Do not `Kill(-pid)` before closing the PTY — give SIGHUP its 250ms; some shells save history on HUP. (2) `cmd.Wait()` may only be called once — everything must go through the `waitOnce` reaper. (3) On macOS there is no `/proc`; don't write polling code that assumes it. (4) `ESRCH` from `Kill` is success, not an error.

**Process:** ADR (terminal process-group termination policy + escalation constants). PATTERNS.MD entry ("Group Kill Session Leader"). Update TODO.md with the job-control limitation.

## 0.2 Handle signals — **S**

**Read first:** `cmd/codelima/main.go` (all 12 lines), `vaxisTUIRunner.Run` (`tui_vaxis.go:389-448`).

**Steps.** (1) In `main.go`: `ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM); defer stop()`, pass `ctx` to `codelima.Run`. (2) In `Run` (`tui_vaxis.go`): the event loop already selects on `ctx.Done()`; verify that path runs `sessions.Close()` (which iterates all sessions closing terminals — after 0.1, group-killing them) and restores the host terminal (Vaxis teardown) via defers, and add whatever is missing. (3) Non-TUI CLI paths: in-flight `limactl` children get the cancelled ctx via `exec.CommandContext` — verify `ExecLimaClient` uses the passed ctx everywhere (it does, except the 0.3 `context.Background()` sites).

**Tests first.** `TestRunClosesSessionsOnContextCancel` using `newAsyncTestTUIApp` + `sharedFakeTUISessionManager`: cancel the ctx, assert every `fakeTUITerminal` got `Close()` and `Run` returned.

**Done when:** Ctrl+C in the TUI exits cleanly — terminal restored to cooked mode, no orphaned VM shells (`ps` shows no leftover `limactl shell`/`ssh`), test green. QA note added to the TUI flow.

## 0.3 Reads must not write — **M**

**Read first:** `reconcileNodes` (`service.go:2020`), `reconcileNodeWithObservations` (`:2039`), its two callers (`:562` in `projectTreeData`, `:902` in `NodeList`), `EnsureReady` (`:130`), `EnsureLayout` (`store.go:21-73`), `ensureBuiltInEnvironmentConfigs` (`store.go:188-231`), TODO #20, B.3 step 6.

**Why.** Two separate write-on-read mechanisms exist, and they compound:

- `reconcileNodes(…, persist=true)` — its only two callers are the read surfaces (project tree and `NodeList`), both passing `context.Background()`. With `persist=true` it calls `store.SaveNode` for **every node on every call** — every 2 seconds from the TUI auto-refresh goroutine, holding **no `nodes` flock** (read paths take no locks — verified), racing any foreground mutation.
- `EnsureReady(false)` runs the full `EnsureLayout()` on every read: rewrites `config.yaml` if "stale", re-seeds agent profiles and environment configs, and rewrites any project.yaml/node.yaml failing a "needs refresh" check. `ensureBuiltInEnvironmentConfigs` creates a **fresh `newID()`** when the slug lookup misses — two concurrent callers (TUI refresh + CLI) can each create one. This is almost certainly TODO #20 (a fresh home showed *three* `codex` and *three* `claude-code` rows).

**Steps.**

1. Change both `reconcileNodes` call sites to `persist=false` and thread the caller's real `ctx` (also fix `validateDependencies`' `lima.List(context.Background())` at `:155`). Change `NodeShow`'s `reconcileNode(…, true)` (`:945`) to `false` — showing is a read. Keep `persist=true` only inside explicit lifecycle transitions: `NodeStart` (`:1050`), `NodeStop` (`:1109`), `NodeClone`'s post-transition sites. Audit each `reconcileNode` caller against the rule: *persist only when the surrounding operation already holds the `nodes` lock.*
2. Split `EnsureLayout` into `ensureDirectories()` (mkdirs only — idempotent, cheap, safe for reads) and `seedAndRepair(now)` (config rewrite + profile/env-config seeding + metadata refresh). `EnsureReady(mutating=false)` calls only `ensureDirectories` (guard with `sync.Once` per Service instance). `EnsureReady(mutating=true)` and a new `doctor --repair` flag call `seedAndRepair` — **while holding the `environment-configs`, `projects`, and `nodes` flocks**.
3. Delete nothing else: `LastReconciledAt`/`LastRuntimeObservation` still get set in-memory on reads (that's ADR 37's batch-reconcile pattern — status stays live); they just stop being persisted from reads.

**Tests first.**

- `TestFreshHomeSeedsSingleBuiltInEnvironmentConfigs` (TODO #20's regression): fresh home → `EnsureReady(true)` → `EnvironmentConfigList` returns exactly one `codex` and one `claude-code`.
- `TestConcurrentSeedingDoesNotDuplicate`: N goroutines calling `EnsureReady(true)` on a fresh home under `-race`; same assertion.
- `TestReadSurfacesDoNotWrite`: create project+node, record mtimes of every file under the home, run `ProjectTree` + `NodeList` + `NodeShow` + a simulated refresh tick, assert no mtime changed.
- `TestRefreshDoesNotRaceMutation` (run under `-race`): loop `NodeList` in a goroutine while `NodeStop`/`NodeStart` runs in another, against `fakeLima` with gates.

**Done when:** all four tests green under `go test -race ./internal/codelima`; disk stays quiet while the TUI idles (verify manually: `find <home> -newer /tmp/stamp` after 30s of idle TUI shows nothing).

**Pitfalls.** (1) `configFileNeedsRefresh` exists to migrate old config files — moving it behind `mutating=true` means a stale config is only upgraded by a mutating command or doctor; that's acceptable, document it in the doctor help. (2) Don't add internal locking to `Store` — keep the lock discipline at the Service operation level where it lives today. (3) The TUI refresh goroutine must keep working against a *read-only* path — check `loadTUIProjectTree` doesn't depend on `EnsureReady(true)` side effects.

**Process:** ADR (read/write split + seeding moves to mutating paths — this supersedes part of the current `EnsureLayout` behavior described in PATTERNS.MD "Built-In Metadata Seeding"; update that entry). Mark TODO #20 resolved.

## 0.4 `node delete` orphan bug (TODO #10) — verify, then fix — **S–M**

**Known discrepancy — read carefully.** TODO #10 and QA record a repro: `node delete` removed metadata, then failed `NotFound: node not found` while the Lima VM kept running. But the *current* `NodeDelete` (`service.go:1244-1296`) already orders correctly: it marks `terminating` → `lima.Delete` → only then marks `terminated` (which is when `SaveNode` removes the instance index), and a failed teardown returns early leaving the node listable in `terminating`. It also never removes the node directory (soft delete). So the recorded repro does not match this code path. Prime suspect: **`NodeCleanupIncomplete`** → `RemoveIncompleteNodeMetadata` (`store.go:830`), which `os.RemoveAll`s node dirs missing `node.yaml` — if a node dir became "incomplete" (failed create, partial write), cleanup deletes the metadata while the Lima instance lives on, and `lima-instance.ref` goes with it.

**Steps.** (1) Reproduce before fixing: write a test that creates an incomplete node dir *with* a `lima-instance.ref` pointing to a live fake-Lima instance, runs `NodeCleanupIncomplete`, and observe: today the instance survives with its metadata gone. (2) Fix: cleanup must consult `lima.List` and, for any incomplete dir whose ref matches a live instance, either tear the instance down first (then remove metadata) or refuse with an actionable error naming the instance. Pick teardown-first; it matches user intent. (3) Also add the direct `NodeDelete` regression tests, so the correct ordering is pinned: delete a running node (fake Lima) → both metadata marked terminated and instance deleted; inject `lima.Delete` failure → node still listable in `terminating`, instance index still present, second delete attempt succeeds after the fake recovers.

**Done when:** all three tests green; the QA "Doctor And Incomplete Node Cleanup" flow re-run manually against a real Lima instance and its cleanup steps updated. If your investigation finds a *different* orphaning path than the suspect above, document what you found in TODO #10 before closing it.

**Process:** update TODO #10 with root cause; QA.md cleanup-flow update; no ADR unless you change the delete state machine.

## 0.5 Real observability — **M**

**Read first:** `cli.go:68-97` (`parseGlobalOptions` — `--log-level` is parsed into `options.LogLevel` at `:82-87` and then **never read anywhere**), `withGhosttyStderrSuppressed` (`tui_terminal_ghostty_cgo.go:66-92`), the `a.status` string field and operation output capping in `tui_vaxis.go`.

**Steps.**

1. Add `logger *slog.Logger` to `Service` (constructor param with sane default). Wire `--log-level` → `slog.LevelVar`. Sinks: CLI commands → `slog.NewTextHandler(stderr)`; TUI (and later the daemon) → append-only file `CODELIMA_HOME/_logs/codelima.log` with dumb size-based rotation (at ~5 MB rename to `.1`, keep one generation — no new dependencies).
2. Replace the `/dev/null` sink in `withGhosttyStderrSuppressed` with a pipe whose reader goroutine logs each line at `debug` level, tagged `source=libghostty`. Keep the `sync.Once`+mutex structure; note in a comment that the dup2 is process-global (this also fixes the concurrency hazard when two terminals initialize simultaneously).
3. Sprinkle log lines at the seams only — service operation start/finish (with duration + error), lima command invocations (template name, not full argv — argv can hold paths), terminal open/close, refresh errors. Do not log per-tick refresh success.
4. TUI message surface: replace the single overwritable `a.status` string with a ring buffer (`tuiMessageLog`, cap ~200 entries, each `{time, level, text}`). The status line renders the newest entry as today; a new key (wire through the existing dialog/menu layer) opens a scrollable messages view. Background-operation output (currently capped at 200 lines and discarded on completion) gets retained in the ring on completion/failure.

**Tests first.** Logger plumbing: `TestLogLevelFlagControlsVerbosity` (capture stderr handler output at `warn` vs `debug`). Ring buffer: unit tests for append/overflow/ordering. Ghostty stderr capture: cgo test that triggers a bridge call and asserts the suppression path doesn't deadlock (exact log content is not assertable — libghostty may print nothing; assert plumbing, not output).

**Done when:** `codelima --log-level debug node list` prints structured logs to stderr; the TUI writes `_logs/codelima.log`; the messages view scrolls; failed background operations remain inspectable after completion. QA.md TUI flow gains a "messages view" step.

**Process:** ADR (logging architecture: sinks per mode, libghostty capture). ROADMAP/TODO sweeps as appropriate.

## 0.6 Delete the dead Patch subsystem — **S**

**Read first:** `service.go:1343-1642` (`PatchPropose/List/Show/Approve/Reject/Apply`), `patches.go`, `SavePatch` + patch store methods + `OrphanedPatchStatusIndexes` (`store.go:858-908`, `:1045`), types `PatchProposal`/`DiffSummary`/`ApplyResult`/`ConflictSummary`/`ApprovalMetadata` (`types.go:293-336`), patch consts (`types.go:30-39`).

Verified: the CLI has **no `patch` command group** (`isCommandGroup`, `cli.go:132-138`), the TUI never calls Patch methods, and the only callers are `service_test.go:1240-1320`. **Keep** `captureSnapshot` and `materializeSnapshot` (`snapshot.go`) — `ProjectFork` uses both (`service.go:695`, `:704`); `materializeSnapshot`'s *only* caller is ProjectFork, so snapshot.go stays intact.

**Steps.** (1) Delete the six Service methods, `patches.go`, patch store methods, the `_index/patches/by-status` mkdir in `EnsureLayout`, `OrphanedPatchStatusIndexes` + its doctor wiring, the `patches` lock-key uses, the five types, and the patch tests. (2) Grep sweep: `grep -rn -i 'patch' internal/ --include='*.go'` — the only survivors should be `scripts/patches/` references and unrelated words. (3) The copy-mode file-return replacement (TODO #8) is designed fresh later as `codelima node export`/`sync` — write nothing now beyond confirming TODO #8 still says that.

**Done when:** `make verify` green; `ProjectFork` tests still pass; home dirs created by the new code have no `patches/` or `_index/patches/`; existing homes with those dirs still load fine (they're just ignored — add a test).

**Process:** ADR recording the deletion and the survival of snapshot.go via ProjectFork. Update PATTERNS.MD if any entry references patches.

## 0.7 Small correctness backlog — **S each**

1. **TODO #6 — duplicated stdout from `codelima shell <node> -- <cmd>`.** Verified mechanism worth your attention: in `ExecLimaClient.Shell` (`lima.go:393-447`), resolved *pre-commands* run via `runCommandString(..., multiWriter(streams.Stdout, c.Stdout), ...)` (`:411`, `:436`) — both arguments are ultimately the process stdout but are **not pointer-identical**, and `sameWriter` (`:514-526`) only de-dups on comparable-type pointer equality, so output writes twice. The interactive final command wires `streams.*` directly and is not doubled. Fix: stop passing both writers (the client's own `c.Stdout` should not be layered on top of caller streams for shell pre-commands), or make de-dup unnecessary by design — do **not** "improve" the reflection in `sameWriter`. Test: fake-free unit test on `ExecLimaClient` with a stub runner asserting a single-line command produces exactly one line.
2. **Swallowed rollback errors.** `service.go:1003, :1019, :1035` (`NodeStart` failure paths: seed / bootstrap-command / validation-command) each do `_ = s.store.SaveNode(...)` then `_ = s.store.AppendNodeEvent(...)`. After 0.5, change to `if err := ...; err != nil { s.logger.Error("rollback save failed", ...) }`. Test by injecting a store failure (read-only home dir) and asserting the log record.
3. **`atomicWriteFile` durability.** Verified: temp+rename, no fsync. Add `f.Sync()` before close, and fsync the parent directory after rename (open dir, `dir.Sync()`, ignore `EINVAL` on platforms/filesystems that reject dir fsync — macOS may). Benchmark once (`go test -bench`) to confirm metadata writes stay sub-millisecond-ish; if the TUI-tick writes from 0.3 were still in place this would hurt — do 0.3 first.

## 0.8 Stop reflecting into vaxis internals — **S**

**Read first:** `renderedHyperlinkAt` (`tui_runtime.go:195-231` — reflects `*vaxis.Vaxis` → unexported `screenNext.buf[row][col].Style.Hyperlink`), `CapturesMouse` (`tui_terminal_vaxis.go:105-124` — reflects `term.Model`'s unexported `mode` bools), consumer at `tui_vaxis.go:968`.

**Steps.** (1) File upstream issues/PRs against vaxis proposing small accessor APIs (a cell/hyperlink accessor; a mouse-mode query on `term.Model`); link them in code comments. (2) Interim: add canary tests `TestVaxisHyperlinkReflectionStillValid` / `TestVaxisTermModeReflectionStillValid` that exercise the reflection against a real `vaxis.New` (use `newRenderTestVaxis`) and a real `term.Model`, failing loudly with "vaxis internals changed — see tui_runtime.go:renderedHyperlinkAt" if fields vanish. (3) Confirm go.mod pins exact vaxis versions (modules do by default; just don't bump casually). Don't over-invest: the Vaxis fallback matters less once the Ghostty path is daemon-owned.

**Done when:** canary tests exist and pass; upstream links recorded; a deliberate local rename of the reflected field (temporary hack) makes the canaries fail with the loud message.

---

# Part E — Track 1: Terminal identity and runtime registry (in-process, no UX change) — **M**

**Goal:** separate *what a terminal is* (durable identity + pure metadata) from *what runs it* (PTY, child, emulator state), while everything still lives in the TUI process. Foundation for the daemon; costs nothing user-visible.

The herdr lesson to internalize: their terminal id is an opaque allocated string, documented as *forbidden* to derive from pane id or layout position. Identity must survive any future change in how terminals are arranged, displayed, or owned.

**Read first:** B.3 and B.4 above, in full. Then `tui_model.go:35-40, :90-95, :101-115`, `tui_vaxis.go:27-51, :79-85, :112-192, :218-287`.

## 1.1 Types — new package `internal/codelima/terminal/`

The flat 43-file package is at its limit; new subsystems go in subpackages. (The TUI files import it; it imports nothing from the parent — keep the dependency arrow pointing inward.)

```go
type TerminalID string // opaque; allocate as "term_" + unix-nano-hex + "_" + counter-hex.
                       // NEVER derived from tab index, pane, or target.

type TargetKind int // TargetProject, TargetNode
type TargetKey struct {
    Kind TargetKind
    ID   string
}
func (k TargetKey) String() string    // renders exactly "project:<id>" / "node:<id>" — byte-compatible
                                      // with today's strings; existing on-disk/UX strings must not change.
func ParseTargetKey(s string) (TargetKey, error) // the ONLY place parsing may happen, ever.

type TerminalKind int // ProjectHostShell, NodeShell (leave room for AgentShell)

type TerminalState struct { // pure data — no PTY, no cgo handles, trivially serializable
    ID         TerminalID
    Target     TargetKey
    Kind       TerminalKind
    Label      string
    CWD        string
    Launch     []string
    CreatedAt  time.Time
    LastActive time.Time
    ExitStatus *TerminalExitStatus
}

type TerminalRuntime struct { // owns the live tuiTerminal backend — a deliberate migration shim
    ID TerminalID
    // wraps the existing tuiTerminal for now
}

type TerminalRuntimeRegistry struct { // plain map, single-owner on the TUI event loop; NO locking yet
    runtimes map[TerminalID]*TerminalRuntime
}

type TargetTerminalState struct { // per-target tab bookkeeping (replaces sessionOrder/tabCounters/sessionErrors)
    Target       TargetKey
    Tabs         []TerminalTabState // ordered
    ActiveTabID  TabID
    NextTabIndex int
}
type TerminalTabState struct {
    ID         TabID
    Label      string
    TerminalID TerminalID // single pane per tab in v1; becomes a layout tree in Track 6.6
}
```

## 1.2 Migration steps — three PRs, strictly in this order

**PR 1 — TargetKey (mechanical, no behavior change).** Introduce the type; replace every hand-built `"project:"+id` / `"node:"+id` (~25 construction sites: `tui_model.go:93, :95, :252, :748, :752`; `tui_vaxis.go:113, :160, :276, :700-766, :1421, :1467-68, :1481, :1550-51, :1563, :1874-75, :1904-05, :1942-43, :1955`) and every `strings.HasPrefix/TrimPrefix` parse (~15 sites: `tui_model.go:445, :552-556, :569-570, :586, :669-677`; `tui_vaxis.go:1143-48, :1158-63, :2038, :2175`) with `TargetKey`/`ParseTargetKey`. Where a string is still needed at the edge (map keys, display), call `.String()`. Good news from the survey: **nothing parses the `#n` session-key suffix anywhere** — you only migrate the prefix scheme. The `"%s#%d"` session-key format survives PR 1 untouched (it lives at `tui_vaxis.go:84` and, duplicated, in the fake at `tui_test.go:314` — unify the fake onto the real helper while you're there).

**PR 2 — TerminalID + registry.** `tuiSession.terminal tuiTerminal` becomes `tuiSession.terminalID TerminalID`; `putSession` allocates through the registry; every open/close/read/send/resize/draw flow resolves the runtime via the registry. `newSessionTUITerminal` keeps its role (the registry calls it) so the test-injection pattern keeps working. While here, stop `vaxisTUIApp` reading store internals: replace direct `a.sessions.sessionErrors[...]` / `a.sessions.sessions[...]` accesses with methods on the store (`SessionError(target)`, `SessionByKey(key)`) — the daemon boundary later needs exactly this discipline.

**PR 3 — Tab bookkeeping.** Move `tabCounters`/`sessionOrder`/`sessionErrors` out of `tuiSessionStore` into `TargetTerminalState` (note: `sessionErrors` is keyed by **target**, not session — preserve that). `tuiSessionManager` (the interface) stays as-is.

**Tests first / test contract.** Before PR 1, write characterization tests (Track 7.1 policy — identity is a core surface): pin today's session-key strings, tab ordering after open/close/reopen, active-tab fallback behavior (`targetActiveSessionKey` falls back to the first key when the recorded one is gone), and error-record lifecycle. Then the standing rule: **the existing fake-backed TUI tests must pass unmodified except for constructor plumbing. If a test's assertions must change, you broke semantics — stop and re-check.**

**Done when:** no UX change; all existing tests green; `tuiTerminal` values appear in exactly one place (the registry); an ADR records the identity model and the "never derive from layout" rule.

**Pitfalls.** (1) `TargetKey` as a map key: the struct is comparable — use it directly, don't `.String()` map keys (that reintroduces stringliness). (2) Don't "fix" the never-decremented tab counter — tab numbering behavior is user-visible via labels; characterize it, keep it. (3) Do not add mutexes to the registry — single-owner on the event loop is the Track 2 invariant that makes the actor refactor tractable.

---

# Part F — Track 2: Runtime actors, one launch contract, TUI decomposition — **L**

## 2.1 Generalize the PTY writer into a runtime actor

**Read first:** B.3 steps 3–5; `ghosttyPTYWriter` (`tui_terminal_ghostty_cgo.go:247-431`), `readLoop`/`ingestPTY` (`:1202-1244`), ADRs #30/#32.

The queued writer goroutine is one-third of an actor. Complete it: each `TerminalRuntime` gets a **single goroutine that owns all mutation** of its PTY/emulator, driven by a command channel:

```go
type runtimeCommand interface{}
type cmdInput struct{ Data []byte }
type cmdResize struct{ Cols, Rows int }
type cmdFocus struct{ Focused bool }
type cmdScroll struct{ Delta int }
type cmdRead struct{ Source ReadSource; Format ReadFormat; Reply chan ReadResult } // visible | recent scrollback
type cmdSnapshot struct{ Reply chan SnapshotResult }
type cmdClose struct{ Reason string }
// Handoff commands (BeginHandoff / ReleaseAfterHandoff / RollbackHandoff) are DECLARED now,
// implemented in Track 4. Declaring them now means Track 4 doesn't rewrite the loop.
```

Actor states: `Running → Quiescing → Quiesced → Released` (herdr's shape). In Track 2 only `Running` and closed matter.

Loop skeleton (this is a reorganization of existing code, not a rewrite — reuse the read loop and writer logic):

```text
for {
    drain all pending commands (non-blocking)      // input→enqueue, resize/focus→apply, read/snapshot→serve
    flush pending PTY writes                       // existing EAGAIN logic — and FIX the busy-spin:
                                                   // production must pass waitGhosttyPTYWritable, today Start
                                                   // passes waitWritable=nil (verified) so EAGAIN spins hot
    poll PTY readability (short timeout)
    read + ingest into the emulator
    publish a dirty notification (coalesced)
}
```

`cmdRead`/`cmdSnapshot` make terminal content **testable without a TUI** — that is the point; the daemon API (Track 3) and agent detection (Track 5) both consume them. `cmdRead` returns visible text or recent scrollback as plain text or ANSI; `cmdSnapshot` returns the full cell grid (rune, fg/bg, attrs, cursor) — the Ghostty bridge already answers these queries for `Draw`; route the same calls through the actor.

**Tests first.** `TestActorReadReturnsVisibleText`: spawn a real `/bin/sh` through a runtime actor (no TUI), send `echo hello-actor\n` via `cmdInput`, poll `cmdRead` until output contains `hello-actor`. `TestActorResizeIsOrderedBeforeInput`, `TestActorCloseIsIdempotent`, `TestActorServesSnapshotWhileStreaming` (a busy `yes`-style child; snapshot returns consistent generation while reads continue). Use the ghostty-unavailable skip pattern.

## 2.2 One shell-launch contract

**Read first:** `OpenProjectTab`/`OpenNodeTab` (`tui_vaxis.go:112-192`), `interactiveShellLaunchCommand()` (`service.go:1882-1916`), `Service.Shell` (`:1315-1341`), TODO #18, `plans/TMUX_PLAN.md`'s invariant.

Kill the second launch path. Add to the service layer:

```go
func (s *Service) TerminalLaunchSpec(target terminal.TargetKey, kind terminal.TerminalKind) (LaunchSpec, error)
// LaunchSpec: Argv []string, Dir string, Env []string.
// NodeShell:        {codelimaExe, "--home", cfg.MetadataRoot, "shell", nodeID}   (what OpenNodeTab builds today)
// ProjectHostShell: interactiveShellLaunchCommand() rooted at project.WorkspacePath (what OpenProjectTab builds today)
```

The TUI (and later the daemon, tmux sidebar, anything else) asks the Service for the spec and hands it to the registry to spawn. `OpenProjectTab`/`OpenNodeTab` stop building commands themselves; the workspace-path validation currently inside `OpenProjectTab` (exists/is-dir, recorded into session errors) moves into `TerminalLaunchSpec`'s error return. This is the invariant both `plans/TMUX_PLAN.md` and `plans/AGENT_MONITORING_PLAN.md` already demand: *every managed terminal enters the VM through CodeLima* — never a raw `limactl shell` / bare bash.

While here, fix **TODO #18**: `interactiveShellLaunchCommand`'s temp `INPUTRC` under `$HOME` fails on read-only homes (`mktemp: Read-only file system`). Probe writability; fall back to the project-rooted `tmp/` dir; if nowhere is writable, skip the INPUTRC customization entirely rather than failing the shell. Regression test with `$HOME` pointed at a read-only dir.

**Tests first.** Table test on `TerminalLaunchSpec` covering both kinds + error cases (missing workspace, deleted node). TUI-side: assert `Open*Tab` now spawns exactly `spec.Argv` (fake terminal records `startCmd` — the assertion hooks exist).

## 2.3 Decompose `tui_vaxis.go` (2,883 lines, ~130 methods on `vaxisTUIApp`)

Split by responsibility. **Mechanical moves only, one PR per extraction, zero logic changes**, `make verify` after each:

- `tui_sessions.go` — registry adapter + tab bookkeeping (much moves to the `terminal` package)
- `tui_input.go` — key/mouse routing, shortcut matching
- `tui_operations.go` — background task orchestration (`operations map[string]*tuiOperationState`, `operationOrder`)
- `tui_dialogs.go` — the ~11 `openXxxDialog` builders + selectors/menus
- `tui_render.go` — `draw*` and layout (`treeContentRect`, `terminalBodyRect`, link regions)
- `tui_app.go` — the shrunken event loop

The dialog/action/operation layer is the "shared controller" the tmux-sidebar plan needs; extracting it now is what makes that plan (and any future frontend) cheap.

**Definition of done for Track 2:** multiple terminals run concurrently with no direct UI-goroutine ownership; close reliably kills process groups (0.1's helper, now actor-owned); `cmdRead`/`cmdSnapshot` unit tests pass with a real `sh` and no TUI; the EAGAIN busy-spin is fixed (writer waits on POLLOUT); no file in the package exceeds ~1,200 lines. ADR for the actor model; PATTERNS.MD entry.

---

# Part G — the backend swap happens here

The Lima → microsandbox swap is complete in the current environment. E1–E10 passed, ADR 55 is accepted, metadata schema 2 rejects old Lima homes, and all runtime commands remain behind `SandboxClient`. Native release qualification remains tracked separately.

---

# Part H — Track 3: The daemon — **XL, split as below**

Now — and only now — move the registry out of process. Prerequisite reading: B.5 glossary (sockets, JSON-lines, SCM_RIGHTS), §3.0 below.

## 3.0 Prerequisite: embed a version — **S**

Verified: **no version is embedded in the binary today** — no `-ldflags -X`, no `--version` subcommand; version exists only at packaging time (`PACKAGE_VERSION` → archive name + manifest JSON + Homebrew formula). The daemon's exact-match handshake (3.2), `daemon.identity`, and Track 7.4 self-update all need one. Add `var Version = "0.0.0-dev"` in the codelima package, set via `-ldflags "-X github.com/brianrackle/test_lima/internal/codelima.Version=<v>"` (note the module path!) in `make build` and `scripts/package_release.sh`; add `codelima --version`. Test: build with the flag, assert output.

## 3.1 Process model and socket hygiene — **M**

- `codelima daemon run` (foreground; what launchd/systemd or a user shell runs), `codelima daemon start` (spawn detached + wait for ping, ~5s timeout), `stop`, `status`. New `daemon` command group in `dispatch` (`cli.go`).
- The TUI auto-starts the daemon if absent (config flag `daemon.autostart: true` default). The CLI never silently auto-starts for read commands; it reports "daemon not running (codelima daemon start)".
- Files under `CODELIMA_HOME/_daemon/`: `daemon.sock` (request/response API), `client.sock` (attach/event stream), `daemon.pid`, `daemon.identity` (random token + binary version, written via `atomicWriteFile`). All sockets `0600` (`syscall.Umask` dance or chmod-after-bind — test it).
- Startup mutual exclusion: take `_locks/daemon.lock` (the existing flock pattern — non-blocking variant; add `acquireLocksNonBlocking` alongside `acquireLocks`); then if `daemon.sock` answers `daemon.ping`, fail "already running"; if it doesn't answer, remove the stale socket and bind. Never two daemons on one home.

**Tests first (this is where the integration tier from 7.2 starts existing):** `tests/daemon_lifecycle_test.go` — start (real binary, throwaway home), status, double-start fails, stop; kill -9 then start again succeeds (stale socket + lock recovery).

## 3.2 Protocol — **M**

- **JSON-lines over Unix sockets** for both surfaces: `{"id": n, "method": "...", "params": {...}}` → `{"id": n, "result": ...}` | `{"id": n, "error": {"category": ..., "message": ...}}`; events are `{"event": "...", "data": ...}`. One `encoding/json` object per line; `bufio.Scanner` with a raised buffer cap. Do not invent a binary protocol until terminal frame streaming measurably needs it.
- **Version policy (herdr's most transferable lesson): exact-match, no backward compatibility.** Client `hello` carries `Version` (3.0); mismatch is an error whose remedy is "restart the daemon" (later: live-update). Client and daemon ship in one binary, so compat shims buy nothing. Cap request size (1 MB); per-connection read deadlines; errors map to the `AppError` taxonomy (B.2) so CLI exit codes keep working through the daemon.
- The daemon hosts the same `Service` the CLI uses. Metadata reads/writes keep going through `Store` + flocks, so direct CLI commands and the daemon coexist safely. Terminal state, however, lives **only** in the daemon.

Package layout: `internal/codelima/daemon/` (server, protocol types, conn handling) + `internal/codelima/daemonclient/` (dialer used by CLI and TUI). Protocol types get table-driven encode/decode tests including oversized-request rejection.

## 3.3 Minimum API surface — **L**

```text
daemon.ping / daemon.status / daemon.stop / daemon.snapshot
project.tree / project.list / node.list / node.start / node.stop
terminal.list                      → []TerminalState + tab structure per target
terminal.open   {target, kind, label?}
terminal.close  {terminal_id}
terminal.focus  {terminal_id}
terminal.resize {terminal_id, cols, rows}
terminal.read   {terminal_id, source: visible|recent, format: text|ansi}
terminal.send_text / send_keys / send_input {terminal_id, ...}
terminal.scroll {terminal_id, delta}
terminal.snapshot {terminal_id}
events.subscribe {topics: [...]}   → stream on the client socket
```

Handlers are thin: `terminal.*` translate to actor commands (Track 2's `cmdRead`/`cmdSnapshot`/`cmdInput` — they were built for this); `project.*`/`node.*` call the hosted `Service`. `terminal.read` + `terminal.send_input` are the automation surface — they exist from day one and get CLI verbs (`codelima terminal list/read/send`). This is how an agent in one VM can babysit an agent in another.

## 3.4 Rendering: client-side first, deliberately

Two options existed; the decision is made — **do not relitigate it mid-implementation**:

- **Chosen: daemon-owned runtimes, client-rendered UI.** The daemon owns PTYs + Ghostty emulator state. The TUI renders tree/chrome exactly as today and, for the visible terminal, subscribes to `terminal.dirty` events and pulls cell-grid snapshots (`terminal.snapshot` returns cells: rune, fg/bg as packed u32, attr bitmask, cursor, hyperlink ids — plus a `generation` counter so stale pulls are discarded). Coalesce: one in-flight snapshot per terminal; on dirty-while-pulling, pull again.
- **Deferred: server-side full-frame streaming** (herdr streams the entire TUI as pre-diffed ANSI with synchronized-update wrapping). Revisit only with latency measurements in hand, or when we want `codelima attach <terminal>` as a raw thin client. The dirty→snapshot API is forward-compatible with adding a diff push later.

Do not delay the daemon for rendering elegance. A working detach beats a beautiful protocol.

## 3.5 Reconnect and input ownership — **M**

On client connect: handshake (size, capabilities, version) → daemon returns project tree, per-target tab state, active target, terminal list → client subscribes → client resizes visible terminals → renders.

Events: `terminal.created/closed/dirty/resized/title_changed/clipboard`, `target.tabs_changed`, `project.tree_changed`, `node.status_changed`, `agent.status_changed` (Track 5), `daemon.update_starting/failed/committed`, `daemon.shutdown`.

One **input owner** at a time; extra clients attach observe-only; explicit takeover revokes the previous owner (daemon notifies it). Don't build fancy sharing — single-user tool. Clipboard (OSC 52) and desktop notifications are client-side effects: the daemon forwards them as events; whichever client has focus executes them.

## 3.6 Session persistence — **S**

`CODELIMA_HOME/_daemon/session.json`, **versioned from day one** (`{"version": 1, ...}`), written via `atomicWriteFile` on change and on shutdown: targets → tabs → `{tab_id, label, terminal_id}`, plus per-terminal `TerminalState` (target, kind, cwd, launch argv, created_at). On daemon restart, offer tab restoration by **respawning** shells (config: `daemon.restore: respawn|forget`). Do not attempt PTY survival across normal daemon restarts — that is exclusively Track 4's job. No PTY resurrection after host reboot, ever.

**Definition of done for Track 3:** quit the TUI, terminals keep running (verify: a loop emitting output in a node shell keeps appending; `codelima terminal read` shows it); reopen the TUI, tabs reattach with scrollback intact; `codelima terminal list/read/send` works from a second shell; two TUIs attach with one input owner; kill -9 the TUI and reconnect cleanly; integration tests cover detach/reattach, multi-client, and daemon crash-recovery. ADRs: daemon architecture; protocol + version policy; session persistence schema.

---

# Part I — Track 4: Live update (last, Unix only) — **L**

Replacing the daemon binary without losing live PTYs. Hard prerequisite: Tracks 1–3 stable and soaked in daily use, actors implementing quiesce. Target UX: `codelima daemon update [path-to-new-binary]` — used by brew upgrades and self-update (7.4).

Sequence (adapted from herdr's field-proven design — reimplement from this description):

1. Old daemon stops accepting mutating requests; notifies clients `daemon.update_starting`.
2. Every runtime actor quiesces: drain pending PTY writes (bounded, ~2s timeout), gate further input, pause reads. State `Quiesced`.
3. Old daemon serializes a handoff manifest: session state (3.6 shape) plus per-terminal runtime info `{terminal_id, child_pid, rows, cols, input-mode state, bounded replay buffer of recent output (~8KB/terminal)}`.
4. Old daemon binds a **private** handoff socket (`_daemon/handoff-<pid>.sock`, 0600), spawns the new binary in import mode with a shared token, sends the manifest (JSON + newline), then passes duplicated PTY master FDs via `SCM_RIGHTS` (`golang.org/x/sys/unix`: `UnixRights` + `(*net.UnixConn).WriteMsgUnix`; cap FDs per message, batch if needed).
5. Explicit text handshake with timeouts at every step: import validates manifest → receives FDs → rebuilds actors → binds public sockets → answers ping → signals ready; old daemon sends commit; import acknowledges ownership. Generous ready/commit timeouts (30s), tiny ack timeout.
6. On success: old actors → `Released` — their teardown must **NOT** signal the children (this is why preserve-on-close is an explicit actor flag, not an accident of code paths); old daemon exits; clients reconnect; new daemon nudges each child with a SIGWINCH-style resize to force a repaint.
7. On any failure or timeout: rollback — kill/reap the import child, resume actors to `Running`, resume request handling, notify `daemon.update_failed`. **The old daemon must remain fully alive until commit. No terminal may die from a failed update.**

Fallback where FD passing fails: graceful restart with respawn-restore (3.6) and a clear message.

**Definition of done:** integration test: daemon + terminal running a counter loop → hand off to a re-exec of the same binary → counter never reset, no output bytes lost. Second test injects an import failure → rollback leaves the terminal running. ADR for the handoff protocol.

---

# Part J — Track 5: Agent awareness (the product differentiator) — **M–L**

CodeLima's pitch is *sandboxed agentic coding*. Herdr's is *see which agents are blocked at a glance*. CodeLima needs both halves. This track merges `plans/AGENT_MONITORING_PLAN.md` with what herdr proved works. Depends on Track 3 (daemon owns screen state); the detection engine itself can be built earlier against Track 2's `cmdRead`.

## 5.1 Screen-snapshot detection engine

A detection engine that reads a **screen snapshot** (never parser/viewport internals) and evaluates declarative rules from per-agent manifest files.

- New package `internal/codelima/detect/`. Manifests: TOML under `detect/manifests/` (`claude.toml`, `codex.toml` first — the two agents we seed), embedded via `go:embed`, overridable at `CODELIMA_HOME/_config/agent-detection/<agent>.toml`, hot-reloadable (mtime check per evaluation cycle). TOML parsing needs a dependency (e.g. BurntSushi/toml) — the module is dependency-light on purpose; adding one is fine but say so in the PR.
- Rule shape: match against extracted **regions** of the snapshot — OSC title, last N non-empty lines, prompt-box body — with `contains`/`regex`/`any`/`all`/`not` combinators, a `priority`, a resulting `state`, and a "skip transient screens" escape. Example shape:

```toml
[[rule]]
state    = "blocked"
priority = 90
region   = "last_lines:6"
all      = [ { contains = "Do you want to" }, { regex = "❯?\\s*(1\\.|Yes)" } ]
```

- Calibration facts (verified against herdr's shipped manifests — re-derive our own by running the agents and snapshotting): Claude "working" = Braille spinner glyphs in the OSC title; "blocked" = permission-prompt text + yes/no menu shape; "idle" = `❯` prompt on the last line.
- States: `idle | working | blocked | done | error | unknown`. Badge priority: `error > blocked > working > interrupted > done > idle` (matches AGENT_MONITORING_PLAN).
- **Debuggability is a feature**: `codelima agent explain <terminal>` prints which rule fired on which region, with the region text. Build it with the engine, not after.

**Tests first:** golden snapshot fixtures (text grids captured from real agent sessions, committed under `detect/testdata/`) → table tests asserting state per fixture. The engine is a pure function: `Detect(snapshot, manifest) (state, firedRule)`.

## 5.2 Surfacing

- Tree rows: `api-copy  RUNNING  claude:blocked` with color; **rollup** — a blocked node bubbles a marker to its project row (AGENT_MONITORING_PLAN's "descendant notification marker").
- Events: `agent.status_changed` on the daemon stream; unseen-state tracking host-side (clears when you focus the terminal).
- Notifications: config-gated sound/toast/system notification on `blocked` and `done` transitions, delivered by the focused client. This is the "stop babysitting terminals" payoff.
- CLI/JSON: agent status in `node list --json` / `project tree --json`, read-only, **never persisted into `node.yaml`** (lifecycle-only metadata, ADR #38).

## 5.3 Orchestration primitives

```text
terminal.wait_for_output {terminal_id, match|regex, timeout_ms}
agent.wait  {terminal_id, status: done|blocked, timeout_ms}
```

Then ship a `SKILL.md`-style document (herdr's cleverest product idea): a short instruction file teaching coding agents *running inside CodeLima nodes* to use `codelima terminal open/read/send/wait` to run dev servers in sibling terminals, wait for "ready", and check on other agents. Set `CODELIMA_ENV=1` in managed shells (via `TerminalLaunchSpec` env) so agents can discover they're inside CodeLima.

## 5.4 Later: guest-side monitor

`AGENT_MONITORING_PLAN.md`'s guest-monitor design (transcript files + process liveness → `agent-status.json` in a host-visible per-node `runtime/` dir) remains correct for agents running *without* an open managed terminal, and is more authoritative than screen heuristics. Herdr's equivalent lesson: agent-native lifecycle hooks override screen detection when present. Sequence after 5.1–5.3; screen detection covers the 90% case immediately.

---

# Part K — Track 6: Experience (interleave as relief work; ordered by value-for-effort)

1. **Configurable keybindings + optional prefix mode — M.** Execute `plans/KEY_BINDINGS_PLAN.md` — it is already detailed: `key_bindings.tui` map in config.yaml (`actionID → []binding`), ~27 stable action IDs (`app.quit`, `focus.toggle_terminal`, `tree.*`, `node.*`, `dialog.*`, …), canonical binding syntax, scope-based collision validation (so `s` can be both `node.start` and `node.stop` in disjoint scopes), defaults materialized into config.yaml the way `lima_commands` are, 4-phase migration off the hardcoded `Hotkey rune` matching. Then add an optional tmux-style **prefix key** mode (off by default): the macOS Option story is objectively bad — the README teaches `†`/`∑` glyph fallbacks; a `Ctrl+b`-style prefix sidesteps the Option/Meta swamp entirely.
2. **Roadmap 0.15 + 0.4 — S+M.** 0.15: "terminal should be the default node view (not info) if the vm is running" — note this **supersedes ADR #40** (info-first split panes); implement as running-nodes-only default and record a superseding ADR. 0.4 (verbatim from ROADMAP): *"resizing window often causes terminal contents to clear. option + ` with codex or raw terminal causes whole terminal to clear so the previous content is no longer visible and only a fresh prompt is shown."* It likely dissolves in Track 2/3 when resize becomes an actor command applied before redraw — but verify explicitly against that repro; TODO #12 (the `Ctrl-L` width-growth shim) is adjacent context.
3. **A "goto" picker — S–M.** One key → fuzzy list of projects / nodes / open terminals (with agent status once Track 5 lands) → jump. Reuse the existing `tuiSelector` building blocks from the dialog layer.
4. **Theme fidelity — S–M.** TODO #1: feed host terminal fg/bg (Vaxis can query them) into the Ghostty defaults at startup and on theme-change events. We don't need themes; we need *not looking wrong* in the user's terminal.
5. **Kitty graphics passthrough — M** (roadmap 0.10: "support kitty graphics protocol"). Herdr forwards Kitty graphics end-to-end, proving libghostty-vt handles it; budget a large max-frame cap on the attach socket when this lands post-daemon.
6. **Split panes — last — L** (TODO #21). Only after the daemon has soaked: `TerminalLayout` as a recursive split tree `{Pane(TerminalID) | Split{direction, ratio, first, second}}` inside `TerminalTabState`; every split allocates a fresh `TerminalID` through the registry. The Track 1 data model was shaped so this is additive. Persist layouts in `session.json` (already versioned — bump the version, write a migration).
7. **First-run polish — S.** `codelima doctor` verifies the full happy path (runtime binary present + version, home writable, libghostty loadable — each with an actionable fix line); `codelima config init` emits a commented default config; the zero-config path keeps working.

---

# Part L — Track 7: Engineering system (runs continuously; 7.1–7.2 start with Track 1)

1. **Characterization-test policy.** Adopt as written policy (add to AGENTS.md): any change touching two or more core surfaces, persisted state schemas, protocol/API shapes, or identity allocation requires characterization tests written *before* the refactor. Tracks 1–3 and the backend swap are exactly such changes.
2. **Integration test tier — M.** New top-level `tests/` module/dir, build tag `//go:build integration`, `make test-integration` target; real built binaries against throwaway homes; **no VM needed** — node shells faked with plain commands. Suites, in the order the tracks need them: PTY lifecycle/orphan reaping (0.1), detach/reattach (3), multi-client input ownership (3.5), daemon crash-recovery (kill -9; stale-socket + session.json restore), live handoff (4). Plus: make CI fail instead of skip when Ghostty is missing — a `CODELIMA_REQUIRE_GHOSTTY=1` env check at the existing 26 `t.Skipf` sites (one shared helper: `skipOrFailGhosttyUnavailable(t, err)`), set in CI after `make init`.
3. **TUI visual verification harness — M** (TODO #2). Drive the real binary under a PTY at fixed geometry, capture the final cell grid **with colors** — the Track 3 `terminal.snapshot` API makes this nearly free (a test client attaches and snapshots) — and golden-file the results. Kills most of the standing manual-QA burden (TODO #0/#9/#19).
4. **Self-update — M.** `codelima self-update`: fetch the version-manifest JSON from GitHub releases (`make package` already emits `{version, goos, goarch, asset_name, sha256}` manifests — verified), verify sha256, download, atomic-rename install, then — post-Track 4 — trigger daemon live-update. Requires 3.0 (embedded version). Do TODO #4 (signing/notarization) before promoting this widely.
5. **Vendor patch discipline — S.** We carry `scripts/patches/ghostty-vt-codelima.patch` (touches the libghostty-vt C API surface: `lib_vt.zig`, terminal/stream C bindings). Add `scripts/patches/PATCHES.md` recording per patch: rationale, upstream PR link (file them!), and the concrete deletion condition. Prevents patch rot across Ghostty rebases (the pinned commit is in the Makefile: `GHOSTTY_VT_GHOSTTY_COMMIT`).
6. **Docs versioning — S.** When user docs outgrow the README, adopt `docs/next/` staged-with-PR, promoted on release.

---

# Part M — Sequencing, non-goals, risks, end state

```text
Track 0 (stabilize) ──► Track 1 (identity/registry) ──► Track 2 (actors + launch contract + decomposition)
                                                            │
                                        ┌───────────────────┤
                                        ▼                   ▼
                              backend decision       Track 6.1/6.2
                              (local E1 passed;       (keys, defaults —
                              Phase 0 continues)      anytime)
                                        │
                                        ▼
                                 Track 3 (daemon)
                                        │
                          ┌─────────────┼───────────────┐
                          ▼             ▼               ▼
                   Track 4 (live    Track 5 (agent   Track 6.3–6.7
                   update, last)    awareness)       (picker/theme/kitty/splits/polish)

Track 7 runs continuously; 7.1–7.2 start with Track 1.
```

| Order | Work | Depends on | Size |
|---|---|---|---|
| 1 | Track 0.1–0.5 (lifecycle, signals, read/write split, delete bug, logging) | — | S–M each |
| 2 | Track 0.6–0.8 + backlog | — | S each |
| 3 | Track 1 (identity + registry; 3 PRs) | 0 | M |
| 4 | Track 2 (actors, launch contract, decomposition) | 1 | L |
| 4.5 | Resolve reopened backend decision (microsandbox local E1 passed; remaining Phase 0 + release qualification pending; ADR 55) | 2 | L |
| 5 | Track 3 (daemon; 3.0–3.6) | 2 + 4.5 | XL |
| 6 | Track 5.1–5.3 (detection, badges, wait primitives) | 3 (engine prototypable on 2) | M–L |
| 7 | Track 4 (live update) | 3 stable + soaked | L |
| 8 | Track 5.4, Track 6.5–6.6 (guest monitor, kitty, splits) | 3–5 | M–L |
| — | Track 6.1–6.4, 6.7, Track 7 | interleave | S–M each |

Do not start the daemon until the Track 2 definition-of-done is met **and** the reopened backend decision is resolved by a candidate that passes its gate. Do not start live-update until detach/reattach has soaked in daily use. Live-update is the reward for a clean runtime model, not a shortcut to one.

## Non-goals for the daemon milestone

- No web frontend, no remote TCP daemon, no multi-user daemon.
- No guest-side agent control channel (we clean up host-side wrapper process groups; we do not pretend to manage arbitrary guest processes).
- No PTY resurrection after machine reboot; `session.json` respawn-restore is the ceiling.
- No split panes until the daemon and registry have soaked.
- No live-update on platforms where FD passing is unavailable — graceful restart fallback only.
- No provider abstraction is approved. ADR 55's direct microsandbox replacement remains reopened pending the remaining Phase 0 and release results; Lima remains the implemented backend in the meantime. Docker/Firecracker/apple-container stay cancelled unless a new ADR explicitly revisits them.

## Risks

| Risk | Mitigation |
|---|---|
| Daemon refactor breaks the working TUI | Tracks 1–2 are in-process with a no-UX-change contract enforced by the existing TUI suite (~85 tests in `tui_test.go` alone); the daemon lands behind the same `tuiSessionManager` seam |
| Process cleanup kills too much / too little | Group-kill only the host-side wrapper's group; escalation with grace periods; the orphan-reaping test (0.1) is non-negotiable |
| Handoff loses PTY output | Quiesce/drain before FD duplication; bounded replay buffers; commit only after the import daemon answers ping; rollback keeps old daemon alive until commit |
| Client/daemon rendering scope creep | Rendering decision is fixed (client-side, snapshot-pull); revisit only with latency measurements in hand |
| Detection heuristics rot as agent CLIs change UI | Manifests are data, hot-reloadable, user-overridable, debuggable via `agent explain`; later lifecycle hooks override heuristics |
| herdr license contamination | AGPL — nobody reads herdr source while implementing; this document is the only reference. Reimplement from the descriptions here |
| Two-daemon / stale-socket confusion | flock + ping-before-bind + identity file + exact-version handshake |
| Junior misreads a stale line anchor | Anchor on names; when code and document disagree, trust code and update the document in the same PR |

## End state

- Run `codelima`: same TUI, same tabs, same keys.
- Quit it: your Codex and Claude sessions keep running in their microVMs.
- Reopen it: everything is where you left it, scrollback included.
- Glance at the tree: you can see which agent is blocked on a permission prompt without touching a tab.
- Script it: `codelima terminal read/send/wait` from any shell — including from an agent inside another node.
- Upgrade it: `brew upgrade` + `codelima daemon update`, and your terminals never notice.
- Split it: when you finally need two shells side-by-side on one node, the identity model has been ready for it since Track 1.
