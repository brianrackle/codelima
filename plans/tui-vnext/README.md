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
| [Detail · Logs & events](reference/NodeLogs.dc.html) | Visual reference and associated canvas notes | Partial: four bounded sources, controls and real failed-bootstrap capture verified; final qualification remains |
| [Detail · Spec & template drift](reference/NodeSpec.dc.html) | Visual reference and associated canvas notes | Partial: frozen/current comparison, YAML, actions and real failure/retry step records verified; final qualification remains |
| [Detail · starting (provisioning)](reference/NodeStarting.dc.html) | Visual reference and associated canvas notes | Partial: real Lima phases/output, cancellation and queued attachment verified at both sizes; final artboard qualification remains |
| [Detail · failed](reference/NodeFailed.dc.html) | Visual reference and associated canvas notes | Partial: real failure/source actions and VM inspection verified at both sizes; readiness-gated ports and final artboard qualification remain |
| [Create node · validation](reference/CreateNode.dc.html) | ADR 174 form, async validation and launch intent | Partial; form/launch flows verified, final artboard/native qualification open |
| [Create node · template picker](reference/TemplatePicker.dc.html) | ADRs 174–175 filter/details/shared new-template editor and return | Partial; live return verified, final fidelity/native qualification pending |
| [Confirmations](reference/Dialogs.dc.html) | Visual reference and associated canvas notes | Pending |
| [Templates · configurations](reference/Templates.dc.html) | ADR 176 global usage, frozen drift, stable selection and responsive list/details | Partial; final artboard/native qualification pending |
| [Templates · edit](reference/TemplateEdit.dc.html) | ADR 175 shared draft, resources, ordered environments, command blocks, preview and frozen-node impact | Partial; live wide/compact flows verified, final modal/native qualification pending |
| [Templates · environments](reference/Environments.dc.html) | ADRs 176–178 ordered drafts, external editing, execution evidence, disposable tests and dependency hints | Partial; disposable runs and reviewable dependency fixes verified, final artboard/native qualification pending |
| [Ports · forwarding](reference/Ports.dc.html) | Visual reference and associated canvas notes | Pending |
| [Activity · operations & messages](reference/Activity.dc.html) | Visual reference and associated canvas notes | Partial: shared operations/messages, native events, pending-event handoff, aligned responsive presentation, mouse controls and typed/exact-session navigation; final cross-view/native qualification remains |
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

Logs now reads four bounded local sources without runtime preparation and
captures bootstrap/validation output in private rotating files (ADR 157).
Follow, severity, search, wrap, line navigation, copy and pager controls have
automated coverage, including stale completions, paste ownership, Unicode
graphemes and failed bootstrap state/output. Full verify, race and integration
checks pass. Live terminal checks with isolated diagnostic fixtures covered
150×38/80×24 layouts, source selection, following/pausing/appending, search,
severity, wrapping, clipboard delivery and pager restoration. A fallback
grapheme-width defect is fixed and verified across resize/search (ADR 158).
Tall-record searches reveal the matching continuation. The upstream Vaxis
mise check passes. The daemon, terminals, sockets and diagnostic fixtures were
fully cleaned. The later step/retry run verifies actual failed guest-bootstrap
output; final native/artboard qualification remains open in QA.md and TODO #57.

Spec now has a read-only frozen/current recipe comparison and actual stored
Lima YAML inspection (ADR 159). Wide and compact panels, drift refresh,
new/clone/edit, long-command scrolling, bootstrap log navigation, pager and
path copy passed isolated terminal checks. The run also verified missing
dependencies, malformed YAML and corrected Create-form defaults. Focused
tests, full verify, race and CLI integration checks pass. Per-step provenance,
execution status/timing and explicit retry now have durable records (ADR 160),
with legacy unknowns and inherited clone execution identity preserved.
Attempt IDs link to captured output. Focused, full verify, race and integration
checks pass. Real Ubuntu failure output, exit 23, frozen source/timing,
wide/compact step display, exact-attempt output navigation and TUI retry were
verified. The successful prefix kept its attempt and guest counter; ordinary
start of the restored failure fixture reran that prefix. Refresh now retains
the selected step when error rows appear. TUI stop shut down the failed guest
normally and released allocation. Full verify, race and integration pass.
The reusable stopped guest and its testing
state remain under `tmp/stepqa` while lifecycle qualification continues. Starting/
Failed views, operation ownership and final native/artboard qualification remain open.
The disposable Spec fixture, TUI, daemon, tmux/socket and screenshots were cleaned.

Protocol 11 adds daemon-owned operations with private seven-day receipts,
bounded output/history pages, identity-based arbitration and cancellation
(ADR 161). Activity projects shared operation history with stable selection,
severity/search, clipboard, mouse, pagination and cross-scope node navigation.
Socket tests cover requester exit and cancellation by another frontend. Live
150×38/80×24 checks confirmed quit/reopen survival, conflicts, update refusal
and Activity controls. A real canceled boot exposed a cached stop decision;
uncached pre/post observations and regression tests fix it. Repeated
cancellation stayed active through Lima's shutdown fallback until the guest
stopped and allocation released. Restart retained a blocked operation as
interrupted without finish time or replay. Injected commands and the extra
scope were cleaned, and original node metadata restored. Shared general
messages, phase/boot output, CLI admission, Starting/Failed and final artboard
qualification remain open. The reusable stopped guest and protocol-11
daemon/TUI remain under `tmp/stepqa` for that work.
Live error filtering/copy and newer/older navigation over 55 receipts passed.
Focused tests, full verify, race and integration pass for this checkpoint.

CLI lifecycle admission now shares those daemon receipts (ADR 162). It resolves
create directories before submission, sends progress to stderr, preserves
result stdout and supplies receipt inspection after an interrupted wait.
`operation list/show/cancel` provide terminal access to shared history and
cancellation. Startup protects a responsive operation-owning daemon and asks
for explicit update. Focused socket tests, full verify, race and integration
pass. Live checks covered relative-path creation, JSON separation, a killed
caller and Ctrl+C, continued work visible in Activity, explicit cancellation,
history cursors, missing IDs and enabled/disabled autostart with local help. The extra
test guest and runtime override were removed. Shared messages, phase/boot
output and lifecycle detail views remain open.

Protocol 12 supplies observed phase state, timestamps and both boot streams
(ADR 163). Command updates preserve phase start time; completed work is skipped
without invented durations, failure leaves later phases pending, and daemon
interruption leaves finish unknown. Focused, full verify, race and integration
pass. A real boot failure from stale VirtioFS sockets retained the correct
failed/pending phases and output; stale QA sockets were removed. Real retry
preserved the successful prefix, and both an already-running start and a full
stop/start retained all bootstrap attempt IDs while rerunning validation.
Measured and skipped phase fields matched those actions.

Protocol 13 adds independent latest-start inspection and receipt-bound detail
views (ADR 164). Tests cover late replies, queued attachment readiness/navigation,
real-socket cancellation, failure precedence and status rollback/interruption.
Live checks at both sizes verified boot/validation progress, Metrics lockout,
queue Escape/navigation/failure, one attachment on successful readiness,
raw input/detach/reuse/exit, failed-step provenance/output, source editors and
inspection. Confirmed cancellation stopped the VM under its original receipt.
Navigation messaging and cancellation affordances were tightened after review;
final focused/full verify, race, integration and gopls checks pass. Fixture changes are restored
and terminal sessions ended. The stopped QA VM, daemon/TUI, two tmpfs mounts
and evidence remain under `tmp/stepqa`; four old VirtioFS socket entries require
host-side removal (see TODO). Shared general Activity messages, forwarding
readiness and final native/artboard qualification remain open.

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
window-local general messages alongside shared operation history, incomplete final visual qualification,
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

Protocol 14 adds a private, bounded daemon message journal and a merged Activity
projection (ADR 165). General TUI notifications survive restart after an explicit
acknowledgement; pending messages remain visibly local. Requests and completions
retain distinct stable IDs, with CLI/TUI provenance on new receipts. Message
writes retry idempotently and use epoch/sequence watermarks to bridge the interval
before history refresh. Known lifecycle output is not republished into this
journal. Footer notices expire after six seconds while history remains.
Native route/capacity/update producers and final artboard qualification remain
open. Full verify, full race, integration and focused race checks pass. Gopls
reported no new-file diagnostics and three pre-existing modernization suggestions
in the operation wrapper. Live checks passed at 150×38 and 80×24: two independent
TUIs shared one copied-record notice, 60 seeded messages paginated correctly,
search/severity and older-page node opening worked, pending offline messages
remained local and became shared once after reconnect, and all messages survived
restart. A corrupted journal left operation history visible with an explicit
message error. Live retries exposed notification flooding; a red regression and
fix now record one outage and recovery across twenty seconds of retries.
The corruption fixture was restored/removed, both TUIs and the daemon stopped,
and all 87 disposable message records removed. Slice scratch evidence is cleaned;
the stopped reusable guest/mounts and earlier qualification state remain under
the existing TODO cleanup ledger.

Protocol 15 adds bounded measured progress to operation list summaries (ADR 166).
Activity renders current phase, step/source/command, completed/skipped phase
counts and elapsed time in wide columns or compact two-line rows. Legacy phase
absence stays explicit; completion durations require an observed finish.
Full verify, race, integration and gopls checks pass. Live checks at 150×38 and
80×24 confirmed Boot at 1/7, Bootstrap at 4/7 with step 2/3/source detail,
second-line mouse selection, cancellation and receipt selection after completion.
The displayed 6m24s matches the recorded timestamps, and Lima reported Stopped.
The original node/bootstrap bytes are restored; the TUI and daemon are stopped,
and this slice's message, receipt and scratch evidence are removed. The reusable
guest and earlier cleanup state remain in TODO. Native daemon event producers remain required,
with startup ownership, update quiescing and restored-bell baselines documented
in TODO rather than introducing writes before the daemon holds its lock.

The Activity handoff regression reproduced a stale cache when a successor wrote
a message before the final acknowledgment failed. ADR 167 invalidates that cache
under the update barrier before the predecessor resumes. Focused checks now
preserve the message, idempotent retry and sequence continuity. Full verify,
race, integration and gopls pass; the first race invocation exited 143 without
a test failure, and the isolated rerun completed. Live `daemon update` fault
injection confirmed the recovered epoch,
retry identity and sequences 1/2/3. All three records survived restart and
appeared in Activity. The disposable daemon/TUI, home and slice scratch files
are removed. Native producers and final design qualification remain open.

Protocol 16 / ADR 168 adds native route/localhost-owner transitions and global
CPU/memory allocation warnings/recovery, a bounded persistence worker started
under daemon ownership, and committed warning delivery to frontend footers.
Focused tests, full verify, race, integration and gopls pass, including forwarding
callbacks outside locks, no-TUI persistence, retry/overflow behavior and
ownership-loss retirement. Live 150×38/80×24 checks confirmed unattended CPU
overcommit, route addition/ownership, degradation with HTTP 502, recovery with
HTTP 200, removal/release and CPU recovery after shutdown. Warning footers show
the node, expire after six seconds, and Activity opens the owning node. All
eight native records survived daemon restart unchanged. The start/stop receipts
succeeded; the interrupted stop CLI waiter was checked against the durable
receipt and Lima's Stopped state. Original fixture bytes are restored, the TUI
and daemon stopped, and slice records, HTTP files and scratch evidence removed.
Two-guest ownership qualification remains in TODO. A follow-up check showed the
apparently missing completion was retained scroll position: three controlled
receipts refreshed immediately, and `g` revealed the newest rows after compact
scrolling and widening. Its fixture and artifacts were restored/removed.
Native terminal bells/notifications and final Activity fidelity still remain.
ADR 169 records committed daemon-update history. Focused tests, full verify,
race, integration and gopls pass. A real unattended update preserved two host
shell identities and child PIDs; subsequent input worked and one record reported
two sessions kept. The record rendered at 150×38 and 80×24 and survived restart
unchanged. Injected import failure preserved the predecessor and shells without
recording success. The disposable sessions, TUI, daemon, home and slice scratch
artifacts are removed. ADR 170 supplies typed outcomes/navigation and terminal
notices below; final Activity qualification remains open.

Protocol 17 / renderer protocol 5 adds counted daemon-owned terminal notices
and typed Activity actions (ADR 170). Focused/native checkpoint tests, full
verify, race, integration and gopls pass. Real host shells emitted a bell and
notice with both TUIs closed; opening two windows did not duplicate them, and
identical text emitted again created a new record. Wide and compact Enter
attached the exact second session, including from a scope excluding its node;
input confirmed its PID and detach restored Activity. An ended session left
readable history and never created or substituted another session. Live update
preserved the shell and did not replay notices; renderer restart also avoided
replay and recorded subsequent output. All four terminal records survived cold
restart unchanged. At 80×24, explicit QA Host/Ports actions opened their views;
the real successful update displayed a check mark and opened Daemon. Cancellation
and late replies have automated coverage; delayed live attachment and final
native/artboard qualification remain. The two TUIs, shells, renderers and daemon
are stopped; the isolated `tmp/naq` home, 21 journal fixtures and 46 slice scratch
files are removed. The reusable base QA environment remains in the cleanup ledger.
Review found that memory-only native events pending at the handoff boundary could
be lost: the predecessor cannot flush under the update barrier and the successor
correctly suppresses old metadata. ADR 171 resolves that pending-queue gap below.

Handoff version 6 transfers pending native messages, stable IDs, overflow counts
and terminal/route/capacity baselines at one observation boundary (ADR 171).
The real-socket regression reproduced loss before the fix. A live storage outage
then exposed a permanently cached journal-open error; its regression now keeps
storage unavailable through commit, and initialization retries at most once per
second. Focused, full verify, race, integration and gopls checks pass. The combined
verification command exited 143 during race without a test failure; the standalone
rerun completed successfully. Live checks recovered four previously queued notices
without restarting the shell, then verified a fresh blocked-journal update and
injected rollback, each retaining its two notices exactly once. An identical new
notice created a new record. Wide/compact Activity rendered the recovered history;
Enter attached the original PID and detach restored its filter. The isolated
TUI, shell, renderer, daemon, home, 13 journal fixtures and slice scratch evidence
are removed. Final presentation/native qualification, post-commit reply-loss
reconciliation and the reusable base QA cleanup remain in TODO #57.

Activity presentation now uses cell-aligned timestamp/glyph/node/message/action
columns, full-row ANSI/truecolor selection, border titles, focused panels and
clickable severity choices (ADR 172). Request glyphs use canonical IDs. Compact
history preserves a one-cell action hint; running rows retain two-line observed
progress. Older dates and a visible filtered row range identify retained history.
Footer and per-row mouse actions reuse existing input/navigation paths; running
header counts are amber. Focused regressions, full verify and gopls checks passed.
Live 150×38 and 80×24 inspection used explicitly labeled disposable history
fixtures plus a real VM start. Severity, filtering/cancel, copy to the tmux
clipboard, help, paging, scrolling, running-row open, Host/Ports row actions and
header return worked. Clicking the running row’s cancel action stopped the VM,
retained the canceled receipt and cleared the running badge. Lima used its
three-minute force-stop fallback because cancellation preceded guest readiness;
the final runtime and receipt both confirmed stopped. The compact empty-filter
state also passed. The disposable daemon/TUI, 67 message records, 4 operation
receipts, homes, clipboard buffer and slice scratch files were removed.
Final native/cross-view qualification remains open.

The create form’s launch workflow now has a daemon-owned create-and-start
contract (ADR 173, ordinary protocol 18). The CLI exposes --start; creation-only
remains the default. The combined receipt publishes the new UUID before boot,
retains slug/UUID arbitration, exposes eight observed phases, participates in
latest-start and TUI readiness queries, and stops the VM on explicit cancellation.
Focused regression checks include real socket disconnect and partial failures.
Full verify, race and integration suites pass. Gopls reported only existing
slice-copy and struct-omitempty advisories. Live CLI/TUI checks confirmed a
rejected create-and-start receipt, its request/failure history at 150×38 and
80×24, severity filtering and clipboard copy. The isolated daemon, TUI,
receipt, homes, clipboard entry and scratch evidence were removed. These
backend-only checks are extended by the ADR 174 form/guest evidence below;
final artboard/native qualification remains in TODO #57.

ADR 174 integrates the create form with shared input/discard behavior, async
global-name/path checks, symlink boundaries, directory browsing, nested template
filter/details/new-template return, observed host-fit warnings and launch choices.
Admission and transient UUID navigation/attachment retain their receipt identity.
Full verify, race and integration checks passed. The subsequent compact phase
viewport adjustment passed the create and lifecycle suites, formatting, lint,
build and gopls; live 80×24 now displays all eight phases. Live
wide/compact checks covered name suggestions, directory browsing, invalid ports,
metadata symlink rejection, retained edits, template save rejection/return and
mouse launch dependencies. Create-only opened Overview with no terminal. A fresh
combined creation reached Ready under one UUID-bound receipt; explicit stops
by slug and UUID were refused during boot. Zero shells existed before completion;
exactly one opened after successful validation and accepted a command. Lima's
first guest-agent forward had lost its local socket: canceling that stale SSH
forward allowed Lima to recreate it and the same receipt to finish. This is
qualified attachment behavior with an explicitly repaired test transport, not
evidence of an entirely unattended first boot on this nested-VM host.
A separate first-boot firmware failure retained its node UUID and error, rendered
Boot failed and canceled queued attachment without a shell. The fixture was
deleted. Leaving another form-created startup canceled attachment while the
same receipt progressed to Ready; Activity stayed open and there were no shells.
That run needed the same Lima socket repair. Interrupting a CLI creation wait
returned its receipt ID while the operation continued through Boot. Explicit
cancellation kept that same receipt canceling, without a finish timestamp, while
Lima still reported the VM running. After Lima's three-minute shutdown fallback,
the receipt became canceled only with the VM stopped and both runtime processes
gone. No terminal existed. All form QA nodes/templates, terminals, daemon/TUI,
homes, logs and captures were removed; the ext4 volume was unmounted, its loop
device released and its sparse image removed.
The shared editor follow-up is recorded below. Remaining qualification and the
older stepqa cleanup debt are in TODO #57.

## Shared template editor (ADR 175)

Templates create/edit/clone, Spec and the nested create picker now share one
draft. Resource steps and typed sizes, installed image/profile choices, ordered
environment checks, per-command multiline buffers, resolved preview, global
frozen-node counts and reviewable built-in defaults use service data. Definition
loads and saves remain outside the input loop and completion events are fenced
to the originating editor. Compact windows scroll to the focused field and F2
exposes wrapped execution steps, impact and changes.

Live terminal verification at 150×38 and 80×24 covered invalid/duplicate names,
all resource steps and typed `20G`, installed image/profile choices, toggling and
reordering environments, command add/remove/reorder/apply/discard, long preview
scrolling, default-name protection and restore markers. Real bracketed paste
exposed missing control-key line breaks and tab expansion; regression tests and
the corrected live paste now preserve Unicode, LF and tabs byte-for-byte.
Literal tabs are shown as ⇥ and preview line breaks as ↵.

A real created-only node retained its original 1 CPU/1 GiB/8 GiB and command
order after editing its template. The next created-only node received
2 CPUs/20 GiB/10 GiB and exactly six previewed steps: custom profile install,
two reordered environments, two direct command blocks and profile validation.
Neither VM was started. A clone saved empty environment/direct-command lists,
mouse resource controls and environment toggles worked, `small` was restored,
and creating through the picker returned with the new template selected while
retaining the node name and `5432:5432` port draft.

`make verify`, `make test-race`, `make test-integration`,
`make test-template-editor`, `make test-vaxis-fork` and
`make test-vaxis-upstream` passed. The last target runs the vendored repository's
required `mise run check`, including formatting, custom lint and tests.
`gopls check` reported no errors (only modernization suggestions).
Both disposable nodes and their Lima directories, three custom templates,
two environments, custom profile, daemon/TUI, tmux window, isolated homes,
screenshots, logs and project-local test scratch were removed. The prior stepqa
fixture remains separately recorded cleanup debt. Final manager-board fidelity,
generic child-modal mouse/presentation and native qualification remain open;
this does not qualify the complete redesign or the complete QA matrix.

## Recipe catalogs and environment drafts (ADR 176)

Templates and Environments now show counted tabs, scrollable cell-aligned
lists, global usage and responsive details. Configuration details share Node
Spec's frozen/current comparison without reading Lima files or probing the
runtime. Environment details show ordered blocks, root execution context,
template references and retained command outcomes. Deleted nodes supply history
without inflating usage; copied execution receipts do not count as new runs.
Unavailable metadata is explicit, and selection follows stable IDs through
refreshes. Successful deletion reloads the catalog and clamps its selection.

Environment new/edit/clone/restore, reordering and removal use a reviewable
draft and the same multiline command editor as templates. Duplicate saves keep
the draft, existing names remain fixed, and empty lists can be saved. E invokes
EDITOR with a private command file, resumes the TUI with an unsaved result and
cleans scratch on success/failure. Notes and notices have their own row, leaving
mouse actions visible; focused command hints fit an 80-column terminal.

Live 150×38 and 80×24 checks covered out-of-scope usage, frozen-resource drift,
long wrapped details, command selection/reorder/remove/add, raw bracketed
Unicode/LF/tab paste, exact external-editor output, editor failure cleanup,
dirty keep/discard, duplicate-name retry, empty clones, built-in markers/reset,
mouse tabs/rows/actions and pagination across 20 environment rows. The real
created-only node retained its original resources and commands throughout;
neither this inspection nor editing booted its VM. Deletion errors named the
blocking template/node. Live deletion exposed the missing catalog refresh;
service-backed dialog regression tests now cover that completion path.

The final build also passed live configuration and environment deletion: counts
updated immediately and the next valid selection stayed visible after resize.
`make verify`, `make test-recipes`, `make test-race` and
`make test-integration` passed. `gopls check` found no errors; `go doc` was used
to inspect the process and scratch-file APIs. One race attempt ended with
SIGTERM during manual cleanup; the complete rerun with no manual QA processes
active passed. No source changes followed the final checks.

The created-only node and its Lima directory, all custom recipe definitions,
daemon/TUI, tmux window, homes, scripts, screenshots and test scratch were
removed, including tmp/recipes and tmp/.go-work. Integration scratch was removed
by its Make target. The older stepqa fixture remains separate cleanup debt.

The dependency heuristic, final artboard/native qualification and the complete
QA matrix remain open in TODO #57. This checkpoint does not complete the
major-version redesign.

## Disposable environment runs (ADR 177)

The daemon admits a frozen environment/xsmall recipe before side effects,
reserves a private test node and owns cleanup independently of execution
cancellation. Cleanup recovery never reruns commands and does not expire with
ordinary history. The TUI t action opens live output in Activity; retained
output can be reopened after deletion and late replies cannot steal navigation.

Live 150×38/80×24 checks covered keyboard/mouse admission, streaming, scrolling,
closing/reopening output, TUI quit/reopen during boot, completed output after
deletion, last-run provenance and rejection of an empty recipe without a VM.
The final UI identifies running tests by environment and hides cancellation
on completed receipts. The TEST RUN section explains the disposable VM.

Four actual Ubuntu 24.04 guests used the current xsmall resources and a
project-local ext4 loop volume because the shared filesystem does not support
the required Unix sockets. Success printed UID 0 and the private guest path,
kept the admitted command despite a source edit and completed one attempt.
Injected deletion failure survived daemon restart, then cleanup preserved
success without another command attempt. Failure printed its marker, exited
23 and left the second step pending with zero attempts. TUI K canceled a
sleeping command after its first marker; its final marker was never printed.
Both runs deleted their VM and private workspace and retained output.

Restart during early boot recovered cleanup without a frontend request and
kept execution interrupted with zero command attempts. Lima's three-minute
shutdown fallback stopped that guest; the bounded cleanup attempt timed out
and retried. Another injected deletion failure after restart retained the
original boot output and line offsets. Removing the fault allowed automatic
cleanup to finish. The created-only guard node's metadata and host marker
remained unchanged through all four runs. The first successful boot required
a scoped repair of Lima's stale guest-agent SSH forward; failure and cancellation
boots recovered that forward themselves. This is nested Linux/aarch64 software
emulation evidence, not complete native-platform qualification.

Regression tests reproduce and fix loss of retained output on a failed cleanup
after restart, stale retry text after successful cleanup and completed-test
footer actions. Final `make test-environment-runs`, `make verify`,
`make test-race` and `make test-integration` pass after all code changes.
`gopls check` found no errors, only modernization suggestions. The final build
also recovered a fixture receipt made pending after its VM had already been
deleted: output was unchanged, no VM or command was recreated, and corrected
completion details were visually checked at both sizes. An earlier build
received SIGTERM while manual QA was active; the separate build and complete
final gates with manual processes stopped passed.

The guard VM, all isolated metadata/definitions/workspaces/receipts,
daemon/TUI, tmux server/socket, `.l` mount and loop device/backing file,
temporary `tmp/stepqa/bin/limactl` fault wrapper, screenshots, scripts, logs,
`tmp/et` and Go test scratch were removed. The older stepqa fixture remains
separate cleanup debt. The dependency warning, final artboard/native
qualification, complete redesign and full QA matrix remain open in TODO #57.

## Environment dependency hints (ADR 178)

The catalog now parses bounded shell syntax without execution, flags literal
curl uses without recognized earlier installation and derives provider hints
from current saved definitions. Conservative control-flow handling excludes
conditional, background and simulated installs from unconditional evidence.
Enter or the mouse opens a source-preserving edit in the shared unsaved draft;
normal editing, explicit save and discard remain available. The parser is
pinned to mvdan.cc/sh/v3 v3.12.0 with creack/pty v1.1.24 selected by its module.

Focused analysis/draft checks, recipe regressions, full verify, terminal
attachment, race and CLI integration passed. A later oversized-command
regression showed that analysis-limit notices could be buried below the script;
notices now precede command content. The regression, recipe suite and final
build pass, and gopls reports no diagnostics. Final full verify, race and CLI
integration also pass (core unit 49.576s, core race 69.596s, integration 16.631s).

Live 150×38 and 80×24 checks inspected warnings and reviewable drafts. Enter
and a mouse click opened the exact earlier install; discard preserved the
saved source. Explicit apply/save inserted only curl, preserving Unicode,
comments, quoting and multiline content, and cleared the warning. Editing the
codex provider removed its name from the hint. Quoted examples and literal
here-documents did not warn; conditional, simulated and background installs
did not establish a prerequisite. No-safe-fix Enter retained ordinary editing,
while e opened the normal environment editor. Invalid and 65,537-byte blocks
showed visible unavailable notices and remained editable/saveable. Browsing
and editing created no VM, and the host execution marker remained absent.

The isolated daemon, TUI and tmux have stopped; the metadata/socket tmpfs was
unmounted. All definitions, captures, logs and other scratch under `tmp/dq`,
plus focused-test scratch, have been removed. No VM was created. The parser is advisory;
unsupported dynamic shell behavior remains unknown. Full redesign and native/
artboard qualification remain open in TODO #57.
