# Fleet-manager TUI implementation

Source: [Claude design](https://claude.ai/artifact/Pu9wB7f3KH3LaCF6rDm7ps), version `1792822420-f77f`.

The exact 26 artboards and canvas interaction notes are retained in `reference/`.
All example identities, metrics and timestamps in those files are reference data; the application must render real observations or explicit unavailable states.

## Delivery ledger

| Surface | Required behavior | Status |
|---|---|---|
| [Nodes · dashboard (home)](reference/Main.dc.html) | Live fleet, preview, lifecycle actions and routes | Partial: shared observations/routes (ADR 186), bounded recent/failure evidence and explicit session activity (ADR 187); runtime configuration, CPU/memory trends and task detail (ADR 188); prompt idle timing (ADR 189); per-agent recorded validation (ADR 190); final qualification remains |
| [Navigation map](reference/NavMap.dc.html) | Launch, global views, detail/back, editors/pickers, confirmations and shell routes | Implemented through the shared action/context registry and existing navigation (ADRs 150–200); attached-header presentation remains separately tracked below |
| [TUI kit · tokens & states](reference/Kit.dc.html) | Palette, states, gauges and motion | Partial: shared fill/track gauges and 10-fps foreground progress cells (ADRs 201–202); final qualification remains |
| [Nodes · filter, sort & bulk marks](reference/NodesFilter.dc.html) | Typed filters, identity marks, metric sorting, batch review/actions | Partial: hidden-target/global-allocation summary (ADR 186) and guest/host session impact (ADR 187); per-target task detail added (ADR 188); prompt idle timing added (ADR 189); captured durable batches and retained outcomes verified (ADR 198); final qualification remains |
| [Nodes · first run (empty)](reference/NodesEmpty.dc.html) | ADR 184 scoped creation, cached host checks and actual template defaults | Partial: wide/compact blocked-host controls and defaults verified; live healthy-host transition and final native/artboard qualification remain |
| [Nodes · compact 80×24](reference/NodesCompact.dc.html) | Condensed navigation/capacity, percentage table and selected-node summary | Partial: condensed navigation/capacity, phase rows and reserved selected/marked summary implemented (ADR 186); final artboard/native qualification remains |
| [Detail · Overview](reference/NodeOverview.dc.html) | Identity, resources, sessions, ports and recent events | Partial: five responsive panels, stable selected actions, daemon session age and shared bounded observations (ADR 191); recorded auth-import date/count/categories (ADR 203); final artboard/native qualification remains |
| [Detail · Metrics](reference/NodeMetrics.dc.html) | Visual reference and associated canvas notes | Partial: bounded history, detail telemetry and processes; live guest checks pass; final qualification open |
| [Detail · Logs & events](reference/NodeLogs.dc.html) | Visual reference and associated canvas notes | Partial: four bounded sources, controls and real failed-bootstrap capture verified; final qualification remains |
| [Detail · Spec & template drift](reference/NodeSpec.dc.html) | Visual reference and associated canvas notes | Partial: frozen/current comparison, YAML, actions and real failure/retry step records verified; final qualification remains |
| [Detail · starting (provisioning)](reference/NodeStarting.dc.html) | Visual reference and associated canvas notes | Partial: real Lima phases/output, cancellation and queued attachment verified at both sizes; final artboard qualification remains |
| [Detail · failed](reference/NodeFailed.dc.html) | Visual reference and associated canvas notes | Partial: real failure/source actions, VM inspection and readiness-gated ports verified; final artboard qualification remains |
| [Create node · validation](reference/CreateNode.dc.html) | ADR 174 form, async validation and launch intent | Partial; form/launch flows verified, final artboard/native qualification open |
| [Create node · template picker](reference/TemplatePicker.dc.html) | ADRs 174–175 filter/details/shared new-template editor and return | Partial; live return verified, final fidelity/native qualification pending |
| [Confirmations](reference/Dialogs.dc.html) | Exact-name delete, busy stop, clone/start and captured parallel batch review | Partial: delete/stop (ADR 196) and clone/start (ADR 197) passed automated and live Linux/TCG qualification; durable batch review, partial outcomes and cancellation verified (ADR 198); no-frontend terminal teardown passed focused/full automated and live checks (ADR 199); final native/artboard qualification remains |
| [Templates · configurations](reference/Templates.dc.html) | ADR 176 global usage, frozen drift, stable selection and responsive list/details | Partial; final artboard/native qualification pending |
| [Templates · edit](reference/TemplateEdit.dc.html) | ADR 175 shared draft, resources, ordered environments, command blocks, preview and frozen-node impact | Partial; live wide/compact flows verified, final modal/native qualification pending |
| [Templates · environments](reference/Environments.dc.html) | ADRs 176–178 ordered drafts, external editing, execution evidence, disposable tests and dependency hints | Partial; disposable runs and reviewable dependency fixes verified, final artboard/native qualification pending |
| [Ports · forwarding](reference/Ports.dc.html) | Visual reference and associated canvas notes | Partial: readiness/TCP, durable pins and interactive Ports pass two-guest Linux qualification (ADRs 182–183); final artboard/native qualification remains |
| [Activity · operations & messages](reference/Activity.dc.html) | Visual reference and associated canvas notes | Partial: shared operations/messages, native events, pending-event handoff, aligned responsive presentation, mouse controls and typed/exact-session navigation; final cross-view/native qualification remains |
| [System · doctor, daemon, host, settings](reference/System.dc.html) | ADRs 153/179–181 responsive capacity, retained Doctor findings, cached daemon status and explicit actions | Partial; Doctor, overview and action flows verified; macOS sampling and full artboard/native qualification remain |
| [Command palette](reference/Palette.dc.html) | Grouped fuzzy results, live context, recents and captured actions | Partial: result spans/counts, responsive modal, session recents, identity guards and directory Start/Retry (ADR 193); final whole-design/native qualification remains |
| [Help · keymap](reference/Help.dc.html) | Contextual groups, search, compact access and one-press behavior | Partial: dedicated read-only help and responsive keyboard/mouse navigation (ADR 192); remapping, effective hints, full automated gates and wide/compact Linux checks passed (ADR 200); final native qualification remains |
| [Shell · where to open](reference/ShellChooser.dc.html) | Grouped new/existing sessions, availability, captured attachment and saved defaults | Partial: dedicated responsive chooser and stable session shortcuts (ADR 194); live guest/host/window/tmux/embedded checks pass; final native/whole-design qualification remains |
| [Shell · attached (TUI suspended)](reference/ShellAttached.dc.html) | Visual reference and associated canvas notes | Pending |
| [Shell · embedded terminal (opt-in)](reference/ShellEmbedded.dc.html) | Opt-in detail, framed terminal, session strip, input ownership and live routes | Partial: shared viewport and active-tab overflow with mouse controls (ADR 195); host/guest, route and focus checks pass; final native/whole-design qualification remains |

## Cross-cutting requirements

Auth-import checkpoint (ADR 203), 2026-10-25: Overview projects the selected
node's retained import event into date, item count and recognized categories.
Empty imports, missing receipts and inherited clone credentials remain explicit.
Ordinary daemon protocol is 30; session/renderer/handoff remain 2/6/7.
`make verify` passed (core 73.498s), focused evidence/Overview checks passed,
and the evidence race gate passed (5.855s). The existing first-start credential
test also checks the projection from the actual importer event. A malformed
latest timestamp regression failed first and was corrected; final focused race
(5.715s), build and lint checks passed after that correction. No full gate was
repeated for text fixes.

Live checks used disposable metadata/event fixtures without importing any
credentials or creating a VM. At 150×38 truecolor and 80×24 ANSI, the receipt
states, item categories, panel focus, Identity scrolling and resize were checked.
Inspection found a joined item label and clipped missing-receipt text; both were
corrected, rebuilt and rechecked with the Overview tests (0.759s) and running TUI.
The final wide ANSI view also passed. The owned frontend, daemon, tmux server,
store, captures and 32 MiB tmpfs were removed; two new inactive exact-content
inputrc files were cleaned, preserving older and active files. The previously
tracked transient ANSI startup logo corruption was observed again; its existing
follow-up remains open, as do native qualification and the attached header.

Navigation-map reconciliation, 2026-10-25: the map is a routing reference, not
an additional screen. Current `tui_key_catalog.go` and `tui_navigation.go` cover
launch/Nodes, five global views, detail tabs/back, shared node actions, palette
and Help. Template/directory picker tests cover return to the preserved form;
chooser and raw-attachment tests cover exact-session routing and detach/return.
These are already implemented; the previous “Pending” label was stale. The
Attached artboard's fixed header remains an unresolved presentation difference
from the source's explicit real-terminal ownership requirement.


Spinner checkpoint (ADR 202), 2026-10-25: active progress cells use the Kit's
braille sequence on a 100 ms event-loop timer. Ticks touch registered cells only;
full draws rebuild their clipped positions, overlays own their registrations,
and idle/shutdown stop the timer. Static node statuses and historical request
symbols retain their meanings. `make build verify` passed (core tests 73.414s),
as did `make test-tui-spinners TUI_SPINNER_TEST_FLAGS=-race` (11.440s) and gopls.
The tests cover frame order, late ticks, partial paints, styles, clipping,
overlays, foreground loading/completion, hidden panels and shutdown, plus the
affected view regressions.
Live checks used an isolated store and a wrapper that delayed only the real
`limactl --version` check by three seconds. Both 150×38 truecolor and 80×24 ANSI
captures showed all ten frames in order at roughly 100 ms intervals. Help stayed
stable while the check ran; switching to Host hid its indicator. A resize to
100×28 and completion left no stale spinner. Captures were visually inspected;
no VM was created. The owned frontend, daemon, tmux server, delayed-command
wrapper, temporary store, mount and captures were removed. The prior
whole-design/native qualification items remain open.


Gauge checkpoint (ADR 201), 2026-10-25: resource lines retain explicit gauge
values and widths until drawing. Empty cells use `#2b3a49` / ANSI gray, filled
cells use the 70/90 percent thresholds, and selected/modal backgrounds survive.
Automated coverage checks all six resource renderers, missing/nonfinite values,
fractional cells, literal block text and clipping in both color modes.
`make verify` and targeted gopls checks passed. An existing asynchronous
Daemon-control test now waits for capture and log completion instead of assuming
two queued events; redraw events could otherwise race the second completion.
Live checks used the rebuilt binary with isolated metadata: 150×38 truecolor
and 80×24 ANSI System/Host and create views, navigation, cancellation and resize.
The create fit showed 1/2 CPUs and fractional 1.0/3.8 GiB memory with distinct
fill/track cells; the default 4 GiB request showed the expected full coral gauge.
Captures were visually inspected; no VM was created. The owned frontend, daemon,
tmux server, temporary store, mount and captures were removed. Spinner timing and the
previous whole-design/native qualification items remain open.


Configurable-key checkpoint (ADR 200), 2026-10-25: a strict immutable registry
now drives manager/detail commands, every overlay, embedded focus, Help,
palette and rendered controls. Settings inspection/update/reset preserve
comments and unknown data; invalid startup uses defaults, invalid refresh keeps
the last valid map, and physical press/repeat/release ownership survives reload.
The original key event reaches text and terminal owners without rewriting.

Regressions cover parser aliases/case/Unicode and alternate physical codes,
overlapping contexts, required/disabled actions, immutable copies, concurrent
conflicting writes, recovery, captured destructive review targets, menus/forms,
embedded payload, pointer cancellation and effective hints. The unshifted
alternate-code regression failed before its fix. Targeted gopls checks are clean.
Final `make fmt build verify test-race test-integration` completed with exit 0:
core unit 94.930s, core race 128.328s and CLI integration 18.330s, plus format,
lint, Vaxis and normal VCS-stamped builds. Focused final key tests passed
(0.907s); the full gates include the final Help-context hint regression.

Live Linux/tmux checks used an isolated metadata fixture and real host shells at
80×24 ANSI and 150×38 truecolor, with captured screens rendered and inspected.
Custom Delete/Save/Cancel/view/focus/host/guest/previous-tab mappings worked;
old keys became inactive. Template/environment/command editors, directory and
template pickers, chooser, palette, Help search, bracketed paste and a pointer
Cancel were exercised. A disabled Delete shortcut still opened its review from
the palette. Reviews were canceled and no VM was created or started.

External valid edits applied on System refresh. A conflicting edit retained the
last valid binding and recorded its diagnostic; restart used safe defaults.
Single/all reset preserved comments and unrelated settings. A raw host shell
ran a command and detached with Ctrl+]; the same session then worked embedded.
A raw input probe received F6 (`ESC [17~`), Alt+Left/Right (`ESC [1;3D/C`) and
ordinary text, while configured Ctrl+F6 changed focus without entering the probe.
Final rebuilt frontends verified expanded host/guest controls and the remapped
Ports hint at both sizes. This does not substitute for native terminal protocol
and macOS qualification in the whole-design matrix.

Owned shells/frontends and the daemon were stopped, a privileged process audit
found no owned survivors, and the 32-MiB socket/store tmpfs was unmounted.
All owned captures, stores, scripts, logs and Go/integration scratch were removed.
Removed 47 new inactive exact-content inputrc files, preserving 1,015 older or
nonmatching files and the active INPUTRC reference. No task commands entered the
user’s guest shell history. Baseline VM, store and tool state were preserved.
The earlier `tmp/countq/tmux` virtiofs entry still requires host-side removal;
that separate count-stage cleanup remains open in TODO #57.

Count wording checkpoint, 2026-10-25: shared explicit singular/plural forms
now cover fleet/group counts, captured batch titles and deletion prompts,
retained batch display labels, Activity node/phase progress, palette targets and
sessions, Ports, Doctor/System and template/environment usage. A one-node review
also describes this node's sessions, VM and disk in the singular. This is a
presentation change with no architecture or schema change.

Existing manager and batch gates passed (17.497s and 4.445s); after the final
singular impact wording, format/build and batch checks passed again (4.693s).
Final manager and confirmation checks passed (19.409s and 4.426s). The existing
System render test now expects the corrected `1 port degraded` wording.
Targeted gopls found no errors. Final `make build verify test-race
test-integration` passed with exit 0: core unit 93.928s, core race 126.776s and
CLI integration 17.651s, plus format, lint, Vaxis and normal VCS-stamped builds.

Live checks used disposable metadata targets without creating or booting a VM.
One/two-node fleet and directory counts, marked deletion titles and exact-count
prompts passed at 80×24 ANSI and 150×38. Final singular impact text passed at
80×24/150×38 truecolor. Screens were captured from the running TUI and rendered
for visual inspection. Both reviews were canceled; no lifecycle receipt was
admitted and neither fixture was deleted by a review.

Cleanup removed both metadata fixtures, stores, workspace, browser scratch,
captures, scripts, logs and completed Go/integration scratch. The isolated
16-MiB socket tmpfs was unmounted; a privileged audit found no owned process
survivors. Removed 48 new inactive exact-content inputrc files, preserving 1,015
older files and the active user's INPUTRC. Removed only the owned test-launch
line from guest shell history. The baseline VM disk hash is unchanged from the
ADR 199 checkpoint. One cleanup item remains explicit in TODO #57:
`tmp/countq/tmux`, created by the failed first tmux launch on virtiofs, returns
EOPNOTSUPP for guest stat/unlink/rename and requires host-side removal. The user
has been asked to remove that one entry. This count checkpoint is not complete
until it is gone; all other owned verification artifacts have been removed.

ADR 199 checkpoint, 2026-10-25: a real no-frontend audit reproduced the orphaned
host terminal: its node was deleted but the terminal ID, PID and exported state
survived. Deletion now removes the complete node-owned session set in the daemon
before receipt finalization. Runtime failure preserves sessions; a committed
tombstone still requires cleanup when the later node-event write fails. Terminal
opens resolve canonical identity and recheck it under the removal mutex before
publication. Cold restoration refuses deleted targets. Versions remain 29/2/6/7.

Regression tests failed first on the original implementation, including single
and partial-batch cleanup, post-commit event failure, session-persistence errors,
late publication, caller exit/ownership and cold restoration. The focused race
suite then passed (2.216s), also covering slug reuse and canonical session targets.
Targeted gopls found no errors; existing modernization suggestions in service.go
are outside this change. `make build verify test-race test-integration` passed
with exit 0: core unit 97.508s, core race 126.840s and CLI integration 17.712s,
plus format, lint, Vaxis and both normal VCS-stamped builds.

The rebuilt binary passed the new QA Flow 7 no-frontend subsection using isolated
metadata and a real managed host shell, with no VM created or booted. The original
orphan exited on shutdown; its stale saved intent was refused at cold restart.
Two fresh disposable metadata targets then verified successful deletion, actual
terminal/PID removal, retained host workspace/sentinel, refusal to open a deleted
canonical target, and empty persisted/restored sessions. A deliberately failed
provider list command prevented the second deletion and preserved its terminal,
PID and exported variable; removing that fault and explicitly retrying closed
the shell. No frontend was opened at any point in this audit.

Cleanup stopped the isolated daemon and removed all three metadata fixtures,
workspace/sentinel, provider wrapper, stores, captures, runners and logs under
`tmp/bt`, plus completed Go/integration scratch. A privileged command/environment
audit found no owned process survivors. Removed 25 new inactive exact-content
inputrc files; preserved 1,015 older files and the active user's INPUTRC. No VM
or mount was created. The baseline VM disk SHA-256 remained
`a717dba3fd2c2dfe699262623aa2c6b5147955ea4bca46c8da4dfae8ba42913c`.
The full 26-artboard/native qualification remains open.

ADR 198 checkpoint, 2026-10-25: marked and directory palette actions now admit
one captured batch to the daemon. Up to four ordinary children execute
concurrently within the global sixteen-worker limit. Queued targets remain
reserved, failure does not stop siblings, root/child cancellation has explicit
scope, and recovery retains completed outcomes without replaying uncertain
work. The responsive review includes exact bulk-delete counts, current identity
and action checks, session/resource evidence, scrolling, mouse and size guards.
Fleet rows expose reservations; Activity opens retained per-target outcomes and
counts progress in nodes. Ordinary/session/renderer/handoff versions are **29/2/6/7**.

The final focused race suite passed (6.825s), covering atomic admission, captured
identity, requester disconnect with queued work, partial failures, root/child
cancellation, global limits, recovery, bounded summaries, review guards,
validation retention, released-delete recovery and session-impact wording.
Final formatting, lint, unit and Vaxis checks passed (core unit 92.805s).
The build encountered a transient Git VCS-status error; normal Git queries
succeeded, and `make build test-race test-integration` then passed with VCS
stamping enabled (core race 125.380s, CLI integration 17.736s, exit 0).
Earlier complete gate runs also passed; an incomplete interrupted race run was
not counted. Targeted `gopls` found no errors in the changed logic; the shared
dialog file retained its existing modernization suggestions. `go doc` covered
cancellation and SSH public-key parsing.

Live checks used an isolated 448 MiB `tmp/bq` tmpfs, separate metadata/Lima
homes, two real 1-vCPU/768-MiB Linux/TCG guests and a later stopped deletion
fixture. Wide 150×38 truecolor and compact 80×24 ANSI start/stop/restart/delete
reviews were inspected. Hidden marked targets stayed captured; wrong or
space-padded deletion counts, pasted counts and unreadable 60×14/40×10 screens
did not admit work. Esc discarded reviews; scrolling, mouse and keyboard
submission worked. Stopped targets correctly showed zero resource release.

The first fresh-home start exposed Lima 2.1's SSH-key initialization race: one
child failed while its sibling reached ready, with both outcomes retained after
requester exit. The provider initialization gate now protects only key creation.
Both starts in another fresh Lima home then succeeded with overlapping execution
and an unchanged shared key after their requester exited. Another frontend
observed their child phases and root outcomes.

The stop batch's two operations overlapped for about 32 seconds. Both guest
sessions ended, allocation returned to zero, and host terminal/PID 2385335 and
its exported state survived. A new startup batch was canceled through Activity
while both QEMU processes were live. Its root remained canceling during Lima's
180-second shutdown wait, then retained two canceled outcomes with both VMs
stopped. An injected provider deletion failure for alpha still allowed beta's
deletion; Activity retained both outcomes after beta disappeared.

Regression-first fixes address findings from these runs. Form errors now survive
non-editing events and clear when the field changes. Fresh revision-matched,
unclaimed evidence with no reservation exposes a new deletion review for an
incomplete deletion; the frontend never rewrites lifecycle state. The rebuilt
TUI retried alpha successfully. Deletion was also found to close node-owned host
sessions, so its review now distinguishes this from stop/restart host survival.
A final mouse-confirmed deletion of a stopped fixture verified the corrected
warning, host terminal/PID closure, preserved host workspace marker, and retained
root/child outcomes after every node was deleted. These deletion checks had a
frontend present; the subsequent no-frontend audit and fix are recorded in the
ADR 199 checkpoint above.

Cleanup removed all three node fixtures and every owned VM, terminal, frontend,
daemon and tmux server. A privileged read-only command/environment audit found
no fixture survivors. The baseline disk SHA256 remained
`a717dba3fd2c2dfe699262623aa2c6b5147955ea4bca46c8da4dfae8ba42913c`.
Unmounting/removing `tmp/bq` discarded its metadata, overlays, captures, scripts
and sockets. Two empty directories from an early failed browser capture were
removed. Cleanup removed 98 new inactive exact-content INPUTRC files, preserving
1,015 older files and the active reference; Go/integration scratch was removed.
Existing baseline shells, tmux and the 64/16-MiB mounts remained intact. Owned
logs and gate runners were removed after recording this evidence.

The full design remains incomplete. TODO #57 retains the no-frontend teardown
audit, singular count wording, the previously observed transient startup header
corruption, Attached presentation and final whole-design/native qualification.

ADR 197 checkpoint, 2026-10-25: the clone review suggests a globally available
name, preserves edits during asynchronous name loading, shows observed source
and guest/host impact, and supplies a keyboard/mouse optional-start checkbox.
The source identity stays captured. Ctrl+s submits; Esc cancels. Optional startup
belongs to one daemon receipt and begins after source restoration. Cancellation
tracks the child separately, while failures retain a committed child. Startup
projections switch from the source to the child only when startup begins.
Ordinary/session/renderer/handoff versions are now **28/2/6/7**.

The final focused clone race suite passed (8.220s), as did the existing
confirmation race checks (5.714s). Full
`make fmt verify test-race test-integration` passed initially (core unit 89.076s,
core race 122.723s, CLI integration 28.347s). After correcting stale validation
text when an asynchronous suggested name arrives, the final full log records
core unit 109.433s, core race 151.198s and CLI integration 19.943s, with formatting,
lint, Vaxis and both binary builds successful and integration cleanup complete.
The final run's long-lived tool observer returned 143 despite the completed log;
no test or make process remained. A subsequent test-only `slices.Clone`
modernization passed the focused race run above. `go doc` covered cancellation
and text input; final `gopls` checks reported no new-file diagnostics.
Tests cover source-before-child ordering, source/child cancellation boundaries,
restoration/start failures, child reservations and identity, observer disconnect,
bounded summary/Activity/fleet projections, CLI opt-in, name scope, stale results,
keyboard event lifecycles, captured source, mouse and unreadable-size refusal.

Live qualification used an isolated `tmp/cq` home and Lima root. Wide 150×38
truecolor and compact 80×24 ANSI reviews were inspected. A name in another
directory caused the suggestion to skip to `clone-qa-3`; invalid/occupied names,
pasted shortcuts and 60×14/40×10 screens did not admit work. Esc discarded the
checkbox/input. A real stopped clone preserved the parent ID, frozen
configuration and workspace values and stayed stopped. CLI `node clone --start`
then reached ready from a stopped source, leaving that source stopped.

A running-source TUI clone/start was submitted with the mouse; the requesting
TUI exited while a second frontend observed the same daemon receipt. The source
stopped for copying and restarted before child boot. Its guest shell ended;
host terminal identity, PID 2313978 and exported state survived. Activity's K
action canceled child startup. Lima's QMP shutdown waited 180 seconds before
stopping the child; the final receipt was canceled, the child stopped and the
source still running. Later starting the retained child reached ready, and a
guest shell read the source's guest-only `CQ-DISK-MARKER` file. Parent identity,
frozen configuration, resources, workspace mode and bootstrap state matched.
The source Overview stayed running during child startup. An initial ANSI header
retained RGB/query-like cells until a resize repainted it; the repainted capture
and modal used ANSI colors. This unattributed startup issue remains in TODO #57.

Cleanup deleted all five fixture node records and every owned VM, closed the
host/guest terminals, stopped the daemon/frontends/tmux server and preserved the
host workspace marker until teardown. A privileged read-only process audit of
command lines and environments found no fixture survivors. The baseline disk's
SHA256 remained
`a717dba3fd2c2dfe699262623aa2c6b5147955ea4bca46c8da4dfae8ba42913c`.
Unmounting/removing `tmp/cq` removed all overlays, metadata, captures, scripts
and socket state. Cleanup removed 46 new inactive exact-content INPUTRC files,
preserving 1,015 older files and the active process reference; test work
directories and owned logs were removed. Existing baseline resources remained.
The full design, including durable parallel batches and final native/artboard
qualification, remains unfinished.

A live read through Claude Code in this VM again returned HTTP 200, all 49
published files, the same canvas title and version. The retrieved canvas and
Main artboard hashes matched the committed references; the tool's two temporary
copies were removed after checking.

ADR 196 checkpoint, 2026-10-25: single-node delete
and stop now use a content-sized themed review with a fixed input/action area,
wrapped scrollable evidence and dimmed manager context. Delete requires the
untrimmed exact slug; both reviews retain canonical identity and revalidate
availability before mutation. Esc discards confirmation input immediately.
Stop distinguishes guest/host impact, exposes an exact captured attachment and
persists an edited idle preference only on submission. Its asynchronous save
revalidates the target before stopping. Completion retains unaffected host tabs.
No daemon/storage/worker protocol change is needed; versions remain 27/2/6/7.

Focused regressions cover exact text, canonical identity, renamed/removed/
in-flight/disconnected targets, retained errors, paste/release/repeat gating,
host versus guest activity, unclassified kinds, ended attachment targets,
preference persistence/cancellation and asynchronous revalidation, compact
scrolling, mouse controls, small-view refusal and host-tab retention. `go doc`
checked Vaxis text input and `gopls` found no new-file diagnostics. The existing
lifecycle test now supplies actual typed confirmation input instead of directly
calling its callback with only a value map.

The rebuilt TUI was inspected at 150×38 truecolor and 80×24 ANSI. Real copied
and mounted fixtures showed their disk/workspace consequences and port data.
Partial and space-padded names were refused; pasted exact text enabled deletion
without submitting. Esc canceled immediately. An exact-name review at 60×14
refused Enter until restored. Mouse deletion removed the copy fixture's Lima
directory/disk and retained a host marker file.

The real mounted guest booted in about five minutes under TCG. Twelve guest
sessions and one host exercised the list; title/progress metadata became visible
once their SSH shells were ready. Attachment retained guest PID 2437 and its
exported state; Ctrl+] returned to the same review. Cancellation kept all thirteen
sessions. Closing the captured guest then pressing x refused replacement and
left the remaining twelve sessions intact. The wide full list and compact
scrolling exposed working titles, age, actual 1-vCPU/1-GiB allocation release,
retained disk/workspace and host survival. Canceling a toggled checkbox left
`always` unchanged. Suspending only the fixture daemon refused submission;
after recovery, mouse toggling and submission saved `busy`. Stop ended all eleven
remaining guests while host terminal identity, PID 2252867 and exported state
survived. A second real start (273.71s) followed by host-only working activity
skipped the busy review and stopped the guest directly. Both stops took about
30.5s. The latest rebuilt frontend reattached that same surviving host process
and showed matching confirmed-stop evidence in the stopped-node delete review.

The focused race run passed (5.268s), including the added unclassified-kind
regression; both binaries built and `gopls` checks were clear. The first full
format/lint/unit/Vaxis/build/race/integration pass succeeded (core unit 86.572s,
core race 118.636s, CLI integration 17.821s). Cleanup deleted both fixture
nodes/VMs, closed all shells, stopped
the owned daemon and both frontends, and removed their tmux server. Command and
environment process audits found no owned survivors. The read-only baseline's
SHA256 remained `a717dba3fd2c2dfe699262623aa2c6b5147955ea4bca46c8da4dfae8ba42913c`.
Unmounting/removing `tmp/dq` removed the overlays, metadata, scripts, captures,
host marker and socket state. Existing baseline resources were preserved.

Review then found an additional minimum-size edge: the manager does not draw
overlays below 60×14, so the modal's previous size flag alone could authorize a
hidden action. A failing regression reproduced this for both delete and stop.
Activation now checks the current root size and shared manager minimums, as well
as the modal's own required height. Focused race coverage passed again (5.248s).
The rebuilt app was exercised with a separate metadata-only fixture at 60×14,
40×10, 59×24 and 80×13. Enter created no operation receipts; returning to 80×24
retained the exact input and Esc canceled. Screenshots were inspected, the owned
frontend/daemon stopped, process absence verified and `tmp/ds` unmounted/removed.
Final gopls reported only existing modernization hints in `tui_render.go`.
The final `make fmt verify test-race test-integration` run passed after this
correction: core unit 87.648s, core race 118.575s and CLI integration 17.650s,
with formatting, lint, Vaxis tests and both builds successful. Cleanup removed
47 newly created inactive exact-content INPUTRC files, preserving 1,015 older,
active or other files; active-process references were checked with read-only
privileged inspection where the baseline PAM/sshd processes hid their
environments. There were no new Lima cache files. Test work directories and
owned verification logs were removed. All 26 retained artboards and the canvas
were rechecked against the manifest hashes.

Clone/start review, durable parallel batch review/ownership and the full native
and whole-design QA matrix remain in TODO #57. This checkpoint does not complete
the Dialogs artboard or the accepted redesign.

ADR 195 checkpoint, 2026-10-25: Shell detail is now explicitly gated by embedded
mode, including its empty state. The themed session strip keeps the active tab
visible through overflow and resizing. Host accents, titles, progress and bell
acknowledgement share existing session metadata. The framed terminal reserves
its own content rectangle; startup, resize and pointer translation use the same
geometry. Focus controls sit outside guest cells. Mouse tabs retain node/session/
terminal identities, while detail tabs and the Nodes breadcrumb are clickable.
Known daemon disconnection refuses new guest and host launches. The footer
advertises only current, authorized, ready live routes. Protocols remain 27/2/6/7.

The focused race target passed (14.954s), including the opt-in, empty-state,
overflow, resize, closed-target mouse, disconnected launch, forwarding freshness,
manager, focus/key lifecycle, host and background-operation regressions. Existing
fixtures now explicitly enable embedded mode where they test its behavior.
`go doc` checked the window and canonical node-target APIs; `gopls` reported no
errors after replacing an accidental production reference to a test helper.
Both production binaries built successfully before live verification.

The rebuilt application was visually inspected and operated at 150×38 and
80×24 in truecolor/ANSI. Thirteen host tabs exercised overflow, active identity,
mouse arrows and keyboard navigation; mouse creation added a fourteenth. Resize
through 60×14 and the 40×10 minimum-size notice retained the selected session.
The live focus label, detail tabs and breadcrumb worked. After switching between
frontends, an explicit host focus report reclaimed the existing resize lease;
the shell then reported the exact 146×25 content area. A raw mouse-reporting
program received (1,1) and (146,25) at the first/last framed cells. Clearing its
screen did not erase the frame. A raw keyboard program received Alt-left/right,
F7 and ordinary action characters unchanged; F6 returned to the TUI without
entering the payload. Search found two expected matches, and a drag copied the
exact selected string through OSC 52 into the isolated tmux clipboard. An
inactive host tab displayed actual OSC title/progress (42%) and an unread bell.

Settings opt-out hid Shell and refused F6. Opting in again preserved all 14
session IDs; host PID 2225032 retained its variable. The disposable guest reached
running with completed bootstrap in 4m53s. Its embedded shell (PID 3198) accepted
commands, retained its variable and reported 25 rows/146 columns. A real HTTP
server on guest port 5173 appeared as the observed forwarded address; an HTTP
request through that address succeeded. Stopping the service removed the live
route. Suspending only the fixture daemon suppressed stale forwarding, dimmed
both launch controls and refused their actions. Recovery retained the same
15 sessions. After the guest stopped, the empty view explained guest
unavailability; a new host shell still ran in the exact node directory and
exited cleanly.

All owned terminals, guest, daemon and frontends were stopped. A command and
environment process audit found no remaining fixture processes. The read-only
base image hash was unchanged; the owned 256 MiB tmpfs was unmounted, removing
its overlay, metadata, screenshots, clipboard state and verification scripts.
Interactive Claude Code in this VM again read the live artifact with its
Artifact tool (HTTP 200, 49 files); its canvas matched the retained version.
The Claude session exited normally and its project-local scratch was removed.
Full `make verify test-race test-integration` passed with exit 0 after cleanup:
core unit 86.083s, core race 116.810s and CLI integration 17.685s, plus formatting,
lint, Vaxis checks and both builds. Lint identified a now-unused split-pane
layout helper; it and its obsolete layout-only assertions were removed, with
current detail/focus and framed-geometry tests retained. Final `gopls` reported
only existing modernization suggestions. The final owned-process audit was
empty. Removed 39 new inactive exact INPUTRC files; preserved 1015
older/active/other files. No new Lima cache files were found. Test work directories
and all 12 stage logs were removed; existing baseline cleanup debt is unchanged.

The Attached artboard's presentation and the full native/whole-design QA matrix
remain open in TODO #57. This checkpoint does not complete the full design.

ADR 194 checkpoint, 2026-10-25: the dedicated shell chooser now separates new
sessions from exact existing attachments, shows launcher/readiness availability,
current default and observed session activity/age/bells, and requires Settings
opt-in for embedded mode. All new choices create fresh sessions; ordinary `x`
retains reuse. Numeric shortcuts remain bound to their opening identities after
closure or reordering. Mouse targets carry IDs, and saved-default callbacks
revalidate the captured node instead of consulting current selection. The
palette shares those lookup/navigation helpers. Protocols remain 27/2/6/7.

The focused race target passed (3.398s), including launcher/attach/palette
regressions, scrolling beyond nine sessions, stale clicks, reordered/ended
shortcuts, paste/release/repeat guards, real-RPC fresh creation, removed targets
and navigation changes during asynchronous preference saves. Embedded creation
also verifies captured-node navigation. `go doc` and `gopls` checks passed.
Full `make verify test-race test-integration` passed: core unit 84.724s,
core race 116.744s and CLI integration 17.642s, including formatting, lint,
Vaxis checks and both builds. An initial plain-pipe runner was terminated
before unit results; it was not counted as a pass. The complete tracked PTY
run finished with exit 0. Afterward the owned-process audit was empty, no new
Lima cache files existed, and 38 new inactive exact INPUTRC files were removed;
1,015 older/active/other files were preserved. Test work directories and logs
were removed. Existing baseline cleanup debt remains in TODO #57.

The rebuilt TUI was visually inspected and operated at 150×38 and 80×24 in
truecolor/ANSI. Resize through 60×14 and 40×10 returned to the same choice.
Thirteen host sessions exercised compact scrolling, Home/End, PageDown, wheel
and mouse controls; this found and fixed orphaned context lines and a hidden
first group heading. Actual OSC title/progress/bell evidence rendered without
inferring an agent identity or retaining historical bell badges in the title.
Pasted action text remained inert. A closed numbered session and a stale mouse
target refused attachment; a daemon RPC reorder preserved the original numeric
target. Deleting a captured metadata-only node refused a subsequent host launch.

A real disposable Lima guest reached running with completed bootstrap. During
boot the chooser blocked guest choices and allowed a host shell in the exact
node directory. Host PID 2184019 and its variable survived detach/reattach; guest
PID 2453 and its variable also survived. Fresh guest attachment increased the
session count; `x` reused an existing guest. Mouse attachment and `exit` removed
only the selected terminal. A project-local launcher wrapper recorded literal
attach arguments, the correct metadata home and directory, and opened an actual
attached terminal in an isolated tmux window. This qualifies the launcher
contract here, not native GUI terminal integration. Real tmux launch also passed.
Shift+W/T and Shift+Enter saved defaults; reopening retained attach. A missing
launcher refused Shift+W without creating a session or changing the default.
A separate ANSI frontend without TMUX showed its explicit unavailable reason.
Settings opt-in enabled a fresh embedded tab, guest input and F6 manager return.
Pausing only the fixture daemon produced unavailable choices and suppressed
stale activity; host launch was refused and resuming recovered the connection.

After stopping the guest, its host-shell choice remained usable in the node's
exact directory and exited cleanly. All fixture terminals, frontends, guest,
daemon and tmux processes were stopped; a command/environment process audit
found none remaining. The read-only backing image hash was unchanged. The owned
256 MiB tmpfs was unmounted, removing its disk overlay, metadata, screenshots,
launcher wrapper, socket state and scripts. No existing baseline fixture was
changed. Full verification runs only after this cleanup.

Interactive Claude Code in this VM independently returned the live artifact
through its Artifact tool (HTTP 200, 49 published files, 26 artboards); its
canvas matched the retained design byte-for-byte. The verification scratch
copy was removed and the Claude session exited normally.

ADR 193 checkpoint, 2026-10-25: the palette now groups live scoped commands,
counts fuzzy results, highlights actual matching spans and shows lifecycle,
route and daemon context. Wide rows keep shortcuts aligned; compact rows put
context below the label. The modal sizes to short results and scrolls longer
lists. Empty queries show up to 20 available recent commands from this frontend
session. Per-node commands, confirmation callbacks and mouse targets retain
canonical IDs; a removed selected result becomes unavailable. Directory reviews
capture exact members and Start/Retry choices and preserve unrelated marks.
Existing execution owners and protocols 27/2/6/7 are unchanged.

Focused race checks passed after the live sizing correction (6.782s). Go API
inspection and gopls passed; the shared-operation file's existing modernization
suggestions are unrelated. Live Linux checks at 150×38 and 80×24 covered grouped
matching, paste, completion, scrolling, no matches, frontend-session recents,
mouse result/footer controls, resize through the minimum-size notice, ANSI and
truecolor, node/bootstrap-log/system navigation and the captured shell chooser.
An actual host shell opened in the exact selected node directory and exited.
Removing a result through another daemon client retained an unavailable command;
Enter refused it. Removing a confirmed target before submit also refused it.

The disposable QEMU/TCG guest failed its second bootstrap command. Directory
Retry preserved the first command's one attempt, reran the second successfully
at attempt two and ran validation once. A node added after review had no
bootstrap attempts; an unrelated marked peer stayed marked. The guest start
continued after both frontends exited and reopened. This is Linux terminal
qualification; final native and whole-design qualification remain open.
An actual palette Restart preserved bootstrap attempts 1/2 and reran validation
at attempt two. With confirmation set to always, both Stop and Restart reviews
named the explicit node and could be canceled; confirmed Stop shut down that
guest. Removing a captured batch member before submission refused the whole
review and left the first guest stopped. Suspending the disposable daemon
retained a clearly unavailable status in the palette; resuming it recovered.
The terminal inventory was empty, every owned process exited and the original
backing image hash stayed unchanged. The owned guest overlay, metadata, captures,
tmux sockets and tmpfs mount were removed before starting the complete gates.
Final `make verify test-race test-integration` passed: core unit 83.955s,
core race 115.762s and CLI integration 17.647s, plus format, lint, Vaxis and
both builds. The final process audit found no owned verification processes.
Cleanup removed 23 new inactive inputrc files with the exact generated contents;
1,015 older/other files and all active references were preserved. No new Lima
cache files were created. Project test scratch and check logs were removed.
The interactive Claude source-check session also exited, and its downloaded
scratch canvas was removed. This checkpoint does not complete the overall redesign.

ADR 192 checkpoint, 2026-10-25: help now has seven contextual groups, four/two/one
responsive columns, independent scrolling, all-context search and read-only
keyboard/mouse handling. The opening view leads; the focused column is
highlighted over a dimmed manager. Descriptions and key names wrap at word
boundaries, with grapheme-preserving fallback for overlong tokens. Overview,
Ports, Activity, System and form/shell focus restrictions are represented.
The palette retains its executable model separately. Protocols remain 27/2/6/7.

Final `make verify test-race test-integration` passed after the wrapping fixes:
core unit 84.110s, core race 114.864s and CLI integration 17.706s, plus format,
lint, Vaxis and both builds. The focused help/palette/System regression target
passed (0.766s), and final gopls diagnostics passed. Go API inspection used the
local Vaxis/text-input documentation. No code changed after the final gates
started.

The final rebuilt Linux frontends were visually inspected and operated at
150×38 and 80×24, in truecolor and ANSI modes. The Flow 7 help checks covered
every opening context, all groups, final Spec/System bindings, forward/reverse
group navigation, scrolling, mouse focus/wheel/search/close, typed and bracketed
paste search, empty results, inert Enter/lifecycle/shell keys, palette opening
and subsequent palette navigation. Resize passed through 110×30, 40×10 and
back to both ordinary sizes. Terminal inventory stayed empty. Visual inspection
caught split words and key names; these were fixed, rebuilt and rechecked.
The fixture used a stopped metadata-only node; no guest was started. Both
frontends, their tmux server and the isolated daemon exited. A process audit
found no owned process before the entire fixture/capture tmpfs was unmounted
and removed. Native platform/full-design qualification and configurable keys
remain open, as recorded in TODO #57 and roadmap #9.
The final audit removed 46 new inactive inputrc files with the exact generated
contents; older, active and other files were preserved. No owned verification
process remained. The new Claude artifact-check scratch directory was also
removed after its session exited.

Live source recheck, 2026-10-25: interactive Claude Code in this VM successfully
used its Artifact tool to read the canonical artifact and `project/canvas.json`.
The live title, version `1792822420-f77f`, 26-artboard count and canvas SHA-256
match `reference/SOURCE.md`. A separate local check verified all 27 reference
file hashes. This recheck did not compare every artboard against a fresh live
download. The noninteractive invocation lacked the Artifact tool; use the
interactive terminal path when another live check is needed.

ADR 191 checkpoint, 2026-10-25: Overview now has five responsive panels,
independent focus/scrolling, stable session and route selections, exact-session
attach/end actions, fresh guest/host creation, daemon-derived ages and six
bounded recent records. The dashboard keeps its three-record preview; Overview
no longer loads a complete event log. Missing/stale observations remain explicit.
Tab-change pushes now refresh daemon order asynchronously; generation fences
and membership-preserving application keep later creations and closures safe.
Ordinary/session/renderer/handoff protocols remain 27/2/6/7.

Final `make verify test-race test-integration` passed after the visual and
ordering fixes: core unit 84.625s, race 113.572s, CLI integration 17.734s,
plus format, lint, Vaxis and both builds. Focused race checks passed (8.067s);
Go API inspection and gopls passed. A final process audit found no owned
verification processes. No new Lima cache files were created. The final audit
removed 46 additional inactive inputrc files created since the preceding
checkpoint; older files and every active INPUTRC were preserved.

Live Linux QEMU/TCG verification used two rebuilt frontends at 150×38 and 80×24.
It covered Starting, observed CPU/memory/disk, unknown agent checks, template
change indication, six selected-node events, all panel navigation/links,
mouse row selection/wheel movement, resize, actual host/guest creation and
exact attachment (retained variable and PID). Cancel/confirm ended only the
captured session. A second daemon client reordered tabs; the first live run
exposed a redraw-only cache bug, now fixed and repeated successfully. An
externally closed selection refused attachment without creating/reusing another
session. Frontend reopen and live daemon update retained terminal IDs and
creation times. Daemon suspension refused stale routing, and node stop refused
retained routing. Two discovered guest HTTP services returned HTTP 200;
selected-port opening was checked with a captured host launcher, and OSC 52
copy delivered the same URL to tmux's clipboard buffer. This is Linux terminal
qualification, not native macOS browser/desktop clipboard qualification.

Visual inspection caught and fixed low-contrast inactive titles, missing
resource colors, overlapping ended-session heading text and an inappropriate
guest-shell hint on stopped nodes. The final stopped empty state was run and
inspected after rebuilding. The original backing image hash stayed unchanged.
All owned sessions, processes, guest overlay, metadata, temporary clients,
captures, workspace and tmpfs mount were removed; two newly created inactive
inputrc files with the exact expected contents were removed. Older cleanup debt
in TODO.md remains untouched. Full design/native qualification and real clone
provenance qualification remain open in TODO #57.


ADR 190 checkpoint, 2026-10-25: the selected preview and Overview now project
separate frozen agent checks, including failed/pending, inherited, missing and
custom-profile evidence. Ordinary protocol 27 carries the bounded projection;
session 2, renderer 6 and handoff 7 are unchanged. Overview refreshes its selected
node at both widths and cancels loading on other detail tabs. Compact panels
wrap agent/result pairs, preserving a result when a profile name is long.

The focused race suite passed after the loader and wrapping regressions
(5.490s). Final `make verify test-race test-integration` passed (core unit
82.058s, race 112.998s, CLI integration 17.721s), including format, lint,
Vaxis and both builds. Go API inspection and gopls passed.

Two rebuilt Linux frontends were operated and visually inspected at 150×38
and 80×24 against a disposable QEMU/TCG guest. The built-in Codex check failed
with exit 127 while the later profile check and Claude check remained pending;
the preview retained that failure. Real Codex 0.164.0 and Claude Code 2.1.310
binaries then executed as the guest login user. TUI retry completed both
bootstrap checks and the final profile validation with exit zero. Both views
showed the separate results and login-user label. Isolated metadata fixtures
covered legacy/missing records, custom profile checks, inherited checks, a
locally revalidated agent, and selection between a real node and a metadata-only
child. Overview updated while remaining open. Daemon suspension labeled stale
checks in both clients; recovery and live update preserved the execution
records. Tab navigation, resize and stopped-node retained results passed.
A final isolated metadata check edited a frozen command without a matching
execution record: the wide preview and compact Overview removed success,
left the stored evidence unchanged, and restored the marks when matching
records returned. Its rendered unknown state was visually inspected.

All owned guest/TUI/daemon processes, overlays, fixtures, copied binaries,
captures, stores and the tmpfs were removed. The existing backing image's
checksum was unchanged. These Linux checks do not complete the full Overview
resource/session/route design, the other remaining artboards or final native
qualification; the full redesign remains incomplete.
Cleanup also removed 45 inactive, exact-content inputrc files generated since
the previous checkpoint; older files were preserved.

ADR 189 checkpoint, 2026-10-25: the daemon publisher now retains prompt idle
intervals using positive shell integration and accepted PTY input. Ordinary
protocol 26, renderer protocol 6 and handoff protocol 7 include this metadata;
session 2 is unchanged, and importing handoff 6 still preserves Activity state.

Final `make verify test-race test-integration` passed after the live freshness
fix (core unit 85.167s, race 112.371s, CLI integration 17.737s). Focused prompt
race checks passed in 7.395s; the final fleet race regressions passed in 4.929s.
Go API inspection, gopls, format, lint, Vaxis and both binary builds passed.

Two rebuilt Linux frontends were visually inspected and operated at 150×38 and
80×24 with an isolated metadata-only node and real host Bash sessions. The
original interval survived opening/reopening a frontend, renderer restart and
live daemon update; update also preserved the shell PID. Raw, encoded, pasted
and raw-attachment input reset it. Working/paused/unsupported sessions removed
idle claims, and mixed marked targets did not claim all-idle. A renderer command
timeout published explicit stale activity and recovery retained the interval.
Daemon suspension exposed a response-freshness gap; after its regression/fix,
both rebuilt frontends showed unknown during suspension and restored retained
idle on recovery. Raw detach preserved the session, and compact/wide resize
preserved selection. Shells exited and all owned TUIs, daemons, stores, captures,
helper binaries and the tmpfs were removed. Cleanup also removed 46 inactive,
exact-content inputrc files generated by this checkpoint's tests. The full redesign and final
native/artboard qualification remain incomplete.

ADR 188 checkpoint, 2026-10-25: `make verify test-race test-integration`
passed (core unit 81.792s, race 112.109s, CLI integration 17.759s), including
format, lint, Vaxis checks and both builds. Final focused fleet race checks
passed in 4.825s after adding current-successful-check evidence coverage.
Go API inspection and gopls checks completed; gopls reported only the existing
append-to-slices.Clone style suggestion outside the changed test lines.

Two rebuilt Linux frontends at 150×38 and 80×24 were visually inspected and
operated against a disposable QEMU/TCG guest. They retained the same CPU/memory
history, preserved sample gaps and reflected a real 64 MiB memory allocation.
Effective runtime settings and workspace access matched `lima.yaml`; omitting
settings while stopped showed unknowns, and changing access showed read-only.
Creation/auth/Lima identity, observed task detail and stale/recovery labels
passed. The route-open action dispatched the discovered node-qualified URL to
an isolated opener, and actual Chromium loaded the guest page at that URL.
Confirmed stop ended the managed guest terminal, succeeded after 28s, removed
the route, released observed allocation and rendered stop age in both clients.
All owned processes, guests, overlays, stores, captures, tmpfs and 25 inactive
test inputrc files were removed; the existing backing image was unchanged.
Multi-node conflicts, native desktop browser launch, durable idle timing,
complete agent validation and final native/artboard qualification remain open.
The complete redesign remains partial.

ADR 187 checkpoint, 2026-10-25: `make verify test-race test-integration`
passed (core unit 81.167s, race 113.556s, CLI integration 17.780s). After adding
read-only claim/revision fencing for nil-finish interrupted receipts, focused
race checks passed (4.554s), final `make verify` passed (core 80.977s), and gopls
reported no diagnostics. Go API inspection, formatter, lint, Vaxis checks and
both binary builds passed.

The rebuilt Linux TUI was visually inspected and interacted with at 150×38 and
80×24 using three isolated metadata-only nodes and a real host terminal.
Failure step/source/exit/duration, interrupted recovery without a finish time,
live flock precedence, independent recent history after older Activity paging,
rapid selection, incomplete source recovery, hidden host-session impact and
stale/recovered daemon observations passed. Real OSC progress and prompt
signals showed working/error/paused/at prompt; a title alone remained unknown.
All owned processes, terminals, files, captures and the tmpfs were removed,
including 25 inactive inputrc files left by this checkpoint's test runs.
This does not qualify real guest session termination, confirmed stop-age
presentation, live route/resource comparisons or final native/artboard fidelity.
The complete redesign remains partial.

Fleet re-audit against the three home artboards:

| Requirement | Current evidence and remaining work |
|---|---|
| `r` restart, with failed-step retry retained | ADR 185 service/daemon/CLI/TUI restart passes two-guest Linux/TCG checks; full native/artboard qualification remains |
| `o` opens the first forwarded route | ADR 186 opens the selected ready node's first current live HTTP route; unavailable/stale/non-live/TCP guards tested |
| Port filters/counts include discovered forwarding | ADR 186 joins frozen guest ports with discovered routes by node ID; unavailable/stale counts remain explicit |
| Marked-node panel replaces single-node preview | ADRs 186–187 include hidden targets, global release/remaining allocation, guest/host counts and observed working/unknown activity; per-target task labels added (ADR 188); retained prompt idle timing added (ADR 189) |
| Flat filter view includes directory | ADR 186 shows Directory when grouping is absent |
| Wide memory/disk values show used/total | ADR 186 uses observed byte totals and explicit unavailable values; compact stays percentages |
| Compact navigation retains connection and capacity | ADR 186 abbreviates inactive views and preserves connection/global allocation with unknowns |
| Preview includes resolved routes, runtime and recent events | ADRs 186–187 add provider/runtime, observed routes, independent recent events and explicit session activity; selected VM/mount/nesting configuration and CPU/memory trends added (ADR 188); retained prompt idle timing added (ADR 189); full qualification remains |
| Lifecycle rows explain current work and failure | ADRs 186–187 show recorded running phases, causally matched failed steps/duration, confirmed stop age and recovered interrupted state with live-claim precedence |
| Whole-batch caller independence | Individual admitted mutations survive; the sequential frontend queue does not. Persist complete queue/result ownership |

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

- ADR 186 fleet checkpoint, 2026-10-25: `make verify test-race test-integration`
  passes (core unit 84.926s, core race 110.033s, CLI integration 17.700s), along
  with focused fleet/Ports/Doctor/Activity race checks (8.523s), Go API inspection
  and clean gopls diagnostics on the changed projection/rendering files. The
  80–180 column regression caught and fixed a clipped uptime column at 100;
  the full suite caught and fixed lost compact Activity/Doctor badges.
  The rebuilt TUI ran against an isolated 20-node metadata fixture at
  150×38, 120×30, 100×30 and 80×24. ANSI screenshots were visually inspected;
  keyboard and mouse checks covered hidden marks (including no matching rows),
  retained confirmation targets/cancel, all five compact tabs, grouping,
  scrolling, overflow of the marked list, missing-route rejection, daemon
  suspension and recovery. No shells were created; marks and selection survived
  reconnect, and unavailable allocation/routes stayed explicit. The daemon,
  tmux session, fixture metadata, captures, scripts and private tmpfs mount were
  removed. These were metadata-only UI checks, not new live-guest measurements
  or native/artboard qualification. Real discovered-route browser opening,
  resource-release comparisons and the remaining fleet evidence/session flows
  still require the expanded QA.md flow before full qualification.

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

## Global Doctor findings (ADR 179)

Doctor now retains its own snapshot across navigation, runs one bounded check
at a time, queues explicit repair after an active read, and re-checks after
repair before reporting remaining findings. The System header badge includes
report warnings, failed checks and observed global allocation warnings, with
unknown/stale state marked explicitly. Selectable severity rows open wrapped,
scrollable details; mouse controls match keyboard actions. Current settings
are reread without rewriting malformed files during checks.

Focused checks, full verify, race and CLI integration pass; gopls reports no
errors (modernization suggestions only). The first full invocation stopped at
lint; unchecked test-handler errors and two conditional style issues were fixed
before the successful full run. A settings regression caught stale startup
configuration; checks now inspect the current file. A separate regression
ensures a failed first check has readable full details.

Live 150×38/80×24 checks matched the badge against CLI findings, retained it
across global navigation and exercised selection, full wrapped messages,
resize and mouse details/repair. Two unreadable records moved to quarantine
with original node/event bytes preserved. Their resolved warnings left the
badge while completed repair messages remained in Activity. Malformed settings
were reported without rewriting; restoring them cleared the finding. A metadata
read failure retained the old snapshot and added ?, with its full error in
details. Restored permissions and the following host refresh cleared unknown
state. A scoped limactl failure appeared as failed version/list checks and
recovered when the fixture switch was removed. Real inaccessible /dev/kvm
remains a failed check in this VM.

The periodic read-only check discovered a newly added corrupt record after
61.1 seconds while Nodes remained visible. It updated the global badge and
left the original record untouched. Explicit keyboard R then moved that record
with its bytes preserved. All final automated gates pass (core unit 48.596s,
core race 70.326s, CLI integration 16.756s).

The isolated daemon/TUI/tmux stopped, the 64 MiB tmpfs was unmounted, and all
metadata, quarantine, scoped fault wrapper, logs, captures, `tmp/sq` and focused
test scratch were removed. No VM was created. The older stepqa fixture remains
separate cleanup debt. Daemon controls and final full-System/artboard/native
qualification remain open.


ADR 180 / ordinary protocol 20 adds a cached daemon overview independent of
terminal grids and renderer diagnostics. It includes unique logical subscribers,
seat ownership, actual listener/port/lapsed-route counts, telemetry cadence,
platform reclaim observations and the current incarnation's committed-update
time. System refreshes it off the event loop with one bounded request, cancels
on navigation and preserves explicitly stale observations on failure. Complete
errors and sample times remain available through keyboard/mouse details.

Focused tests, full verify/race/integration and gopls pass. Live 150×38/80×24
qualification covered two TUIs, subscriber/seat changes, full details, resize,
view navigation and recovery after suspending the isolated daemon. An existing
CLI live update preserved a host shell PID and variable while the TUI reconnected
and showed the new daemon PID/update time; ordinary restart cleared that time.
The host session, both TUIs, daemon, tmux, metadata, screenshots, tmp/oq and its
tmpfs mount were removed; no VM was created. Forwarding/reclaim error cases have
automated fixture coverage. Daemon action controls, packaged diagnostic capture
and final artboard/native qualification remain open in TODO #57.


ADR 181 / ordinary protocol 21 adds explicit System update/restart/log/diagnostic
controls, shared CLI lifecycle helpers and an installed Go evidence collector.
Update refuses restart fallback; confirmed restart guards the observed epoch.
Independent workers keep evidence access available, fence shutdown completions
and preserve navigation. The build-free skill wrapper delegates to the same
collector. Offline/malformed-settings capture and subsequent first-run metadata
initialization have regression coverage; unknown home content remains rejected.

Live keyboard/mouse/palette checks at 150×38 and 80×24 verified update shell/PID
and variable continuity, restart confirmation/cancellation, respawn and forget,
log reload/missing/error recovery and pager return. Healthy and suspended-daemon
captures retained private evidence, failed probes, one active capture and the
foreground log view; Activity retained the result path. CLI and wrapper capture
preserved PID/seat, and offline capture did not start a daemon. Compact help and
confirmation text were adjusted and rechecked in the running final build.

All fixture processes, host shells, daemon/TUIs/tmux, metadata, bundles, screenshots,
scripts, tmp/cq and its 64 MiB tmpfs were removed. No VM was created. Native macOS
sampling and final native/artboard/modal qualification remain open in TODO #57;
ANSI-to-HTML previews are not native pixel-level qualification. Older stepqa
cleanup debt remains separate.

Final ADR 181 verification passed: core unit 69.380s, core race 100.661s and
CLI integration 20.571s, plus formatting, lint, Vaxis fork checks and builds.
Gopls reported only existing slices.Clone suggestions in unchanged store code.
The final run used Linux signal tracing and a saved exit result (0); its trace
contained no SIGTERM sender outside the traced processes. The untraced launcher
interruption did not recur there and its cause remains unproven. The 937210-byte
trace, log, exit receipt and identified interrupted-run Go scratch were removed.


ADR 183 / ordinary protocol 22 provides cached live Ports rows, optional guest
process names, durable generic-host pins and discovery order, raw TCP Activity,
legacy/held/stopped labels and responsive keyboard/mouse/palette controls.
Two real Ubuntu guests verified named/generic HTTP ownership, pin persistence,
first/newest discovery order, restart while a pinned guest was unavailable,
expiry/recovery and live daemon updates. TCP passed dual-stack binary half-close,
readiness gating during boot and failed bootstrap, TUI retry, overlap and IPv6
bind-conflict rollback/recovery. Running legacy start preserved PID/config;
stopped migration changed only Lima forwarding and retained frozen node settings.
The old Lima relay stalled on the EOF-response probe; the migrated relay passed.
Malformed policy bytes were retained, valid pin/update rejected, full errors
readable, and restoring the backup recovered service. Compact/wide controls,
clipboard/browser dispatch, filtering, navigation and complete overflow details
were exercised. Handoff now freezes the durable claim cut and resumes discovery
on rollback; regressions cover both direct identity changes and a real failed
handoff. Confirmed stops ended existing TCP connections, removed both host
listeners and recorded exact-node Activity removal; the entry opened Ports.
Both guests, daemons, TUI/tmux, overlays, scripts/captures and tmpfs mounts were
removed with tmp/pq. The old stepqa cleanup debt remains separate. Final automated
checks pass after the shutdown guard: core unit 77.674s, core race 106.253s and
CLI integration 20.417s, plus fmt/lint, Vaxis, builds and clean gopls checks.
The rebuilt CLI runs locally. The traced suite exited 0 with no external SIGTERM
sender recorded; verification logs/traces were removed. TODO #57 retains native
terminal/browser and final artboard/cross-view qualification.

ADR 184 replaces the empty Nodes table with scoped actions, actual stored
template resources and cached Doctor findings. It keeps zero-match searches in
the table, preserves the launch directory in all-node creation and provides
read-only re-check with press-only activation. Initial check errors take
precedence over pending state; long diagnostics remain scrollable in both
layouts. Regression tests cover these behaviors, healthy recovery and warnings.

The isolated Linux TUI passed 150×38/80×24 checks for blocked keyboard, palette
and mouse creation, System/Doctor return after Settings, template navigation,
scope toggling, zero-match filtering/clear, paging/wheel and updated resource
defaults. Re-check preserved configuration/environment/settings hashes.
Template-to-create forms retained the launch directory even though the process
cwd differed, in both scoped and all-node views; both forms were canceled.
The final rebuilt compact guide showed the scroll hint and complete findings.
ANSI screenshots were inspected. This VM reports inaccessible `/dev/kvm`, so
the live healthy-host transition and native terminal qualification remain open.
An attempted outside-scope disposable node failed during disk allocation in the
small verification tmpfs and was rolled back; no VM started. All fixture
processes, metadata, captures and tmpfs mounts under `tmp/eq` were removed.

Final ADR 184 checks passed: core unit 76.086s, core race 104.842s and CLI
integration 17.651s, plus formatting, lint, Vaxis and builds. The focused race
suite passed in 1.736s. Gopls reported only existing style suggestions outside
the changed lines. Existing dialog-routing tests now explicitly start with
successful prerequisites. Verification logs and Go scratch were cleaned.

ADR 185 / ordinary protocol 23 adds daemon-owned restart across confirmed stop
and startup under one node lifecycle claim. CLI and TUI share the receipt;
shutdown and boot output are retained once. Completed bootstrap stays frozen,
validation runs again, incomplete setup follows normal start, and an unmarked
failed node retains explicit suffix retry. Detail actions ignore fleet marks.

Two disposable Ubuntu guests exercised the real Lima lifecycle on Linux/aarch64
with TCG and 9p; only creation used private overlays over an unchanged stopped
QA backing disk. At 150×38 and 80×24, canceled confirmations preserved boot and
terminal identities, counters and attempts. Keyboard/palette restart ended the
old daemon-owned guest shell, changed the boot ID, kept each bootstrap counter
and attempt at one and advanced validation. A CLI disappeared while its admitted
receipt remained running; that same receipt completed successfully. Receipts
retained shutdown/boot output and recorded stop finishing before boot began.

An injected stop-command failure left the VM, boot ID and attempts unchanged
with Boot still pending. A real validation failure followed by `r` retried only
validation, retaining the boot ID and bootstrap count. One marked confirmation
restarted both guests sequentially and cleared marks on total success. With two
marks present, detail restart admitted only the open node; the other node's boot
ID, counter and attempts stayed unchanged. Activity `K` canceled during boot.
The reopened frontend retained canceling state until Lima confirmed stop; this
early emulated boot required Lima's own three-minute forced-shutdown fallback.
The final receipt was canceled and both metadata and Lima reported stopped.

Live checks found a clipped compact confirmation and a misleading failed-node
footer. Both now have wide/compact regressions, and the rebuilt frontend was
visually checked again. Cancellation copy now covers startup generally. ANSI
captures were inspected; this does not finish native-terminal or full artboard
qualification. Both guests, daemon, TUI/tmux, overlays, stores, scripts, captures
and temporary mounts under `tmp/rq` were removed. The backing disk hash matched
its pre-test value. Existing stepqa cleanup debt remains separate.

Final ADR 185 checks passed after the live fixes: core unit 82.327s, core race
108.016s and CLI integration 17.708s, plus formatting, lint, Vaxis and builds.
The focused restart race suite passed in 6.661s. Go doc and gopls inspection
covered the service and changed UI paths; only pre-existing style suggestions
appeared in the shared runtime/service files. The final CLI runs locally and
advertises `node restart`. Verification logs and Go scratch were cleaned.
