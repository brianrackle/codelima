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

`EnsureLayout` seeds durable built-in metadata such as agent profiles and reusable environment configs.
Those defaults are written into the normal store on first use, so the CLI and TUI see the same `codex` and `claude-code` environment configs without special UI logic. Existing user edits or deletions win over later startup, which keeps the seeded defaults behaving like ordinary user-managed records instead of immortal system entries.

## Workspace-Aware Shell Delegation

Shell entry always normalizes the documented `--` separator before delegating to Lima.
Interactive shells and one-off commands pass the node workspace through `limactl shell --workdir` so shell entry is stable regardless of the host directory where `codelima` is launched.

## TUI Session Reuse

The TUI keeps one PTY-backed terminal backend per opened terminal target. Project targets launch a host-local interactive shell rooted at the project's workspace path, while node targets keep using `codelima shell <node>`. The preferred backend is Ghostty VT, with the older Vaxis terminal widget retained as a fallback when the native Ghostty library is unavailable. The session store should track the current embedded-pane size and apply that size to a terminal backend before starting its PTY-backed shell, so login prompts and other early output are rendered at the right dimensions instead of being lost on the first resize. When Ghostty exposes a terminal input, viewport, explicit/default-color, or terminal-query callback API, CodeLima should prefer that Ghostty-owned encoder, viewport state, color semantic, or query callback through a narrow compatibility wrapper and retain the local Go implementation only as a fallback for unavailable APIs or unsupported events. The Ghostty PTY transport should keep the shared PTY handle in blocking mode so the read loop continues to behave like a normal PTY reader, while a dedicated queued writer goroutine owns foreground-originated writes so UI events do not write directly on the render path. Mouse ownership should follow the guest terminal state: when the embedded terminal reports mouse capture, CodeLima forwards mouse input to the guest; otherwise CodeLima only uses terminal clicks for focus and hyperlink activation, leaves wheel-based local scrollback intact, and relies on the host terminal emulator's bypass gesture for text selection and copy. Tree focus owns a sticky right-pane mode: `info` by default for both projects and nodes, `terminal` when toggled with `i`. Changing the selected target only swaps which emulator or terminal placeholder is visible, so scrollback, alternate screens, cursor state, hyperlinks, and unsubmitted input stay attached to that project or node until the TUI exits. `Alt-\`` or `F6` continues to own only the tree-versus-fullscreen-terminal focus transition, and fullscreen focus must reuse the same selected target session that the split pane previews.

## Contextual TUI Actions

The TUI treats the tree selection as the context boundary for mutations.
Project and node hotkeys open right-pane dialogs, menus, and selectors that call the existing `Service` methods, then reload the tree while preserving expansion state and the most relevant selection. Those transient views replace the right pane instead of opening centered modals, so the tree stays visible and the wider pane can hold form fields, descriptions, and help text without obscuring the rest of the TUI. In tree focus, the footer should mirror the currently active pane controls instead of showing generic placeholder copy, so the visible hints always match the keys the operator can press at that moment. Project-scoped bootstrap commands and reusable environment configs use the same pattern via dedicated menus so list editing does not depend on multiline text entry. Command removal uses selectors plus explicit confirmation instead of typed indexes, and command reordering is expressed as single-step move actions so order-sensitive bootstrap lists stay editable without freeform text parsing. When a dialog needs a reusable config reference, it opens a selector in the same right pane instead of asking the operator to type slugs manually.

## Async Lima Progress In TUI

Lima-backed TUI mutations should run as background tasks instead of taking over the right pane.
Dialogs, menus, and selectors still use the right pane directly, but long-running project and node work should leave the current details or terminal surface usable while the tree and details pane render transient task state such as `starting`, `stopping`, or `deleting`. Each task should route `limactl` plus non-interactive guest-command output through its own service-scoped writer clone so streamed lines stay attached to the task that produced them, and the TUI should reject only resource-conflicting actions instead of freezing unrelated navigation or terminal focus changes.

## Bundled Runtime Wrapper

Release archives package the compiled `codelima` binary as `bin/codelima-real`, a small launcher script as `bin/codelima`, and the matching `libghostty-vt` shared library under `lib/`.
The wrapper owns `CODELIMA_GHOSTTY_VT_LIB`, so local packaging, manual tarball installs, and the generated Homebrew formula all reuse the same runtime layout instead of each inventing a different library-discovery scheme.

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
