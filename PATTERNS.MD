# Patterns

## Filesystem Metadata Store

The CLI treats `CODELIMA_HOME` as the authoritative store for projects, nodes, snapshots, and indexes.
Every write goes through temp-file-plus-rename semantics so metadata stays recoverable after partial failures.

## Incomplete Metadata Recovery

When store enumeration finds a node directory without `node.yaml`, it treats that directory as incomplete metadata instead of a real node.
Normal node and tree reads skip those directories so older failed writes do not brick the CLI or TUI, while `doctor` and `node cleanup-incomplete` surface the problem explicitly for operators.

## External Command Boundary

Lima interactions are isolated behind `LimaClient`.
Tests use a fake implementation to assert delegation behavior without creating real VMs, while the production path uses `limactl`.

## Node Slug Lima Identity

New Lima-backed nodes use the normalized node slug as the Lima instance name and provision the guest hostname to the same value during first boot.
That keeps `limactl` names, node metadata, and guest shell prompts aligned with the operator-facing CodeLima node name without relying on unsupported Lima YAML fields. Existing node metadata remains authoritative for already-created nodes, so identity changes should not rewrite stored `lima_instance_name` values unless an explicit migration is implemented.

## Native Library Compatibility Bridge

When CodeLima runtime-loads a native library whose upstream C API changes faster than the Go call sites should, keep the adaptation in dedicated compatibility files instead of inline cgo blocks. The compatibility layer owns `dlopen` symbol resolution, capability probing, and any mapping from newer upstream types or callbacks to the narrower app-facing surface, so later rebases stay localized.

## Command-Template-First Lima Overrides

The authoritative default Lima command templates live in `CODELIMA_HOME/_config/config.yaml` under `lima_commands`, including `bootstrap` for first-start guest bootstrap work and `workspace_seed_prepare` for the copy-mode guest directory preparation step.
Each `lima_commands` entry is an ordered command list, so one action can execute multiple commands in sequence. Projects and nodes can each override any subset of those command lists under the same key, with precedence `config.yaml`, then `project.yaml`, then `node.yaml`. Project and node metadata files without explicit overrides include commented example blocks so operators can discover the shape in place. Runtime expansion happens with placeholders like `{{instance_name}}`, `{{template_path}}`, `{{command_args}}`, `{{target_path}}`, and `{{target_parent}}`, while advanced per-node create-time overrides are seeded through `node create --lima-commands-file`, `node clone --lima-commands-file`, or the matching TUI dialog field before `node.yaml` already exists on disk.

## Snapshot Then Promote

Fork flows never mutate a target workspace before an immutable snapshot exists.

## Bootstrap State Freeze

Node creation resolves an `AgentProfile` once and stores the concrete bootstrap inputs in `bootstrap.json`.
That resolution includes the effective `lima_commands.bootstrap` list plus any referenced reusable environment config `bootstrap_commands`.
Later config changes do not retroactively mutate existing nodes.

## Built-In Metadata Seeding

Mutating operations and `codelima doctor --repair` seed durable built-in metadata such as agent profiles and reusable environment configs through `seedAndRepair`, always while holding the environment-configs, projects, and nodes locks. Read surfaces never seed or repair: they only create missing directories, so a fresh home shows built-ins after its first mutating command (ADR 57). TUI startup is the one deliberate exception: launching the TUI runs the same locked seed pass once per process — a user-initiated session start, not a background read — so fresh-home users see built-ins in the TUI's pickers, while the TUI's auto-refresh tick remains a pure read.
The defaults are written into the normal store, so the CLI and TUI see the same `codex` and `claude-code` environment configs plus the matching `codex-cli` and `claude-code` agent profiles without special UI logic. When a built-in environment config changes, seeding may refresh an untouched legacy config forward to the new default, but existing user edits or deletions still win, so the seeded records keep behaving like ordinary user-managed metadata instead of immortal system entries.

## Workspace-Aware Shell Delegation

Shell entry always normalizes the documented `--` separator before delegating to Lima.
Interactive shells and one-off commands pass the node workspace through `limactl shell --workdir` so shell entry is stable regardless of the host directory where `codelima` is launched.

## TUI Session Reuse

The TUI keeps one PTY-backed terminal backend per opened terminal tab, and each project or node target owns an ordered list of tabs keyed `<target>#<n>`. Project tabs launch a host-local interactive shell rooted at the project's workspace path, while node tabs keep using `codelima shell <node>`. The preferred backend is Ghostty VT, with the older Vaxis terminal widget retained as a fallback when the native Ghostty library is unavailable. The session store should track the current embedded-pane size and apply that size to a terminal backend before starting its PTY-backed shell, so login prompts and other early output are rendered at the right dimensions instead of being lost on the first resize. Resize events should update the active terminal backend before redraw so the screen is repainted at the new geometry instead of clearing around stale dimensions. When Ghostty exposes a terminal input, focus-report, viewport, explicit/default-color, terminal-query, or clipboard callback API, CodeLima should prefer that Ghostty-owned encoder, focus encoder, viewport state, color semantic, query callback, or clipboard callback through a narrow compatibility wrapper and retain the local Go implementation only as a fallback for unavailable APIs or unsupported events. The Ghostty PTY transport should keep the shared PTY handle in blocking mode so the read loop continues to behave like a normal PTY reader, while a dedicated queued writer goroutine owns foreground-originated writes so UI events do not write directly on the render path. Paste input should normalize pasted newlines to carriage returns before dispatch so multiline paste behaves like pressing Enter inside the embedded terminal. OSC 52 clipboard writes from guest output should sync to the host clipboard without taking ownership of mouse selection. Mouse ownership should follow the guest terminal state: when the embedded terminal reports mouse capture, CodeLima forwards mouse input to the guest; otherwise CodeLima only uses terminal clicks for focus and hyperlink activation, leaves wheel-based local scrollback intact, and relies on the host terminal emulator's bypass gesture for text selection and copy. Tree focus owns a sticky right-pane mode: `info` by default for both projects and nodes, `terminal` when toggled with `i`. Changing the selected target only swaps which emulator or terminal placeholder is visible, so scrollback, alternate screens, cursor state, hyperlinks, and unsubmitted input stay attached to that project or node until the TUI exits. TUI startup opens one initial tab for the starting project or running node after terminal size has been recorded, while preserving tree focus and info mode. A TUI launched with a workspace-root path must store that root and use the same filtered `ProjectTreeByWorkspaceRoot` surface for startup, explicit reloads, and automatic refreshes. After the startup tab, selecting or visiting tree entries, toggling the `i` pane mode, and automatic refreshes must never create or activate terminal sessions. Creating a node from the TUI may select the new node and switch the split pane to terminal mode so the terminal placeholder is immediately available, but it still must not start a shell session for a non-running node. `Option+\`` or `F6` continues to own only the tree-versus-fullscreen-terminal focus transition; fullscreen focus reuses the focused target's active tab and opens that target's first tab only as part of this explicit command. Terminal tabs are otherwise explicit and per-target: `Option+t` opens a fresh tab for the focused project or running node without leaving tree focus (repeat presses add more tabs for the same target), `Option+Left` and `Option+Right` switch among the focused target's tabs only, and `Option+w` closes the focused target's active tab. Closing a tab focuses the next higher-numbered adjacent tab when available, otherwise the previous lower-numbered tab. The terminal pane border shows only the focused target's tabs, numbered when more than one is open, with project tabs labeled as host tabs and the active tab bracketed; each target remembers its active tab across focus changes. Shortcut matching accepts terminal `Alt` and `Meta` modifier forms plus the common macOS Option-generated sequences (`†`, `∑`, `Alt+b`, and `Alt+f`); there are no function-key or tree-focus `t` tab fallbacks. Modified terminal keys that are not TUI tab or focus commands should not create TUI splits; when terminal focus is active they are forwarded to the embedded terminal, and when tree focus is active they should not match plain action hotkeys. The host-terminal switch is separate from tree focus: from a selected node, `Option+Shift+Backtick` may temporarily point fullscreen terminal focus at the owning project's host-local terminal while preserving the node selection and a return target back to the node session across automatic refreshes, and that host override should turn the existing TUI top bar red until the node terminal is restored.

## Contextual TUI Actions

The TUI treats the tree selection as the context boundary for mutations.
Project and node hotkeys open right-pane dialogs, menus, and selectors that call the existing `Service` methods, then reload the tree while preserving expansion state and the most relevant selection. Those transient views replace the right pane instead of opening centered modals, so the tree stays visible and the wider pane can hold form fields, descriptions, and help text without obscuring the rest of the TUI. In tree focus, the footer should mirror the currently active pane controls instead of showing generic placeholder copy, so the visible hints always match the keys the operator can press at that moment. Project-scoped bootstrap commands and reusable environment configs use the same pattern via dedicated menus so list editing does not depend on multiline text entry. Command removal uses selectors plus explicit confirmation instead of typed indexes, and command reordering is expressed as single-step move actions so order-sensitive bootstrap lists stay editable without freeform text parsing. When a dialog needs a reusable config reference, it opens a selector in the same right pane instead of asking the operator to type slugs manually.

The TUI should also periodically refresh the same project tree surface it used at startup through its event loop, preserving expansion state, selection, path scope, and open terminal sessions. That keeps runtime status and out-of-process metadata changes aligned with the same read surface used by `project tree` and node list-style queries without introducing a second runtime-status source.

## Async Lima Progress In TUI

Lima-backed TUI mutations should run as background tasks instead of taking over the right pane.
Dialogs, menus, and selectors still use the right pane directly, but long-running project and node work should leave the current details or terminal surface usable while the tree and details pane render transient task state such as `starting`, `stopping`, or `deleting`. Each task should route `limactl` plus non-interactive guest-command output through its own service-scoped writer clone so streamed lines stay attached to the task that produced them, and the TUI should reject only resource-conflicting actions instead of freezing unrelated navigation or terminal focus changes.

## Bundled Runtime Wrapper

Release archives package the compiled `codelima` binary as `bin/codelima-real`, a small launcher script as `bin/codelima`, and the matching `libghostty-vt` shared library under `lib/`.
The wrapper owns `CODELIMA_GHOSTTY_VT_LIB`, so local packaging, manual tarball installs, and the generated Homebrew formula all reuse the same runtime layout instead of each inventing a different library-discovery scheme.

## Platform-Scoped Development Binary

Repository-local builds should write native executables under `bin/<os>-<arch>/` using the same platform tag as `.tooling/<os>-<arch>`, and may refresh a short compatibility symlink such as `bin/codelima` for manual commands. Make targets and long-lived in-process re-exec paths should use or cache the platform-scoped binary rather than relying on the compatibility symlink, because a shared host/guest checkout can repoint that symlink when the other platform rebuilds.

## Repository-Scoped Toolchain

Development tools required by agents or CI should be pinned in `Makefile`, installed by `make init` into `.tooling/<os>-<arch>`, and exposed through make recipes when they need direct invocation. Installer scripts should use the repository `tmp/` directory for temporary work and the repository-scoped Go caches so host-global toolchains and caches are not required.

## Guest Workspace Seed

New nodes default to `copy` workspace mode.
In that mode, the first successful start resolves `lima_commands.workspace_seed_prepare` to clear and recreate the guest target path, then runs `lima_commands.copy` to copy the host workspace into the guest at the same absolute path. That keeps guest edits isolated while preserving the expected path layout for project bootstrap commands and shell entry.

## Workspace Mode Per Node

Node creation chooses the workspace binding strategy per node instead of per project.
`copy` keeps the existing isolated guest copy behavior, while `mounted` renders a writable Lima mount at the same absolute path on host and guest and skips the guest copy step. Node metadata and clone behavior preserve that mode explicitly so later shell entry, details rendering, and cloned nodes do not need to infer it from current runtime state.

## Clone Is VM Copy

Node clone uses `limactl clone` as a full-VM copy.
If the source node is running, CodeLima stops it, clones it, and starts it again. The cloned node stays in the same project, keeps the same workspace mode and workspace path binding, and keeps the source VM's bootstrap state.

## Workspace Rebind Guard

Project workspace paths are treated as durable host bindings.
Rebinding a project to a new workspace path is allowed only after all nodes for that project are terminated, which keeps seed inputs, node metadata, and project metadata from drifting out of sync.

## Default List Tables

Human-facing list commands should render concise tables by default instead of dumping full YAML records.
Keep the column contract stable for quick scanning. When a list represents live infrastructure, include the runtime state directly in the table instead of forcing a second `show` command. Reserve `--json` for full structured output used by automation.

## Batch Runtime Reconciliation

Runtime-backed read surfaces should query Lima once per response and merge those observations onto the returned nodes instead of trusting persisted `running` or `stopped` metadata.
That keeps `node list`, `node show`, `node status`, and tree-style views aligned with the actual VM state even when a VM was started or stopped outside the normal CodeLima lifecycle path, while still leaving CodeLima-owned lifecycle states such as `created`, `failed`, and `terminated` in local metadata.

## Lifecycle-Only Node Metadata

`node.yaml` should persist only CodeLima-owned lifecycle state plus stable node metadata such as workspace mode, template paths, bootstrap flags, and Lima instance identity.
Live runtime facts like `running`, `stopped`, `last_runtime_observation`, and `last_reconciled_at` belong to Lima and should be populated in memory on demand instead of being written back into the node metadata file.

## Project Tree Includes Nodes

Lineage views should expose both projects and their attached nodes.
Keep nodes scoped under their owning project in both the human tree render and the structured JSON tree so operators can see the full topology from one command.

## Per-Mode Slog Sinks With A Shared Level

Observability routes through `log/slog` with sinks chosen per run mode. A `Service` holds `logger *slog.Logger` plus `logLevel slog.Level`, installed via `SetLogger` (default discard, nil-safe `log()` accessor, pointer-shared by `withIO`'s clone). `Run` installs a stderr `TextHandler` for CLI commands; TUI mode swaps in a size-rotated file sink at `CODELIMA_HOME/_logs/codelima.log` via `enableFileLogging`, at the same `--log-level`. A process-global package logger (default discard, pointed at the file sink at TUI start) reaches free functions that have no `Service` in scope — the libghostty cgo stderr capture forwards each captured line through it at debug. Log at seams only (operation start/finish, runtime command verbs, terminal open/close, refresh errors), with operation start/finish at debug so a default `info` CLI stays quiet. In the TUI, a durable `tuiMessageLog` ring backs a scrollable messages view: transient `a.status` is mirrored into the ring at info on every draw, while true-level entries (operation results, rollback failures, refresh failures) go through the single `pushMessage` entry point.

## Opaque Terminal Identity

A terminal's *identity* is modeled explicitly and kept separate from where it is shown. The `internal/codelima/terminal` leaf package (which imports nothing from the parent `codelima` package, so the dependency arrow always points inward) owns two identity types: `TargetKey{Kind, ID}`, the durable project-or-node a terminal belongs to, and `TerminalID`, the opaque per-terminal handle. `TargetKey.String()` renders exactly `"project:<id>"`/`"node:<id>"`, byte-identical to the strings the TUI has always produced, and `ParseTargetKey` is the single chokepoint that turns those strings back into keys — code must never hand-build the prefix with `+` or hand-parse it with `strings.HasPrefix`/`TrimPrefix`. `TargetKey` is a comparable value, so it may be used directly as a map key; where a string is still required at an edge (a string-typed field, a display string, or a mixed resource-key list that also carries non-target keys such as `"projects"`), call `.String()`. `TerminalID` is opaque and **never derived from a tab index, pane, layout position, or the `TargetKey`** — identity must survive any change in how terminals are arranged, displayed, or owned. Session (tab) keys keep the `"<target>#<n>"` form via the shared `formatSessionKey` helper; the `#n` suffix is a per-target ordering/display discriminator only and is never parsed back — session→target resolution uses the stored `session.target` field.

Live terminal backends are owned by a single lock-free `TerminalRuntimeRegistry` on the event loop, keyed by opaque `TerminalID`; a `tuiSession` holds only the id and resolves its backend through the store (`SessionTerminal`). Nothing outside the registry holds a live terminal, and the store never exposes its session/error maps — callers go through accessor methods (`SessionTerminal`, `SessionError`, `ClearSessionError`, `SyncFocus`, `PruneStaleSessions`). This is the daemon-boundary discipline: the UI never reaches into runtime-owner internals.

## Group Kill Session Leader

Embedded terminal children are started with `Setsid`, making each child a session leader whose process-group id equals its pid, so `kill(-pid, sig)` reaches every descendant that stays in the group. Terminal teardown funnels through `shutdownTerminalProcess` (`tui_terminal_shutdown.go`): close the writer and PTY master (SIGHUP to the foreground group) → 250ms grace → SIGTERM to the group → 250ms grace → SIGKILL → bounded reap (2s at 20ms polling), with ESRCH treated as success and `cmd.Wait` running exactly once in the owner's reaper. Both terminal backends must route Close through this helper rather than killing the direct child PID (ADR 56).
