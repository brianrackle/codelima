# Fleet-manager TUI implementation

Source: [Claude design](https://claude.ai/artifact/Pu9wB7f3KH3LaCF6rDm7ps), version `1792822420-f77f`.

The exact 26 artboards and canvas interaction notes are retained in `reference/`.
All example identities, metrics and timestamps in those files are reference data; the application must render real observations or explicit unavailable states.

## Delivery ledger

| Surface | Required behavior | Status |
|---|---|---|
| [Nodes · dashboard (home)](reference/Main.dc.html) | Visual reference and associated canvas notes | Pending |
| [Navigation map](reference/NavMap.dc.html) | Visual reference and associated canvas notes | Pending |
| [TUI kit · tokens & states](reference/Kit.dc.html) | Visual reference and associated canvas notes | Pending |
| [Nodes · filter, sort & bulk marks](reference/NodesFilter.dc.html) | Visual reference and associated canvas notes | Pending |
| [Nodes · first run (empty)](reference/NodesEmpty.dc.html) | Visual reference and associated canvas notes | Pending |
| [Nodes · compact 80×24](reference/NodesCompact.dc.html) | Visual reference and associated canvas notes | Pending |
| [Detail · Overview](reference/NodeOverview.dc.html) | Visual reference and associated canvas notes | Pending |
| [Detail · Metrics](reference/NodeMetrics.dc.html) | Visual reference and associated canvas notes | Partial: bounded history, detail telemetry and processes; live guest checks pass; final qualification open |
| [Detail · Logs & events](reference/NodeLogs.dc.html) | Visual reference and associated canvas notes | Pending |
| [Detail · Spec & template drift](reference/NodeSpec.dc.html) | Visual reference and associated canvas notes | Pending |
| [Detail · starting (provisioning)](reference/NodeStarting.dc.html) | Visual reference and associated canvas notes | Pending |
| [Detail · failed](reference/NodeFailed.dc.html) | Visual reference and associated canvas notes | Pending |
| [Create node · validation](reference/CreateNode.dc.html) | Visual reference and associated canvas notes | Pending |
| [Create node · template picker](reference/TemplatePicker.dc.html) | Visual reference and associated canvas notes | Pending |
| [Confirmations](reference/Dialogs.dc.html) | Visual reference and associated canvas notes | Pending |
| [Templates · configurations](reference/Templates.dc.html) | Visual reference and associated canvas notes | Pending |
| [Templates · edit](reference/TemplateEdit.dc.html) | Visual reference and associated canvas notes | Pending |
| [Templates · environments](reference/Environments.dc.html) | Visual reference and associated canvas notes | Pending |
| [Ports · forwarding](reference/Ports.dc.html) | Visual reference and associated canvas notes | Pending |
| [Activity · operations & messages](reference/Activity.dc.html) | Visual reference and associated canvas notes | Pending |
| [System · doctor, daemon, host, settings](reference/System.dc.html) | Visual reference and associated canvas notes | Pending |
| [Command palette](reference/Palette.dc.html) | Visual reference and associated canvas notes | Pending |
| [Help · keymap](reference/Help.dc.html) | Visual reference and associated canvas notes | Pending |
| [Shell · where to open](reference/ShellChooser.dc.html) | Visual reference and associated canvas notes | Pending |
| [Shell · attached (TUI suspended)](reference/ShellAttached.dc.html) | Visual reference and associated canvas notes | Pending |
| [Shell · embedded terminal (opt-in)](reference/ShellEmbedded.dc.html) | Visual reference and associated canvas notes | Pending |

## Cross-cutting requirements

- Preserve daemon-owned sessions, node/configuration identities and existing storage compatibility.
- Fleet dashboard is the launch view; selecting a node must never create a shell.
- Five global views, contextual navigation, command palette, key help and mouse equivalents.
- Scoped/all-node toggle, stable selection, grouping, typed filtering, sorting, marking and bulk confirmations.
- Actual host capacity, lifecycle progress/failure details, daemon-owned operation history, hour-long metric history and on-demand process samples.
- Raw terminal attach/detach, window/tmux launching, configurable shell mode; optional embedded shell passes every key except F6.
- Live validation, dirty-form cancellation, configuration/environment order, restore and isolated environment test runs.
- Dynamic forwarding presentation and explicit generic-route ownership pinning.
- Theme tokens and ANSI fallback; wide, medium and 80×24 layouts; no clipped interactive controls.
- Automated checks and every QA.md flow, live visual/interactive verification, documentation and disposable-state cleanup.

## Current evidence

- `ddabb01` checkpoints outstanding notes before implementation.
- Prior baseline `make verify` passed on Linux/aarch64; baseline TUI menus and resize were exercised. These do not qualify the redesign.
- Design files retrieved through an interactive Claude Code Artifact session; all 27 indexed files and manifest hashes verified.

## Implementation sequence

1. Fleet projection, typed navigation, adaptive geometry and reusable theme/panels.
2. Wire dashboard and global/detail views to existing service snapshots; replace old sidebar and shell-first defaults.
3. Add missing runtime contracts (history/processes, lifecycle progress/cancellation, raw attachment, route pins and settings) with regression tests.
4. Complete forms, palette/help, mouse/keyboard paths, narrow layouts and all reference states.
5. Run automated/native/manual gates, document results, resolve follow-ups and clean verification artifacts.

## Implementation in progress

The initial fleet projection, responsive table/preview, global navigation,
initial detail views, palette/help and F6-only shell interception are wired
into the app (ADR 150). `TestTUIFleet*`, `TestTUIManager*` and resize regressions
pass. Full `make verify` and `make test-race` also pass after regression migration
and compact, scrolling forms with dirty-cancel confirmation. An isolated live
TUI verified navigation, palette filtering, resizing and edited-form cancellation;
the disposable daemon, tmux session and home were cleaned. Scope refreshes now
capture their request state and reject stale replies with regression coverage.
Runtime contracts and artboard
interaction completion remain open; no artboard is marked delivered yet.

Raw attachment is wired to `x`, interactive `codelima shell` and `terminal attach`;
native export and real-PTY tests cover detach/survival, reattach, final output
and exclusive query replies (ADR 151). A real host shell retained its PID and
variables across detach/reattach; resize, `vi` suspension, existing-session
selection, exit counts and manager restoration were exercised visually. The
verification home, daemon, sessions and files were cleaned. Guest lifecycle,
remaining terminal modes and full shell choices still need qualification.
Settings now support shell mode/window, credential defaults, stop confirmation,
grouping and color through comment-preserving edits. The chooser adds window
and tmux launch, shifted default choices, scrolling, Enter and mouse activation
(ADR 152). Live settings at 120×34 and 80×24 verified persistence and ANSI/auto
color, with the disposable daemon/home cleaned. Verify, race and integration
checks pass; external-window and guest-session qualification remain open.
Current limitations include published-port-only Ports,
window-local Activity, incomplete final visual qualification, basic event Logs,
and existing forms inside the new modal geometry. These are implementation
stages, not reductions of the accepted scope. See TODO #57.

Host model/CPU/memory and LIMA_HOME free space now use native observations
(ADR 153). Allocation is global across scopes and includes failed nodes whose
VMs remain up. System renders four responsive panels with keyboard and mouse
navigation. Focused tests and macOS sampler cross-builds pass; runtime macOS
sampling and two-VM allocation changes remain part of native qualification.
Daemon actions and the rest of System's reference behavior remain in progress.

Full verify, race and integration checks pass for host capacity. Live 150×38
and 80×24 panels were inspected and compared with OS readings; the VirtioFS
fragment-size regression prevents an inflated disk total. A real Ubuntu guest
now boots under TCG/Cortex-A72; raw detach/reattach retained its shell PID and
variable through resize. Tmux and a headless external launcher resumed that same session. Native desktop
window behavior still requires qualification. Global allocation counted a failed
but running VM outside the current directory scope, then released it on stop.
Two-VM overcommit was visible before host memory exhaustion killed one fixture.
Both nodes, daemons, tmux/SSH processes, socket mount and verification files
were stopped/deleted/unmounted and cleaned; sustained multi-VM and native KVM
qualification remain open.

Metrics now implements one-hour aggregate rings and optional process snapshots
over the existing daemon SSH peer (ADR 155, protocol 9). Real time plots,
per-core CPU, cache/swap, uptime, root-disk growth, four windows, pause, sorting,
process selection/copy and responsive layouts have focused coverage. Full
verify, race and integration pass; new process/rendering files have clean gopls
checks. Live 150×38 and 80×24 screens were inspected. Procfs/disk comparisons,
a CPU workload, unchanged paused screen, CPU/memory ordering and clipboard PID
delivery passed. History survived new client connections and reset on daemon
replacement; unavailable connections retained history with explicit errors.
The fixture was fully cleaned. Nested Lima guest-agent socket degradation and
a later host-agent SIGINT limit lifecycle conclusions from that run.
Fleet/preview sparklines and both global top process sets during pause now have
bounded data and regression coverage (ADR 156, protocol 10). Full verify, race
and integration pass; gopls is clean. Final live
fleet/preview and disjoint-ranking qualification, plus artboard/native checks,
remain in TODO #57.
