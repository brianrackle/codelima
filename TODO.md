# TODO

## Open Work

### 55. Qualify the clipboard connection-routing fix on physical hosts

Problem: Codex `/copy` intermittently stopped reaching the host clipboard after
the libghostty-vt overhaul. The user confirmed that a short OSC 52 probe passes
directly in host Ghostty but fails inside CodeLima. A real-socket TUI regression
test reproduced a daemon routing defect: clipboard delivery required the
physical input connection to subscribe, but the TUI subscribes on its separate
event connection. ADR 148 routes to the owning frontend's current subscribed
event connection while preserving the physical input lease and no replay.
The user's additional `copy terminal selection: native result -4` message was
separately reproduced by a plain mouse press/release without a selection.
ADR 149 makes that normal no-value result a quiet no-op, preserving explicit
copy errors and real word-selection copying.
The regression now passes before and after reconnect; daemon tests cover seat
takeover, stale subscriptions and disconnect cleanup. The TUI still delegates
to the outer terminal using OSC 52. No clipboard limit increase was implemented.
`make test-clipboard`, `make verify`, `make test-race`, `make test-integration`,
`make test-package`, gopls diagnostics and running the rebuilt CLI passed on
Linux ARM64. The integration test sends a real shell's OSC 52 output through the
built renderer and daemon and repeats after reconnect. Physical-host clipboard
completion and the remaining QA.md manual flows cannot be established in this
Linux guest.
The maintainer subsequently reported "fixed" and requested merge/release as
v0.3.5. Their reported symptom is resolved; the broader clipboard QA cases
below still need qualification. Release evidence is in
`plans/clipboard_native_agents_release_qa.md`.

Suggested solution: build/install the corrected pair on the host, update the
running daemon, and repeat the guest probe and Codex `/copy`. Run QA.md's
clipboard checks with multiple windows, reconnects, Unicode and near-limit
responses, plus the remaining manual flows on supported hosts; clean disposable
state. If failures remain, trace renderer callbacks, daemon admission, the TUI
event queue and the final OSC 52 write without recording clipboard contents.
Keep larger clipboard limits as a planning item until separately authorized.

Advantages: confirms that the reproduced routing fix restores the user's
clipboard without bypassing the outer terminal. Disadvantages: requires the
user's physical host/terminal setup; socket tests cannot prove host clipboard
completion or all intermittent failure modes.

### 54. Qualify native agent provisioning on real Lima hosts

Problem: native agent provisioning (ADRs 146–147, seed revision 9) has executable
shell and migration tests, but this development environment has no `limactl`.
Direct native installer smoke checks passed as UID 502 for Codex 0.154.0 and
Claude Code 2.1.268; disposable installations were removed. The rebuilt CLI
also seeded both native environments in a disposable metadata home.
For bubblewrap, Ubuntu's ARM64 package was downloaded and extracted in a
disposable project directory; `bwrap --version` reported 0.11.1 successfully.
The rebuilt CLI also confirmed bubblewrap appears in Codex's prerequisites
and not in the Claude-only environment. All disposable artifacts were removed.
`make verify test-race test-integration test-package`, `make test-native-agents`,
and gopls diagnostics passed on Linux ARM64.
The full `QA.md` manual flows, including fresh-node native installation,
existing-node npm migration, interactive Claude permission bypass, macOS VZ,
and Homebrew upgrade qualification, remain unverified for this change.

Suggested solution: run every QA flow on supported macOS and Linux Lima hosts;
for Flow 4 verify `bwrap --version`, native binary ownership and paths, then
launch Claude as the
login user with `--dangerously-skip-permissions`. Also restart a node containing
untouched revision-7 npm or revision-8 native bootstrap metadata and confirm
bubblewrap and both native commands
work while credentials and customized definitions survive. Remove all QA
nodes, metadata homes and verification artifacts afterward.

Advantages: validates real privilege dropping, vendor installers, native updates,
and migration through Lima end to end. Disadvantages: requires VM-capable hosts,
network downloads, and interactive agent authentication; it cannot be replaced
by the offline installer fixtures.

### 53. Investigate timed Escape decoding in shortcut lifecycle tests

Problem: the first macOS `v0.3.4` release job failed under `-race` in
`TestTUITabOpenCloseKeyLifecycle/alt_guest/terminal/handleKey` at
`tui_tab_input_test.go:127`: a release sequence decoded as bare Escape
(`Keycode:27`, press) instead of a release event. Evidence: run 34564404178,
job 103153492648, commit `50de45e`. The same commit's macOS main-CI race job
passed. The complete macOS release job passed on its one retry without changing
the tag or checks.

Suggested solution: investigate the input parser's 10 ms Escape timer under
scheduler delays, including when the remainder of a sequence is already
buffered. Add a deterministic delayed-parser regression and make lifecycle
fixtures independent of wall-clock timing while retaining parser integration
coverage. Advantages: separates key-routing regressions from scheduling noise
and may prevent real Escape misclassification under load. Disadvantages:
requires care to preserve prompt handling of a genuine standalone Escape;
the timer explanation is a hypothesis, not a confirmed root cause. Deferred
from the tab-label release; retry results are in the release report.

### 52. Clean managed-shell INPUTRC files after terminal termination

Problem: tab-name QA found that closing a managed host tab can leave its
`~/.codelima-inputrc.*` file behind. The launch wrapper removes it after a
normal shell exit, but process-group termination can bypass that cleanup.
The two manual fixture paths were identified by creation time and the live
shell's `INPUTRC`; this run's leftovers were removed during cleanup.

Suggested solution: give the terminal supervisor ownership of temporary
readline files, including cleanup on close and failed launch; root verification
files under the project's temporary directory. Cover forced close with a real
shell test. Advantages: avoids accumulating scratch files and makes test
cleanup reliable. Disadvantages: requires changing the shared launch contract
and accounting for live handoff. Deferred from tab-label presentation.

### 51. Complete native QA for compact unnumbered shell tab defaults

The maintainer subsequently requested merging and publishing `v0.3.4` after
these limits were disclosed. Full local `make test-race`, `make test-package`
and release-tooling tests also pass. A published `v0.3.3` daemon upgraded to
the versioned `0.3.4` candidate with two live shells and a 1,044,738-byte journal;
both no-argument and explicit-path handoffs preserved IDs, shell PIDs, output
and input. Release evidence belongs in
[the compact tab report](plans/compact_tab_release_qa.md). Publication does not
complete the remaining native/manual checks.

Published as regular Latest `v0.3.4` at `50de45e`; the Homebrew formula and all
three native archives/manifests were verified. Main CI and the release matrix
passed after one macOS retry each (see #49/#53). The downloaded Linux arm64
package passed its renderer smoke test. Native/manual qualification below
remains open.

Problem: ADR 145 replaces empty/common shell titles with `shell` or `host`,
preserving application titles and badges. Regression tests first reproduced
the old long/numbered labels. `make verify`, `make test-integration`, and
race-enabled tab/metadata/bell/scoping tests pass on Linux/aarch64. Gopls is
clean for the new classifier and its tests. Full QA.md qualification remains
partial because this workspace has no `limactl`, accessible `/dev/kvm`, native
macOS terminal or Homebrew:

- Flow 1: help, schema 4/seed 7, ordered preset values, repair and non-mutating
  schema-v3 rejection passed; doctor reports the missing Lima/KVM capabilities.
- Flow 2: environment/configuration creation, resource updates and default
  delete/rename protection passed. Node creation returned
  `DependencyUnavailable`; remaining Flow 2 and Flows 3–4/6 require Lima.
- Flow 5: session-v1 quarantine and real host shells passed using a disposable
  stopped-node metadata/list fixture. Live daemon update preserved the surviving
  terminal ID, shell PID and fresh input. Guest, large-history/containment and
  physical two-window Flow 5b checks remain pending for this change.
- Flow 7: the built TUI under isolated tmux displayed `[host] host`, retained
  `host:Fix login bug` and `host:⠋ Working | codelima`, and restored `host` for
  username/path, home-relative, `bash` and empty titles. Background 60% progress
  and a bell appeared beside `host`; visiting cleared the bell. Reordering
  retained the selected terminal ID; closing left one unnumbered, usable host
  tab. The live update retained the default label. Guest and physical-terminal
  checks remain pending; automated tests cover guest labels.
- Flow 8: Linux snapshot reported reclaim unsupported; macOS remains pending.
- Flow 9: diagnostic status/list/read probes passed with the terminal ID and
  daemon PID retained. Kernel stack reads were denied; macOS sampling pending.
- Flow 10: Go/native adapter/Vaxis tests, lint and build passed through
  `make verify`. Full upstream, package and native interactive qualification
  was not repeated for this presentation change; see #41/#44.
- Flow 11: native Homebrew installation/upgrade remains pending.

The final built frontend was reopened against the surviving shell and retained
`host` and the terminal ID. The disposable TUI, tmux server, daemon, shells,
fixture home, diagnostics, scripts and orphaned verification INPUTRC files were
removed. No VM or package installation was created.

Suggested solution: run the remaining QA.md flows on native Lima-capable
hosts, especially ordinary guest prompts and real agent titles returning to
the shell. Remove all verification artifacts afterward. Advantages: verifies
actual shell conventions across platforms. Disadvantages: requires native VM,
physical-terminal and Homebrew capabilities absent here.

### 50. Complete native QA for terminal title labels and bell acknowledgement

Problem: ADRs 143–144 make reported titles replace the node/number fallback
and show `🔔` for background bells until the tab is visited. Protocol 7 pushes
metadata so hidden tabs update without full-grid reads. Regression tests
reproduce the old behavior and pass afterward. `make verify`,
`make test-integration`, and focused race tests for metadata, bell visits and
publication pass. Gopls reports only existing modernization suggestions
outside the changed lines. Full QA.md manual
qualification remains partial in this Linux/aarch64 guest:

The maintainer subsequently requested merging and releasing these changes as
`v0.3.3` after those limits were disclosed. Full local `make test-race` and
`make test-package` also pass. A published `v0.3.2` protocol-6 daemon upgraded
to the versioned `0.3.3` protocol-7 candidate with two live host terminals and
a 1,045,752-byte renderer journal; IDs, shell PIDs, retained output and new
input survived. An explicit-path live update also passed. Publication evidence
belongs in [the release report](plans/tab_status_release_qa.md); authorization
does not complete the remaining manual checks.

Published as `v0.3.3` at `b862207942c8aeb5e29915f1a05d5dc2061db95b`.
Main CI and all three native release jobs passed on their first attempt.
The regular Latest release, all public asset checksums, the standard Homebrew
formula, and the downloaded Linux arm64 renderer smoke test were verified.
The remaining native/manual flows below are still open.

- Flow 1: help, schema 4/seed 7, ordered presets and non-mutating schema-v3
  rejection passed. Doctor reports missing `limactl` and inaccessible `/dev/kvm`.
- Flow 2: environment/configuration creation passed; node creation fails with
  `DependencyUnavailable` for `limactl`. Remaining Flow 2 and Flows 3–4/6 need
  working Lima; no real VM was created.
- Flow 5: session-v1 quarantine, two real host terminals and a live daemon
  handoff passed using an isolated stopped-node metadata/list fixture. Guest,
  large-history handoff/containment and
  physical two-window Flow 5b checks remain pending for this change.
- Flow 7: built TUI in an isolated tmux server displayed both sample titles
  without node/number prefixes, retained `host:`, renamed the active tab in
  place and restored its fallback when cleared. The bell follow-up showed
  `Background task 🔔` before visiting that tab, cleared the emoji on visit,
  kept it cleared after leaving, showed a subsequent background bell, and
  acknowledged active-tab bells immediately. Live daemon update/reconnect
  retained acknowledgement and both terminal IDs. Other physical-terminal and
  guest-app checks remain pending.
  Explicit focus-independence coverage now exercises spinner-only changes,
  renaming, progress and completion in active/background/unfocused-window
  modes. A live two-host-tab TUI check also passed matching active/background
  spinner frames, title changes, 10%/60% progress, retained working state and
  completion/bell behavior. Its disposable processes and files were removed.
- Flow 8: Linux snapshot correctly reports reclaim unsupported; macOS pending.
- Flow 9: capture status/list/read probes passed and daemon PID/terminal IDs
  remained fixed. Kernel stack reads were denied; macOS sampling pending.
- Flow 10: Go tests, native Go adapter tests, Vaxis tests, lint and build passed
  through `make verify`. Full upstream/package and interactive qualification
  remain pending (see #41/#44).
- Flow 11: native Homebrew install/upgrade qualification remains pending.

Suggested solution: run remaining QA.md flows on native Lima-capable hosts,
including the title/rename/clear/bell steps with actual guest agents. Remove
all verification artifacts afterward. This run's isolated TUI, tmux server,
daemon, terminals, shell files, fixture, capture and QA home were removed.

Advantages: confirms real guest titles and host terminal behavior across the
supported platforms. Disadvantages: requires native VM, physical terminal and
Homebrew capabilities unavailable in this workspace.

### 49. Make VirtioFS ticker cancellation coverage deterministic

Problem: the first macOS `v0.3.2` release job failed under `-race` with
`panic: close of closed channel` in
`TestVirtioFSReclaimTickerCloseCancelsActiveReclaim` at
`virtiofs_reclaim_test.go:102`. This unchanged test closes its notification
channel on every callback. If a tick and cancellation are both ready, the
ticker's select can invoke the callback again with a canceled context. The
separate macOS CI race job passed on the same commit. This failure is separate
from the resize/input changes; its evidence is in release run 34535427384,
job 103065882505. A local Linux arm64 run of 500 race-enabled repetitions
also passed. The complete macOS release job passed on its one retry, without
changing the tag or skipping checks; that does not eliminate the scheduling
edge.

The initial macOS main-CI verification for `v0.3.4` reproduced the same panic
without `-race` (run 34564389208, job 103153404170, commit `50de45e`). The
job passed on its one retry after all other main-CI jobs passed. Details are in
[the compact tab release report](plans/compact_tab_release_qa.md).

Suggested solution: check cancellation before starting a reclaim (including
after acquiring the run lock), make the test notification safe for repeated
calls, and add deterministic coverage for a pending tick during cancellation.
Keep active-reclaim cancellation and bounded Close assertions.

Advantages: removes scheduler-dependent test panics and avoids unnecessary
reclaim attempts after cancellation.
Disadvantages: requires defining ticker shutdown semantics and checking
repeated Start/Close behavior; a notification-only change could hide the
underlying scheduling edge. Deferred from the terminal patch release.

### 48. [resolved] Fix idle daemon handoff after resizing

Problem: September 10 resize QA's disposable Linux/aarch64 daemon stalled in
`daemon update` after one attached host shell produced 1.1 MB of output. The
journal reported 1,047,124 bytes. Capture at 2026-09-10T21:34:48Z showed daemon
PID 33661, shell PID 33694, renderer PID 33687/generation 1, renderer state
`ready`, no pending renderer request, and successful status/list/snapshot/read
probes. No successor socket or import log appeared.

Resolution: the real-PTY reproducer confirmed that `pty.Setsize` called
`File.Fd`, clearing `O_NONBLOCK`. An idle raw reader then could not observe
handoff cancellation. ADR 142 replaces live resize ioctls in both terminal
owners with `terminalio.Resize` through `SyscallConn.Control`, preserving
descriptor flags and lifetime. Regression coverage verifies flags, cell/pixel
geometry, idle handoff/rollback and large-history CLI handoff after resizing.
The integration test waits for an emitted marker rather than command echo.

The live Linux/aarch64 TUI reproduction now passes: repeated outer resizes,
1.1 MB output, idle shell, then no-argument daemon update completed in 0.17 s.
The journal held 1,047,123 bytes. The daemon PID changed, shell PID 59319 and
retained output survived, and the TUI reconnected and accepted input. A second
explicit-path update after resizing preserved 110x24 geometry. Stopping one disposable
renderer afterward caused automatic replacement and reaping while retaining
its shell and leaving a second terminal usable. Native QA remains in #46.

### 47. [resolved] Preserve printable legacy input without an unshifted physical key

Problem: September 10 resize QA using `tmux send-keys -l` dropped `>` and `%`
before the shell received them. Logs reported `Ghostty could not encode terminal
key keycode=62` and `keycode=37`. Exact-byte `terminal send` worked.
`ghosttyKeyForVaxis` maps unshifted physical keys but rejects these characters
when the decoded legacy event supplies no base-layout code. This is distinct
from the reproduced outer-window resize reversal.

Resolution: ADR 141 uses Ghostty's unidentified physical-key contract while
retaining the observed text, codepoint, modifiers and action. Decoded printable
ASCII, accented letters, CJK and emoji tests now pass for native press/repeat
and legacy release suppression; modified and Kitty event-reporting cases pass
as well. Live `tmux send-keys -l` typing through the built TUI executed
`printf '%s\n' 'punctuation: > % $ é 中 🙂'` with exact output. Physical native
keyboard/font/Kitty-host qualification remains part of #46.

### 46. Complete native qualification of the outer-window resize fix

Problem: ADR 140 fixes the Vaxis drawing-buffer resize omission and overlay
event interception. Regression tests failed before the fix and pass after it.
`make verify`, `make test-race`, `make test-integration`, `make test-package`,
focused native encoder/resize regressions, and gopls diagnostics pass with
ADRs 140–142 applied. Full QA.md qualification remains partial.

The maintainer subsequently requested merging and releasing these fixes as
`v0.3.2` with those limits disclosed. Publication evidence is tracked in
[the release report](plans/resize_release_qa.md); authorization does not mark
the remaining manual checks complete. A previously blocked old daemon can
still require stop/start recovery, which closes its terminals.

Published as `v0.3.2` at `da3d4985893112f8f91a23e18cd16d6d08e79892`.
Native automated verification, race, integration and package checks passed
on all three release targets. Public archives/manifests, Latest status and
the standard Homebrew formula were verified, and the downloaded Linux arm64
package passed its real renderer smoke test. The initial macOS test flake and
successful retry are documented in #49 and the release report.

Local manual evidence used the built TUI, real host PTY/Ghostty worker and an
isolated tmux server on Linux/aarch64. One stopped-node metadata/list fixture
enabled host tabs; no VM was created:

- Flow 1: help, settings, schema 4/seed 7, ordered resource presets and
  non-mutating schema-v3 rejection checked. Doctor reports missing `limactl`
  and `/dev/kvm` permission denied.
- Flows 2–4 and 6: real VM create/clone/lifecycle/bootstrap/forwarding blocked
  by those runtime prerequisites; metadata fixtures are not substitutes.
- Flow 5: version-1 session quarantine and a live host terminal checked. The
  initial large-history update failure is resolved in #48. Repeated live
  updates after resizing and renderer containment now pass with real host
  shells. Guest and physical two-window Flow 5b remain pending.
- Flow 7: outer sizes 140x40, 80x20, 120x30 and 100x30 produced matching
  shell/renderer sizes 140x36, 80x16, 120x26 and 100x26, confirmed with `stty
  size`; 40x10 displayed the notice and recovered. Shell/renderer PIDs stayed
  fixed through resize cycles. Wrapped output remained readable. Configuration
  menu/messages overlay resizing, tree/terminal focus and search resizing
  passed. Direct TUI typing preserves punctuation and international text after
  #47. Guest apps, physical keyboard/font changes and remaining visual flows
  require native QA.
- Flow 8: Linux reports `virtiofs_reclaim.supported: false`; macOS unavailable.
- Flow 9: diagnostic capture passed all probes and preserved daemon PID,
  terminal IDs and input owner. Available `/proc` data captured; kernel stack
  access denied. No macOS process sample was possible.
- Flow 10: full Go normal/race tests, integration, local static package smoke,
  focused native encoder/resize checks and Ghostty resize/dirty-frame regressions
  passed. Published packages passed native automated qualification on all
  three targets. Full upstream/native-library, physical graphics/theme/clipboard
  and multi-window qualification remains in #41/#44.
- Flow 11: Homebrew installation/upgrade requires a supported native host.

Suggested solution: run every remaining QA.md flow on supported native hosts,
especially repeated outer-window and font changes with guest apps, dialogs and
search. Items 47/48 are fixed locally. Remove all disposable test state.

Advantages: verifies physical-terminal behavior and real VM apps.
Disadvantages: requires native Lima, Homebrew and terminal features unavailable
in this guest; local success does not complete release qualification.

Cleanup: verification TUI, daemon, renderer, shells and isolated tmux server
were stopped; transient shell inputrc files, metadata fixture, QA home, logs
and captures were removed. Normal ignored development builds/caches remain.

### 45. Complete native QA for shortcut key lifecycles

Problem: ADRs 138–139 make one-shot app actions run only on key press, retain
navigation repeats, and consume shortcut followups across input-context changes.
Decoded regressions fail on the previous implementation and pass with the
fixes; whole-tree `make verify`, `GOFLAGS=-race make test-tui-input`, and gopls
checks pass. Native macOS keyboard and complete QA.md qualification remain pending.

Published in `v0.3.1` at the maintainer's request after these limitations were
reported. Native automated verification, race, integration and package tests
passed on all three release targets; public artifacts and the standard
Homebrew formula were verified. The downloaded Linux arm64 package passed its
real CLI/renderer smoke test. See [the release report](plans/shortcut_release_qa.md).
Publication does not complete the outstanding manual qualification below.

Local verification on Linux/aarch64 used the built TUI, real host PTYs and
Ghostty workers in an isolated tmux session. A disposable Lima-list fixture
reported one stopped node; no VM was created. From tree and terminal focus,
press/repeat/release opened one host tab, rapid separate presses opened two,
and close followups preserved the adjacent tab. The last-tab release produced
no error. Fresh shell input, legacy Option sequences and macOS Option glyphs
worked; shell title updates added username/path text without adding terminal
IDs. All host tabs were closed before cleanup.
The expanded shortcut run also passed held F7/Info/menu actions, search paste,
form-field and selector navigation, and selector Enter/Esc/Ctrl+[ returning
only to the parent form. Held Space toggled one selection, Ctrl+u cleared it,
and held Ctrl+s created one disposable configuration. Releasing Ctrl before
the submit letter did not activate the replacement menu; opening-menu repeats
did not type into forms. Fresh shell input still worked after these transitions.
The TUI, daemon and isolated tmux server exited; the fixture, home, captures
and scratch logs were removed after verification.

QA Flow 1's help, schema/seed, presets and non-mutating schema-v3 rejection
checks passed, as did Flow 8's Linux unsupported assertion. Flows 2–4 and 6
need real Lima VMs; doctor confirms `limactl` is absent and `/dev/kvm` is
inaccessible. Flows 5/5b/7 still need the complete guest, two-window,
physical-keyboard and native visual checks beyond the host-shell subset above.
Flow 9's capture and Flow 10's complete native/interactive matrix were not
rerun for this input-only change; their outstanding native qualification stays
in #0/#41/#44. Flow 11 requires a supported Homebrew host. These partial local
checks do not complete the full QA.md matrix.

Suggested solution: run the added Flow 7 shortcut-lifecycle checks on native macOS
with Kitty event reporting and legacy Option input, including guest tabs, and
finish every remaining QA.md flow on suitable native hosts. Confirm exactly
one terminal ID per open press and only the requested ID removed per close.
Remove all verification homes, processes and VM state after the run.

Advantages: validates physical keyboard delivery and real VM shells on the
reported platform. Disadvantages: requires native terminal, Lima and Homebrew
access unavailable in this guest.

### 44. Complete manual qualification of the published libghostty release

Partially complete: `v0.3.0` is published as the regular GitHub Latest release
and standard `brianrackle/codelima/codelima` Homebrew formula. The beta formula
has been removed. This promotes beta.3's application code as explicitly requested
by the maintainer. All native verification, race, integration and package jobs
passed on macOS arm64 and Linux amd64/arm64. Public downloads match all manifests
and the standard formula; the downloaded Linux arm64 CLI/worker passed the real
package smoke test. Evidence is in [the regular release QA report](plans/regular_release_qa.md).
Earlier candidate history remains in [the beta QA report](plans/homebrew_beta_qa.md).

Problem: native/manual and interactive QA gates from #41/#43 remain open,
including the full upstream Ghostty Debug suite and actual Homebrew installation.
The maintainer authorized beta publication and its subsequent regular promotion
with those checks explicitly marked unverified in release notes. These
publication decisions do not complete qualification.

Suggested solution: finish every remaining QA.md flow on supported native
hosts, including the unfiltered upstream suite on an adequately sized host,
Flow 11's Homebrew installation, VZ/KVM/forwarding, and physical keyboard,
graphics, selection and multi-window checks. Record results and clean all
disposable verification state before closing this qualification item.

Native packaging follow-up: the macOS 14.8.9 runner warns that the native
build-identity object targets 14.8.9 while cgo links for 14.0. The runner's
executables passed their tests. Align the Zig and cgo deployment-target
metadata and qualify the oldest supported macOS version; this makes the
declared compatibility explicit but requires a native rebuild and qualification.

Local tooling observation: one disposable tap test push reported a missing Git
tree object on the shared development filesystem. Ten focused race repetitions
and the complete standard rerun passed. If it recurs, preserve the fixture's
object database and inspect Git/shared-filesystem behavior before changing the
release workflow; this enables diagnosis but requires retaining failed scratch
state temporarily and cleaning it after investigation.

Advantages: completes platform evidence for the published release.
Disadvantages: requires native hosts and interactive work unavailable in this guest; the full upstream suite needs more memory.

### 43. Complete native qualification of the focus-toggle and shortcut fixes

Problem: ADR 134 fixes Option+Backtick/F6 toggling on both press and release.
Decoded-input regressions first reproduced the return to tree focus on release;
the fix passes whole-tree `make verify`, integration tests, focused race tests
and gopls diagnostics. Native macOS keyboard/visual qualification is still
unavailable. The local Linux run passed settings, metadata repair/preset listing
and host-terminal startup; launching the real TUI in an isolated tmux session
failed `DependencyUnavailable: list Lima instances`. Doctor and node start
confirm missing `limactl`, and doctor also reports inaccessible `/dev/kvm`.
The disposable host shell executed a marker command, exited normally, and
left an empty terminal list. Its renderer and daemon exited; the temporary
home, fixture helper, captures and isolated tmux server were removed.
This does not complete QA Flow 7 or the full QA.md native/VM matrix tracked in
#0/#41. The subsequent shortcut audit and implementation are complete locally;
native qualification remains open alongside #45.

Follow-up audit (2026-09-10): eight temporary handler probes reproduced F7
opening then closing search on release; `i` toggling the pane back on release;
selector `Enter` restoring a parent dialog on press then submitting it on
release; selector `Esc` dismissing both selector and parent; `Tab` advancing
two form fields per tap; tab navigation switching twice; search `Enter`
advancing twice; and repeated selector `Space` undoing its own selection.
The probes used decoded terminal input except the explicit Space press/repeat
pair, passed by asserting those current faults, and were removed after the
audit. Permanent regression tests now reproduce those failures before the fix
and pass with ADR 139's shared lifecycle handling.

Delivered fixes:

- Search open/close (`F7`, `Esc`), Info (`i`), dialog submit/activate
  (`Enter`, `Ctrl+s`, selector-field `Right`), selector confirm/cancel
  (`Enter`, `Esc`, `Ctrl+[`), and multi-select toggles (`Space`, `Ctrl+u`)
  are explicit press-only actions. Followups are consumed across overlay
  transitions so a selector's release cannot submit or cancel its restored parent.
- Tree/menu actions (`n`, `a`, `g`, `s`, `d`, `c`, `m`) and UI quit
  (`q`, overlay `Ctrl+c`) require a press. Letter actions also match keycode-only
  events. Holding a menu-opening letter no longer types its repeats into the
  newly opened form.
- Tab switching/reordering, tree arrows, form/selector navigation, search match
  navigation and message scrolling ignore release and retain deliberate repeats.
  Text widgets preserve typing repeats/paste, and ordinary shell input preserves
  press/repeat/release/paste. Shared routing consumes claimed shortcut followups
  even when a modifier is released first; a fresh press starts a new gesture.

Whole-tree `make verify`, focused race tests and gopls checks pass. The rebuilt
TUI passed isolated host-PTY checks for the expanded actions and parent-overlay
transitions; #45 records their scope and the remaining platform limitations.

Suggested solution: run the added Flow 7 tap/release, hold/repeat and rapid
separate-press checks on macOS in both Kitty-reporting and legacy terminals,
alongside every remaining QA.md flow. Include physical-key transitions between
selectors, parent forms and real guest shells, and confirm that intentional
navigation holds still repeat.

Advantages: proves the physical-key behavior in the reporting environment
and validates the shared event policy across real guest input. Disadvantages:
requires native terminal/VM access unavailable in this guest.

### 42. Upstream the narrow native/frontend contracts and extend unsupported graphics modes

Problem: the adopted static graphics path deliberately does not animate pets,
render Unicode placeholders/registered glyphs, or checkpoint native image state.
Native clipboard reads and host-acknowledged Kitty writes remain disabled.
CodeLima carries four attributed native patches (XTQMODKEYS compatibility,
clipboard acknowledgement policy, graphics policy/eligibility, allocator-backed
snapshot pages) and an additive Vaxis bounded-image API. No upstream submission
has been made; publishing changes requires a separate external workflow.

Suggested solution: propose the minimal public contracts upstream with the
owned conformance fixtures, replacing each local patch only after its upstream
equivalent passes those fixtures. Obtain public animation deadline/tick and
glyph/placeholder rendering APIs before enabling those modes; define bounded
image recovery and actual frontend clipboard completion before widening claims.
Keep icon-title handling distinct from window-title behavior and retain only
the independently characterized XTQMODKEYS compatibility patch. Refresh the
terminal-freeze skill's historical stderr-mutex signatures through the skill
maintenance workflow; the adapter now uses bounded native log records.

Advantages: reduces downstream maintenance and enables richer graphics without
inventing protocol success or losing state silently. Disadvantages: depends on
upstream API decisions, expands runtime/transport memory and scheduling policy,
and requires native multi-client visual/clipboard qualification. The current
bounded static/text path remains useful independently of those extensions.

### 41. Finish full libghostty adoption qualification on a native, adequately sized host

Implementation is underway on `feat/libghostty-vt-adoption`; see
[the completion ledger](plans/libghostty_vt_adoption_progress.md).
Per-flow actual execution, the fixed handoff regression and complete disposable
artifact cleanup are recorded in [the QA report](plans/libghostty_vt_adoption_qa.md).

Problem: the upstream Debug `test-lib-vt` compiler was killed by the current
Linux/aarch64 guest at both four jobs and one job. This is not a passing upstream
unit suite. This environment also lacks the native macOS/Lima and two-window
interactive context required by the full QA.md matrix. In-project automated
and bridge checks continue; no recommendation is considered fully qualified yet.

Suggested solution: rerun the pinned upstream unit recipe on a host with enough
memory, then execute every QA.md flow, including actual packaged binaries,
graphics/selection/search, shell reflow, shared input, sleep/wake, and live update.
Keep source/feature/patch identities identical and clean all verification state.

Native build follow-up: the reported macOS `exec: "pkg-config": executable
file not found` exposed a missing bootstrap dependency, now fixed by ADR 133's
managed pkgconf installer and explicit Make/package selection. Linux whole-tree,
package, native-bridge and missing-tool regression checks pass. Rerun `make build`
on the reporting Mac without installing Homebrew pkg-config or deleting its
valid Ghostty cache. This directly qualifies the original environment at the
cost of requiring access to that native host; Linux fixtures alone cannot do so.

Idle compression is implemented and its Linux repeated-history fixture showed
about 84% lower current RSS; see
[measurements](plans/libghostty_vt_adoption_measurements.md). Still qualify
native Darwin residency, mixed high-entropy history, multi-worker aggregate
memory, and loaded end-to-end input latency. The suggested native-host run
avoids overgeneralizing a synthetic workload but requires additional platforms
and representative fixtures. `CODELIMA_GHOSTTY_IDLE_COMPRESSION=0` disables it
while investigating a platform-specific regression.

Advantages: validates real native behavior without weakening tests or substituting
passing subsets for the full gate. Disadvantages: requires host resources and
interactive verification unavailable in this guest; adds qualification time.

### 40. Implement and qualify the September 7 architectural review recommendations

Review: [plans/sep_7_plan.md](plans/sep_7_plan.md). The review is complete.
G01–G14 and their renderer readiness/publication/replay/deadline prerequisites
are implemented on `feat/libghostty-vt-adoption` with qualification tracked in
#41 and the [completion ledger](plans/libghostty_vt_adoption_progress.md).
The remaining non-adoption R recommendations below are still proposed. Stable R/G IDs
refer to the detailed evidence, tradeoffs, and acceptance criteria in that plan.

Problem:

- R01–R06 and R22–R26 identify remaining event ordering, mutation freshness,
  incomplete-cleanup races, input admission, SSH cancellation, replay,
  request lifetime, renderer fencing, RPC validation, rollback, and workspace
  symlink-containment gaps.
- R07–R14 identify stale TUI completions, credential-staging failure windows,
  incomplete inventory handling, resource/deadline limits, discovery/telemetry
  coupling, clone preflight, and metadata identity validation work.
- R15–R21 identify mixed application/UI/native ownership, public lifecycle/view
  ambiguity, backend divergence and job cleanup, conflicting keybindings,
  remaining screen/inventory costs, installer races, and qualification/docs drift.
- G01–G14 identify supported libghostty-vt responsibilities CodeLima can adopt:
  deterministic worker-only native linkage, rendering/formatting, hyperlinks,
  semantic input/paste, clipboard/effects/logging, colors/reflow, selection/search,
  core checkpoints, compression, graphics, and a bounded native adapter.
  September upstream requires Zig 0.16.0 and changes the C ABI; it is not a
  drop-in replacement. Checkpoints omit presentation state and images, and
  public C animation scheduling remains unresolved.
- The baseline review's passing test runs are not adoption qualification.
  New implementation evidence is tracked separately in the completion ledger;
  native Lima, macOS/VZ, two-window visual, sleep/wake, and complete live
  diagnostic qualification remain outstanding.

Suggested solution:

- Deliver the review's slices A–E, starting with deterministic regressions and
  small correctness fixes; keep the roadmap's keybinding priority independently
  shippable. Track every R01–R26 and G01–G14 recommendation through its gate.
- Preserve delivered isolation, backpressure, caching, seat/shared-input, and
  descriptor-lease work. TODO #36 is resolved by ADR 131 and is not reopened.
- Coordinate existing TODO #0/#1/#2/#4/#17/#22/#24/#25/#28/#30/#34/#35/#39
  with the plan instead of creating conflicting implementations. In particular,
  #1 no longer needs to wait for upstream terminal default-color options:
  they already exist at the current pin; host discovery/propagation remains.
- For each implemented slice, run automated tests and local execution, all
  required QA flows, and artifact cleanup. Record actual native results, update
  README/BUILD/PATTERNS/QA and ROADMAP status, and write numbered ADRs for
  internal behavioral changes. Keep the existing untracked August Ghostty
  proposal intact until its owner chooses how to consolidate it.

Advantages:

- Addresses concrete failure paths while preserving existing working safeguards.
- Removes duplicated terminal semantics and gives future features supported
  native APIs, explicit ownership, and measurable performance budgets.
- Keeps recommendations and native qualification limits traceable to evidence.

Disadvantages:

- Protocol, package, and native ABI changes need staged migration and broad
  regression coverage; some work requires native hosts and interactive QA.
- Static linking, stricter limits, and explicit errors change packaging or
  compatibility behavior and need documented rollout decisions.
- Optional graphics and complete recovery require application-owned state
  beyond what the current upstream C API can provide.

### 0. Manually verify the reworked per-node TUI terminal tabs in a real terminal

Problem:

- Terminal tabs are node-scoped (`Option+t` opens guest tabs, `Option+Shift+t` opens host tabs, `Option+Left`/`Option+Right` switch, `Option+Shift+Left`/`Option+Shift+Right` move the active tab, `Option+w` closes with adjacent focus, and TUI startup opens one default tab for the initial running node). Guest and host shells are different tab kinds on the same node target. Automated tests cover non-wrapping movement without an active-tab change, but the real-PTY, real-Ghostty path still needs native macOS observation.
- ADRs 87 and 106 make terminal the default right-pane view for every selected running node, ensure its first guest tab, and leave stopped nodes info-first. Automated tests cover startup, keyboard and mouse selection, runtime transitions, tab reuse, and the same-node `i` override; the real-terminal pass must confirm navigation restores `Info [Terminal]` for each running node without moving keyboard focus out of the node list.
- macOS Option delivery depends on the emulator: if Ghostty is not configured with `macos-option-as-alt = true` and the Option glyph fallbacks (`†`, `ˇ`, `∑`) do not arrive, the tab keybindings cannot fire.
- Daemon integration coverage proves that a second TUI takes input ownership and can open a terminal after several connection read-timeout intervals. Focus-handoff coverage also proves repeated first-to-second-to-first `FocusIn` transitions reclaim ownership before the next mutation, while revocation-event coverage proves routine handoffs stay out of the TUI footer. Paste coverage proves daemon-backed terminals batch semantic paste requests, preserve LF, retain bracket boundaries, avoid shortcut matching, and chunk large UTF-8 payloads safely. Input-queue coverage proves ordinary keys leave the UI loop without waiting for daemon RPC completion, stay ordered, and wait for fresh terminal state before redraw. A real two-terminal run still needs to confirm both TUIs repeatedly follow host-window focus without an ownership warning or observe-only error, verify multiline paste and responsive ordinary typing visually, and open a first guest or host tab after the 35-second QA idle interval.
- ADRs 88, 89, and 104 add automated unit and daemon-integration coverage for reconnect tab order, operator reordering, and non-default-width handoff replay. The matching interactive Flow 7 check still requires a real terminal on native macOS.
- ADR 91 adds an automated two-window regression proving a path-scoped refresh no longer closes tabs whose live nodes are outside that window's projection, while confirmed deletion still closes them. The matching disjoint-root, two-tabs-per-process restart flow still needs native interactive verification.
- ADR 90 removes the two CPU-amplifying paths with automated regressions: idle and hidden daemon tabs no longer poll full cell grids, and an event reader stops after one permanent `EOF` while retaining normal idle-timeout behavior. Native macOS Activity Monitor verification with multiple TUIs, active/hidden tabs, and an already-open post-update TUI is still required; this Linux/aarch64 environment cannot observe macOS process CPU or run the full interactive flow.
- ADR 112 clears the client-side handshake deadline before the long-lived
  request response pump, coalesces renderer snapshot/read publication at the
  existing 20 FPS ceiling, removes repeated refresh focus calls, emits one
  fresh daemon dirty event, and limits renderer operation logging to failures
  and latency outliers. Automated coverage keeps one request socket alive past
  its timeout and drives a real renderer worker through a 64-output burst.
  Native macOS Flow 7 must still confirm the captured recurring
  `tui refresh failed` timeouts are absent after a 30-second idle interval and
  that Activity Monitor plus the daemon log remain quiet under real typing and
  sustained output. It must also confirm ordinary typing has no pre-echo or
  backward cursor jumps.
- ADR 113 moves renderer replay out of the monolithic handoff manifest and
  transfers it as validated 512 KiB chunks. Unit coverage round-trips the full
  1 MiB journal, and integration coverage fills a real renderer above 900 KiB
  before preserving its terminal through live update. A native macOS Flow 5
  pass must still qualify descriptor transfer and the cursor/typing fix. A
  running version-3 daemon whose old sender already exceeds 1 MiB cannot
  self-upgrade; preserve it until the operator chooses between closing
  high-history tabs or one terminal-restarting daemon stop/start.
- ADR 114 backpressures an unthrottled producer at its terminal-local PTY,
  keeps renderer health/lifecycle traffic independent, and removes mutation
  response amplification. A deterministic fullscreen ANSI regression and a
  five-second Linux/aarch64 `cmatrix -u 0` run complete without renderer
  restart or terminal errors. Native macOS Flow 7 must still confirm the
  original TUI no longer freezes or emits repeated reconnect messages, and a
  host-side freeze capture is still needed to record the original event
  connection close reason and native renderer stack.
- ADR 115 removes accepted-input drain and `terminal.close` latency from the
  Vaxis event loop while keeping close admission synchronous and daemon cleanup
  single-shot. Automated coverage holds an input RPC open while repeated close
  calls return immediately, then verifies exactly one daemon close request.
  Native macOS Flow 7 must still confirm `Option+w` removes a busy `cmatrix`
  tab immediately, selects the adjacent tab, and produces no reconnect churn.
- ADR 107 replaces the permanent-disconnection latch with a reconnecting,
  resynchronizing daemon session. Automated coverage proves that a forced
  disconnect preserves terminal IDs, blocks mutations during synchronization,
  and restores readiness from authoritative epoch/sequence state. Native
  sleep/wake and live-update focus behavior still require the interactive Flow
  7 run.
- ADR 93 restores the pre-Microsandbox foreground-process-group invariant for interactive Lima shells. A real Linux/aarch64 Lima guest and controlling PTY verified Left, `Ctrl+a`, `Ctrl+e`, multiline bracketed paste, and guest `Ctrl+c`; `TestRunInteractiveCommandKeepsPTYInForeground` covers the underlying PTY ownership regression automatically. The same checks inside the complete native macOS Ghostty TUI flow remain part of this item.
- ADR 118 repairs exact known built-in Codex and Claude bootstrap snapshots and validates both CLIs as the Lima login user. Automated lifecycle coverage verifies the repair and ownership boundary, and a disposable Linux login user successfully installed the current real npm packages and ran both version commands from its `~/.local` prefix. The complete Flow 4 VM pass still needs a native Lima-capable host: this development session itself runs inside a two-vCPU Lima guest, where a 5 GiB inner guest cannot boot reliably under nested KVM and software emulation does not advance beyond UEFI.
- ADR 119 gives Codex's port-1455 browser callback newest-listener routing while preserving first-claimant routing for ordinary shared ports. Automated HTTP forwarding coverage proves transfer to a second VM and fallback when that listener disappears. The matching two-VM Flow 6 pass still needs a native Lima-capable host to complete a real `codex login` browser callback through the daemon.
- ADR 94 redirects successful Lima-command diagnostics to the rotating TUI log, and ADR 95 replaces width-growth `Ctrl-L` injection with a supplemental process-group `SIGWINCH`. A focused Linux PTY run with a fake Lima boundary reproduced the exact unhealthy-instance warning, confirmed it appeared only as `source=limactl` in `_logs/codelima.log`, toggled Option+Backtick repeatedly without `^L`, and preserved earlier output across split/full-width changes. This development session exposes `limactl` and `/dev/kvm`, but it runs inside a two-vCPU, 3.8 GiB Lima guest: temporary swap allowed the Flow 4 VM allocation, while nested-KVM multi-vCPU boot stalled and software emulation did not leave UEFI. The full lifecycle flow still requires a native-host run.
- The `diagnose-codelima-terminal-freezes` skill and its deterministic
  fake-daemon regression cover read-only status/list/snapshot collection,
  renderer-process discovery, one failed actor probe, local evidence handling,
  and the prohibition on mutating RPCs. Flow 9 still needs a live macOS daemon
  run; a future incident bundle should distinguish reconnect/control-plane
  faults from a terminal-local renderer restart.

Suggested solution:

- Run the QA.md "TUI" flow on macOS in Ghostty, specifically the startup default tab and `Option+t`/`Option+Shift+t`/`Option+Left`/`Option+Right`/`Option+Shift+Left`/`Option+Shift+Right`/`Option+w` movement and adjacent-close steps, with and without `macos-option-as-alt`. In the Create Node form, confirm the muted slug default is the slug-safe current-directory leaf, both slug and directory defaults disappear on the first typed character, and submitting untouched defaults creates the expected node.
- Run Flow 4 on a native Lima-capable host and confirm a completed legacy built-in snapshot is repaired on its next start, both npm package trees and executables belong to the Lima login user, and `codex --version` plus `claude --version` succeed through that user's `HOME` and `PATH`.
- Run Flow 6 with callback listeners on port 1455 in two VMs, confirm generic `localhost:1455` moves to the newest listener while node-qualified routes remain stable, then complete a real Codex browser login in the second VM.
- Launch the same path-scoped TUI from a second real terminal, repeatedly switch host focus in both directions, and verify each newly focused TUI can immediately open or control a guest/host tab without either window showing the ownership-revoked message. Also launch two TUIs at disjoint directory roots, open two tabs in each, wait through refresh, quit and reopen both, and verify all four tabs survive. Paste the multiline QA sample and confirm it appears promptly without executing, type the ordinary-input sample quickly and confirm it keeps pace without reordering, then leave the final owner idle for at least 35 seconds and verify its next terminal action also succeeds.
- Open guest/host/guest tabs with wrapped output, verify all idle CodeLima
  processes settle instead of pinning cores, live-update while one TUI remains
  open and verify that it reconnects and resynchronizes without a CPU spike,
  then quit and reopen at the captured width to verify tab order and line
  boundaries remain intact.
- On a real unhealthy Lima instance, leave the TUI open through several refreshes and confirm the structured warning stays out of the chrome while appearing in `_logs/codelima.log`; immediately after starting a node, repeat split/full-width focus changes and confirm no literal `^L` appears and earlier history remains visible.
- Run Flow 9 against the same macOS daemon before recovery, verify `sample` is non-terminating and every tab survives unchanged, then retain one real frozen-state bundle long enough to classify the owning stack before removing the sensitive artifact.

Advantages:

- Confirms the fix for the original manual-only failure report end to end.

Disadvantages:

- Needs a real Lima-capable host and an interactive terminal; cannot be automated in CI today.

### 1. Qualify host terminal colors on native visual hosts (implementation delivered)

Problem:

- Embedded-terminal rendering now uses Ghostty's explicit-versus-default cell semantics, so pane rendering no longer depends on guessing based on RGB equality.
- ADR 132 now uses the supported native default-color/palette API with bounded
  Vaxis discovery, theme refresh, hidden-tab propagation and recovery policy.
  Unknown colors remain unspecified; actual black stays distinguishable.
- Native macOS/outer-terminal visual and query alignment still needs QA #41.

Suggested solution:

- Run QA Flow 10 across light/dark themes, missing/partial query responses,
  hidden tabs, seat transitions and live renderer/daemon replacement.

Advantages:

- Makes guest-visible default-color queries align better with the outer terminal theme.
- Keeps the embedded Ghostty model closer to the colors the user actually sees in the host terminal.
- Builds on the current Ghostty-plus-Vaxis architecture instead of reintroducing RGB-equality guessing.

Disadvantages:

- Requires native terminals for visual qualification; no longer waits on a missing upstream API.
- Adds startup coordination between the Vaxis host terminal and the Ghostty backend.
- Theme changes become more stateful if existing terminals need to be updated in place.

### 2. Add a reliable fullscreen TUI visual verification path

Problem:

- Raw PTY/script captures are useful for text and escape-sequence inspection, but they do not reliably preserve the final fullscreen color state of the TUI in this harness.
- That makes visual regressions like background rendering harder to verify end to end without manual human inspection.

Suggested solution:

- Add a TUI verification harness that can capture rendered screen state or screenshots with color information intact.
- Use that harness for terminal-pane visual regressions such as background rendering, hyperlink styling, and scrollback behavior.

Advantages:

- Improves confidence in color and rendering changes.
- Makes TUI regressions easier to verify repeatedly.
- Reduces dependence on ad hoc manual checks for visual issues.

Disadvantages:

- Adds maintenance overhead for test tooling.
- May require terminal-emulator-specific setup or image-diff infrastructure.
- Could slow down verification if used too broadly.

### 3. Surface failures in the interactive shell `stty` repair path

Problem:

- Interactive `codelima shell` now repairs broken guest `uutils` `stty` symlinks before launching the login shell.
- That repair is currently best-effort and silent.
- If a node lacks passwordless `sudo` or does not provide `/usr/bin/gnustty`, users can still hit the broken `stty -g` round-trip without seeing why the repair did not apply.

Suggested solution:

- Detect when the guest still exposes `uutils` `stty` after the repair attempt.
- Emit a short warning in the interactive shell preflight that explains why the shell may remain incompatible with `stty -g` round-trip users.
- Consider a richer doctor or node-status check that reports the guest `stty` state proactively.

Advantages:

- Makes remaining shell breakage diagnosable instead of silent.
- Reduces confusion when the repair path cannot run on a particular node.
- Gives operators a clearer path to repair nodes manually.

Disadvantages:

- Adds more shell-startup logic and output in a path that should stay lightweight.
- Requires care to avoid noisy warnings once a node is already healthy.
- A doctor check would add another piece of guest-state probing to maintain.

### 4. Sign and notarize release artifacts

Problem:

- The new packaging and release workflow publishes binary archives and updates the Homebrew tap automatically.
- Those artifacts are not yet signed, and macOS releases are not notarized.
- That leaves users without a machine-verifiable trust signal beyond GitHub release provenance and repository control.

Suggested solution:

- Add signing to the release workflow for the generated archives and manifests.
- Add macOS code signing and notarization for the packaged `codelima-real` binary and the bundled Ghostty library before the archive is created.
- Publish signatures or checksums in the GitHub release and teach the Homebrew tap flow to reference the signed assets where appropriate.

Advantages:

- Improves end-user trust in downloaded binaries.
- Reduces friction from macOS Gatekeeper on distributed artifacts.
- Makes the release pipeline stronger before wider external distribution.

Disadvantages:

- Adds credential and secret management to the release workflow.
- Notarization will increase release latency and platform-specific maintenance.
- Signed builds are more expensive to debug when packaging changes break late in the pipeline.

### 5. Audit the remaining metadata-only service mutations for unnecessary runtime validation

Problem:

- Configuration and environment create, update, clone, and delete avoid Lima validation because they only mutate local metadata.
- Other mutating service paths may still call the broader runtime validation helper even when they do not need live sandbox state.
- That keeps some metadata-only commands slower and harder to use from environments that only need the local store.

Suggested solution:

- Audit all mutating `Service` methods and classify them as metadata-only or runtime-backed.
- Keep dependency validation only on runtime-backed operations such as node create, lifecycle, shell, and clone.
- Add focused regression tests that fail if metadata-only mutations start querying Lima again.

Advantages:

- Keeps metadata operations predictably fast.
- Makes CLI and TUI behavior more consistent when Lima is unavailable or slow.
- Reduces surprising coupling between local metadata edits and host virtualization state.

Disadvantages:

- Requires a careful audit so runtime-backed safety checks are not removed accidentally.
- May expose stale-metadata edge cases that were previously masked by a broad readiness check.
- Adds more distinction between service paths, which slightly increases maintenance overhead.

### 6. Investigate duplicate output from `codelima shell <node> -- <cmd>`

Problem:

- During QA reruns, `codelima shell qa-shell-node -- pwd` printed the expected workspace path twice.
- The command still exits successfully, but the duplicated output breaks strict verification checks and makes shell scripting against the command less predictable.
- The TUI chrome change did not touch this path, so this appears to be an existing shell execution quirk rather than a regression from the current task.

Suggested solution:

- Trace the non-interactive shell execution path to determine whether the guest command is being invoked twice or whether stdout is being relayed twice on the host side.
- Add a focused regression test for `codelima shell <node> -- pwd` that asserts a single line of output.
- Normalize the command wrapper so non-interactive shell invocations have stable, single-pass stdout behavior.

Advantages:

- Makes CLI scripting and QA verification more reliable.
- Reduces confusion for users piping `codelima shell` output into other commands.
- Produces a clearer contract between interactive and non-interactive shell modes.

Disadvantages:

- Requires care to avoid breaking the existing interactive shell startup path.
- May involve subtle changes in PTY versus non-PTY execution behavior.
- Could expose additional assumptions in current shell tests and verification scripts.

Resolution: resolved by work item 0.7. Root cause: `ExecLimaClient.Shell` layered the client's own `Stdout`/`Stderr` on top of the caller-supplied `ShellStreams` via `multiWriter`, relying on `sameWriter`'s pointer/type-identity de-dup to prevent doubling. That de-dup only collapses pointer-identical writers, so any two equivalent-but-distinct writers (same fd via different `*os.File`, or a wrapper such as the `withIO` clone) doubled every byte. Note the doubling was latent through the current `main.go`/`NewService` wiring, where both writers are the identical `os.Stdout` object. Fix: `ShellStreams` writers now win outright, with the client's writers used only as a fallback when a stream is nil, so the two are never both applied; `sameWriter` and `multiWriter`'s de-dup were removed as dead code. Regression test: `TestExecLimaClientShellDoesNotDuplicateOutputWhenStreamsAndClientWriteToSameSink`.

### 8. Design and implement a replacement for the removed patch-based file return flow

Problem:

- The user-facing patch proposal and apply workflow has been removed from the CLI and TUI.
- There is no replacement yet for moving file changes from VM-local copied workspaces back to the host when `workspace_mode=copy`.
- Users still need a deliberate path for synchronizing guest-side edits back to the host without switching every node to `mounted`.

Suggested solution:

- Design a new explicit export or sync flow for copied-workspace nodes that does not depend on lineage patch proposals.
- Keep the replacement node-scoped and directory-aware, and decide whether it should sync whole trees or a selected diff.
- Define conflict handling explicitly when several nodes are bound to the same host directory.

Advantages:

- Replaces the removed feature with a clearer workflow that better matches the copy-versus-mounted workspace model.
- Avoids preserving an outdated patch UX while the new file-return model is being designed.
- Creates a clear boundary between reusable configurations, directory-bound nodes, and workspace synchronization.

Disadvantages:

- Users in copy mode temporarily lose any built-in way to push guest-side changes back to the host.
- The final solution may require larger storage and workflow changes than the removed patch surface.

Update: the internal patch implementation has been removed entirely (work item 0.6, ADR 60), so this is now a clean-slate, node-scoped design rather than an adaptation of the old lineage-patch code.

### 9. [superseded] Complete the pre-schema-v3 interactive `TUI Verification` flow

Resolution: superseded by the schema-v3 QA flow and the focused native terminal-tab qualification in item 0. The checklist below describes the retired project/Lima surface and is retained only as historical context.

Problem:

- This change is covered by automated tests, `make verify`, and host-side manual runs of the non-interactive `QA.md` flows: `List Verification`, `Doctor And Incomplete Node Cleanup Verification`, `Tree Verification`, `Shell Verification`, `Workspace Mode Verification`, `Environment Config Verification`, `Clone Verification`, `Workspace Rebind Verification`, and `Packaging Verification`.
- The only remaining gap is the interactive `TUI Verification` checklist, which still needs a real terminal session for keyboard focus changes, project-terminal preview, sticky `i` info toggling, automatic tree refresh, terminal tab switching and closing, host-terminal red-line rendering, Ghostty-style split-pane shortcuts, right-pane dialog and selector flows, multiline paste, resize repainting, host-bypass text selection, OSC 52 clipboard sync, hyperlink activation, modified-key input such as `Shift+Enter`, and embedded-terminal behavior checks.
- That leaves one operator-facing end-to-end verification flow incomplete even though the Lima-backed CLI flows were exercised locally.

Suggested solution:

- Run the `TUI Verification` section from `QA.md` in a real terminal session on a host with working Lima boot support.
- Confirm the interactive focus toggles, preserved project and node terminal state, sticky `i` pane restoration, automatic tree refresh, terminal tab switching and closing, host-terminal red-line rendering, Ghostty-style split-pane shortcuts, right-pane transient-view behavior, multiline paste, resize repainting, host-bypass text selection, OSC 52 clipboard sync, hyperlink opening, modified-key input such as `Shift+Enter`, and streamed progress output.
- Confirm cleanup completes afterward so no verification-only Lima instances or metadata remain.

Advantages:

- Closes the last remaining manual verification gap for the current host-backed QA matrix.
- Exercises terminal-UI behavior that automated tests and CLI-only QA flows cannot fully cover.
- Produces higher confidence that the documented interactive operator workflow still holds on a real machine.

Disadvantages:

- Requires an interactive terminal session plus Lima-backed guest boot support.
- Takes materially longer than the automated test and lint verification already completed here.
- May expose environment-specific Lima issues that are not reproducible in the current sandbox.

### 10. [resolved] Fix `node delete` so runtime cleanup cannot orphan runtime instances after metadata removal

Resolution: resolved by the teardown-first deletion and incomplete-cleanup work in Track 0, retained through the microsandbox swap. Runtime deletion completes before metadata removal, and automated rollback/cleanup tests cover failed runtime deletion.

Problem:

- During manual `QA.md` reruns, `node delete` removed the node from local metadata and then failed with `NotFound: node not found` while the corresponding Lima VM instance was still running.
- After that partial failure, `node list` showed no node, but `limactl list` still showed the live instance, so cleanup had to be finished manually with `limactl delete -f`.
- This happened in both the `List Verification` and `Shell Verification` flows, so it appears to be a repeatable service-ordering bug rather than a one-off verification artifact.

Suggested solution:

- Trace the `node delete` service path to confirm whether metadata is being removed before runtime teardown and reconciliation complete.
- Reorder the delete flow so the runtime instance is stopped and deleted, or a durable cleanup record is kept, before the node metadata becomes unreachable by normal commands.
- Add a regression test that deletes a running node and asserts both the metadata and the Lima instance are gone afterward.

Advantages:

- Prevents leaked Lima instances after routine node deletion.
- Keeps `node list`, `node show`, and `limactl list` from diverging after a failed delete.
- Removes the need for manual `limactl delete -f` cleanup during QA and normal use.

Disadvantages:

- The delete path will need more careful failure handling around partially deleted runtime state.
- Fixing the ordering may require broader changes in how runtime-backed service mutations reconcile metadata.
- A durable cleanup record or retry path would add state and complexity to node lifecycle management.

### 11. [resolved] Avoid Lima containerd readiness hangs

Resolution: ADR 92 returns to Lima with both system and user containerd disabled
in every rendered instance template. Native Linux/aarch64 start/restart passed
without the optional containerd readiness path.

Problem:

- During fresh-VM manual QA reruns on March 27, 2026, some new nodes reached a usable Lima guest state, but the `codelima node start` command never returned because Lima kept retrying its optional `containerd binaries to be installed` readiness probe.
- In that state, `limactl shell` worked and the guest was reachable, but codelima metadata stayed behind the real VM state until the stuck start process was killed and cleanup was done manually.
- This blocked fresh reruns of the `Clone Verification` flow and one extra TUI smoke on newly created QA nodes, even though the Ghostty prompt regression itself was reproduced and fixed separately.

Suggested solution:

- Reproduce the condition on a clean QA home and capture the corresponding Lima hostagent logs plus codelima progress output to confirm whether the stall is entirely external or whether codelima should stop waiting once the instance is otherwise usable.
- Decide whether codelima should keep delegating fully to Lima readiness, impose its own timeout or degraded-ready state for optional Lima checks, or expose clearer progress when Lima is stuck on optional requirements.
- Add a regression test or operator-facing diagnostic coverage once the expected behavior is chosen so future `node start` hangs are easier to detect and triage.

Advantages:

- Clarifies whether this is a codelima lifecycle bug, a Lima integration edge case, or a host-environment problem.
- Improves operator trust in `node start` by avoiding silent hangs when the VM is already reachable.
- Makes future QA reruns more reliable for flows that need fresh nodes.

Disadvantages:

- The root cause may live in Lima rather than this repository, which could limit how much can be fixed locally.
- Introducing timeouts or degraded-ready behavior would add lifecycle-policy decisions around what counts as a successful start.
- Reproducing the stall consistently may require host-specific Lima state that is hard to model in automated tests.

### 12. [resolved] Replace the embedded-terminal width-growth redraw shim with a terminal-native fix

Problem:

- Embedded Ghostty terminal sessions sent `Ctrl-L` to shell-like primary-screen apps after width growth so readline prompts repainted cleanly instead of leaving duplicated wrapped fragments behind.
- During a newly starting Lima/SSH transport, that input could echo literally as `^L`; in a ready shell it could clear history the user expected to retain.
- Resolved by ADR 95: after updating Ghostty and PTY geometry, CodeLima sends a supplemental `SIGWINCH` to the PTY child process group. The existing wrapped-bash regression still passes, and a new canonical-mode PTY regression proves width growth injects no form feed.

Suggested solution:

- Keep both automated regressions: one for a clean wrapped bash prompt and one proving that canonical-mode applications receive no synthetic form feed.
- Complete the native interactive focus-toggle and history-preservation check tracked in TODO #0.

Advantages:

- Removes unsolicited redraw input from primary-screen applications.
- Makes embedded-terminal resize behavior rely on terminal semantics rather than shell conventions.
- Keeps the prompt-corruption regression covered while reducing workaround-specific behavior.

Disadvantages:

- A second signal is still a compatibility workaround around resize/reflow timing.
- Native interactive validation remains necessary because scripted PTYs cannot prove host-terminal rendering and history preservation end to end.

### 13. Validate the TUI `F6` focus-toggle fallback in Terminal.app and decide whether an Apple-specific shortcut is still needed

Problem:

- The TUI now accepts `F6` alongside `Alt-\`` for switching between tree focus and terminal focus so macOS Terminal.app users are not blocked by the default `Option` behavior.
- Automated tests cover the matcher and focus transitions, but this March 27, 2026 change was developed from a Ghostty host session, not a real Terminal.app session.
- Terminal.app users may still prefer a more ergonomic Apple-specific fallback if `fn`-modified function keys prove awkward on common laptop keyboards.

Suggested solution:

- Run the `QA.md` TUI verification flow from a real Terminal.app session and confirm `F6` toggles focus in both directions while ordinary shell input remains unaffected.
- Verify the updated README guidance about `Use Option as Meta key`, then decide whether the documented `F6` fallback is sufficient or whether CodeLima should also support a second Apple-friendly non-printing shortcut.
- If a different fallback is needed, add a targeted regression test for the new binding and update the footer/help copy in one place alongside the matcher.

Advantages:

- Confirms the new fallback solves the exact host-terminal problem that prompted the change.
- Keeps the documented macOS guidance aligned with verified behavior instead of assumption.
- Provides a clear decision point before adding more shortcuts that could complicate shell input handling.

Disadvantages:

- Requires real Terminal.app access and manual verification rather than pure unit coverage.
- A second Apple-specific binding would increase shortcut surface area and help-text complexity.
- Function-key behavior can vary with host keyboard settings, which may make the final choice somewhat environment-specific.

### 14. Validate the nested-PTY Ghostty raw-prompt regression test on Ubuntu 24.04

Problem:

- `TestGhosttyTerminalRoundTripsSttyRawPromptThroughNestedPTY` timed out in the Ubuntu 24.04 CI job because the test used the BSD `script file command ...` argv form, while util-linux `script` expects `-c` for an explicit command string.
- The test has been updated to build the nested-PTY command per platform, but that follow-up has only been exercised on macOS plus pure argument-shape unit coverage.
- This session does not have a running Linux container or VM, so the util-linux path still needs a real Ubuntu confirmation outside of unit tests.

Suggested solution:

- Rerun `make verify` on an Ubuntu 24.04 environment after this patch lands and confirm `TestGhosttyTerminalRoundTripsSttyRawPromptThroughNestedPTY` completes without the previous `ready`-file timeout.
- If the Linux run still flakes, capture the nested `script` process tree and the Ghostty PTY command arguments in test logs so the remaining discrepancy can be narrowed quickly.
- Keep the platform-specific command-builder unit test alongside the end-to-end Ghostty regression so future portability regressions fail closer to the source.

Advantages:

- Closes the exact CI portability gap that prompted this fix.
- Distinguishes a resolved argv-compatibility bug from any remaining Linux-specific Ghostty PTY timing issue.
- Leaves a clear audit trail for why this test now has platform-specific `script` handling.

Disadvantages:

- Requires access to a real Ubuntu environment with Ghostty test prerequisites available.
- If util-linux behavior varies across distro versions, more probing may still be needed than this change alone provides.
- Adds one more manual Linux-specific follow-up item to the QA backlog.

### 15. Hide or relabel conflicting TUI action hints while a background task is active

Problem:

- Long-running TUI mutations now run as background tasks and reject conflicting follow-up actions on the same node or configuration.
- The footer and action hotkeys still reflect persisted node state, so a selected busy node can continue to advertise actions like `start`, `stop`, `delete`, or `clone` even though pressing them will now return an in-progress error.
- The behavior is correct, but the hint surface is still one step behind the new background-task model.

Suggested solution:

- Teach the footer and any future contextual action list to consult the active background-task set before rendering action hints.
- Either hide conflicting actions while the task is active or relabel them with an explicit busy state so the visible shortcuts match what the operator can actually trigger.
- Keep the existing resource-conflict check in the action path as the final guard even after the hint surface is updated.

Advantages:

- Aligns the visible action hints with the real background-task behavior.
- Reduces avoidable "already in progress" errors from advertised but temporarily unavailable actions.
- Makes the async TUI model feel more intentional and self-explanatory.

Disadvantages:

- Adds more coupling between footer rendering and the task manager.
- Busy-state hint design will need a small UX decision about hiding versus relabeling actions.
- The underlying persisted node state still differs from the transient task state, so the renderer has to reconcile both sources explicitly.

### 16. Extend the interactive-shell `Shift+Enter` fix beyond bash/readline if non-default guest shells become common

Problem:

- Interactive `codelima shell` sessions and embedded TUI shells now install temporary readline bindings so bash consumes modified-enter sequences as literal newlines instead of echoing fragments like `;2;13~`.
- That repair is intentionally scoped to the default guest shell path, which is bash in CodeLima's Ubuntu Lima template.
- If a user changes their guest login shell to zsh or another line editor, the current `INPUTRC`-based fix will not help because those shells ignore readline configuration.

Suggested solution:

- Validate the current behavior with the default guest bash shell, then decide whether CodeLima should also inject equivalent bindings for zsh (`bindkey`) or other supported interactive shells.
- If broader coverage is needed, extend the interactive shell wrapper so it installs shell-specific temporary config only for the detected shell, and add focused regressions for each shell family.
- Keep the terminal-level Ghostty mode propagation in place so full-screen or app-managed terminals can still negotiate their own modified-key behavior independently of the shell wrapper.

Advantages:

- Keeps the current fix narrowly targeted at the shell that CodeLima actually provisions by default.
- Leaves room to support other guest shells without regressing terminal-app behavior that already works.
- Makes the compatibility boundary explicit instead of assuming readline settings apply universally.

Disadvantages:

- Users who replace bash with zsh or another shell may still see raw modified-enter sequences until shell-specific bindings are added.
- Supporting multiple shell families will complicate the interactive shell wrapper and its tests.
- More shell-specific logic increases the risk of drift between CLI shell sessions and embedded TUI sessions if it is not kept centralized.

### 17. Separate durable node lifecycle from live Lima runtime state

Problem:

- `node.yaml` now persists only CodeLima-owned lifecycle metadata, but the in-memory `Node` model and user-facing outputs still expose a single `status` field that mixes lifecycle values with live Lima runtime values.
- That means callers still have to infer whether a given `status` came from CodeLima lifecycle state such as `failed` or `terminated`, or from a fresh Lima observation such as `running` or `stopped`.
- The storage-layer split is done, but the API and renderer vocabulary still overlap.

Suggested solution:

- Split the public node model into an explicit lifecycle field for CodeLima-owned states such as `created`, `provisioning`, `failed`, `terminating`, and `terminated`, plus a separate live runtime field sourced from Lima.
- Update CLI and TUI rendering so operator-facing surfaces can present both concepts deliberately instead of overloading one `status` field.
- Keep compatibility shims only as long as needed for existing API and test callers.

Advantages:

- Makes Lima the single source of truth for live VM state.
- Clarifies which parts of node state are CodeLima-owned orchestration metadata versus external runtime facts.
- Reduces ambiguity for renderers, tests, and future API consumers.

Disadvantages:

- Touches a wide swath of service, CLI, TUI, and test code that currently assumes one `status` field covers both concerns.
- May require outward-facing output changes or compatibility handling for existing automation.
- Increases short-term implementation complexity while the codebase transitions to the split model.

### 18. Make the temporary interactive-shell `INPUTRC` path resilient when `$HOME` is read-only

Resolution: resolved by Track 2.2 (ADR 62). `interactiveShellLaunchCommand()` now probes `${HOME}`, then `${PWD}/tmp`, then `${TMPDIR:-/tmp}`, creating the temp INPUTRC in the first writable directory (each `mktemp` suppresses its own stderr); if none is writable it skips the INPUTRC customization rather than failing or polluting the shell. Regression tests: `TestInteractiveShellLaunchCommandToleratesReadOnlyHome`, `TestInteractiveShellLaunchCommandSkipsInputrcWhenNowhereWritable`.

Problem:

- During local TUI verification of the project terminal preview, the host-local interactive shell surfaced `mktemp: Read-only file system` while trying to create `~/.codelima-inputrc.XXXXXX`.
- The preview shell still launched, but the startup noise pollutes the terminal surface and breaks the expectation that sandboxed tooling should run cleanly inside the workspace.
- The current `interactiveShellLaunchCommand()` wrapper assumes `$HOME` is writable, which is not always true in sandboxed or locked-down environments.

Suggested solution:

- Change the temporary `INPUTRC` setup to prefer a writable project-controlled path such as a repo-root `tmp/` location or another writable temp directory when `$HOME` is not writable.
- Probe writability before calling `mktemp`, and skip the temporary readline file entirely when no safe writable location exists.
- Add a focused regression test that covers the fallback path so embedded project terminals and `codelima shell` stay quiet in read-only-home environments.

Advantages:

- Keeps interactive shell startup clean in sandboxed environments.
- Aligns better with the repo policy of keeping temporary artifacts under project-controlled temp paths.
- Reduces noisy false-alarm output in project terminal previews and interactive shell sessions.

Disadvantages:

- Requires threading a writable temporary location into a shared shell-launch helper that currently has no project-specific context.
- May need different fallback behavior for CLI shells versus TUI project previews.
- Adds more environment probing to a startup path that should remain lightweight.

### 19. [superseded] Run the pre-schema-v3 info-first split-pane QA pass

Resolution: superseded by schema-v3 `QA.md`, which covers the flat directory-scoped node list and node-scoped guest/host tabs. ADR 87 later supersedes ADR 40's info-first startup for running nodes. Native macOS keyboard and rendering qualification remains tracked once, in item 0.

Problem:

- The TUI now defaults the split pane to `[Info] Terminal` and defers terminal preview session startup until the operator toggles into terminal mode or focuses fullscreen terminal view.
- Automated coverage now verifies the new default, the inverted tab order, sticky pane-mode behavior, and the affected mouse and node-action paths.
- Automated coverage now verifies that clicking inside an active host tab preserves that tab instead of switching to the selected VM node's guest tab.
- Automated coverage now verifies automatic tree refresh, daemon-batched multiline paste with LF preservation and bracketed boundaries, resize-time active-terminal resizing, TUI terminal tab keybinds, host-terminal red-line rendering, Ghostty-style split-pane shortcuts, and OSC 52 clipboard event dispatch.
- Automated coverage now verifies DECSET 1004 focus-report gating and focus gained/lost bytes through the Ghostty focus encoder path.
- Automated coverage now verifies path-scoped TUI launch and refresh filtering, and that TUI node creation selects the new node, switches the split pane to terminal mode, and does not start a shell for a non-running node.
- The full manual `QA.md` flows still need a human-run pass against a real terminal and Lima environment to confirm the updated startup path, fullscreen restoration, link handling, and node lifecycle interactions end to end.

Suggested solution:

- This historical info-first verification path is obsolete. Use current `QA.md` Flow 7, which checks terminal-first startup for a running node, info-first startup for a stopped node, and the sticky `i` override.
- Confirm both project and node selections restore the expected pane mode after fullscreen terminal focus and that stopped-node terminal placeholders still behave correctly after the default change.
- Confirm a fullscreen host tab stays active after a mouse click inside the terminal pane, then switches back to the selected VM node's guest tab with `Option+Left` or `Option+Right`.
- Confirm the new TUI checks from `QA.md`: automatic tree refresh, multiline paste preservation, resize repainting, `Alt+t`/`Alt+Left`/`Alt+Right`/`Alt+w` terminal tab behavior, host-terminal red-line rendering, Ghostty-style split-pane shortcuts, and OSC 52 guest-to-host clipboard sync.
- Confirm the new Ghostty focus-report check from `QA.md`: enable DECSET 1004 inside a focused embedded terminal, toggle terminal focus away and back, and verify `^[[O` then `^[[I` are delivered to the guest.
- Confirm the path-scoped TUI launch from `QA.md`: `./bin/codelima --home "$CODELIMA_HOME" "$WORK_ROOT"` hides `qa-tui-outside` and keeps that scope after automatic refresh.
- Confirm the create-node TUI flow from `QA.md`: creating `qa-tui-b` selects it, shows the terminal placeholder immediately, and still does not open a shell session before the node is started.
- Record any discrepancies back into `TODO.md` or a follow-up ADR if the info-first behavior exposes a broader product decision.

Advantages:

- Closes the remaining verification gap for the new info-first default.
- Confirms the real host-terminal and Lima interactions match the updated automated expectations.
- Reduces the chance of a UI mismatch between the documented `QA.md` flow and the actual interactive experience.

Disadvantages:

- Requires an interactive terminal and Lima runtime rather than sandbox-only automation.
- Takes longer than the automated suite because the flow exercises project, node, terminal, and link interactions manually.
- May uncover environment-specific issues that require a second round of follow-up changes.

### 20. Investigate duplicate built-in environment configs on a fresh `CODELIMA_HOME`

Problem:

- During the `Environment Config Verification` rerun for the built-in agent-profile change, the first `environment list` on a brand-new `tmp/qa-env-config/.codelima` home showed three `codex` rows and three `claude-code` rows before any user-created environment config existed.
- `environment show codex` and `environment show claude-code` still worked, but repeated seeded duplicates make the list output noisy and suggest the built-in environment-config seeding path is writing multiple records for the same slug.
- That behavior was unrelated to the launch-command change in this task, so it was observed but not debugged here.

Suggested solution:

- Reproduce the issue in an automated regression test that starts from an empty metadata root and asserts a single built-in `codex` and `claude-code` environment config after the first readiness pass.
- Trace the seeding and slug-index flow for environment configs to determine why repeated built-ins are being persisted instead of recognized as already present.
- Fix the seeding path so built-in environment configs remain singleton records while still preserving user edits and deletions.

Advantages:

- Restores the expected clean built-in environment list on a fresh home.
- Prevents metadata churn and operator confusion around duplicate built-in records.
- Tightens confidence in the durable seeded-defaults behavior that both the CLI and TUI rely on.

Disadvantages:

- Requires careful debugging of the metadata store and slug-index interactions rather than a narrow surface-level fix.
- May reveal a broader store or readiness bug that touches more than just environment-config seeding.
- Could require follow-up migration or cleanup logic for homes that already contain duplicate seeded records.

Resolution: resolved by work item 0.3 (ADR 57). Root cause: `EnsureReady(mutating=false)` ran the full `EnsureLayout` seeding pass on every read with no locks; concurrent readers each missed the slug lookup and persisted built-ins with fresh IDs. Seeding now runs only from mutating readiness checks, TUI startup, and `doctor --repair`, always under the `environments`/`configurations`/`nodes` flocks. Regression tests: `TestFreshHomeSeedsSingleBuiltInEnvironmentConfigs`, `TestConcurrentSeedingDoesNotDuplicate` (under `-race`).

### 21. [cancelled] Design independent multi-shell TUI split panes

Resolution: cancelled when split panes were removed. The supported model is multiple daemon-owned terminal tabs per node target.

Problem:

- The Ghostty-style split shortcut now creates an in-TUI split surface and places the active CodeLima terminal in the new right or lower pane.
- The inactive pane is contextual chrome, not an independent second shell for the same project or node.
- Supporting independent panes would require CodeLima to allow multiple simultaneous terminal sessions for one project or node target, which is a larger identity and lifecycle change than the current roadmap item required.

Suggested solution:

- Design a multi-session terminal identity model that distinguishes project or node targets from individual terminal instances.
- Define close, focus, resize, tab-switching, and terminal-closed behavior for multiple panes and tabs that point at the same target.
- Add automated tests for duplicate node/project terminal sessions before changing the session store contract.

Advantages:

- Would make TUI split panes behave more like full terminal-emulator panes.
- Enables multiple independent shells for one node or project without leaving CodeLima.
- Creates a clearer foundation for future pane persistence or rearrangement.

Disadvantages:

- Increases terminal lifecycle complexity substantially.
- Requires new user-facing rules for duplicate session labels, close behavior, and active-target selection.
- Could destabilize the existing one-session-per-target preservation model if rushed.

### 22. Make `make init` safe under concurrent invocations

Problem:

- While verifying repository-scoped `gopls` installation, two `make init` paths were run concurrently and the existing Ghostty relink step failed with `ln: Already exists`.
- Normal sequential `make init`, `make gopls`, and `make verify` runs completed successfully, so this was not blocking the `gopls` provisioning change.
- Concurrent init can still happen when multiple agent shells or editor tasks bootstrap the same checkout at once.

Suggested solution:

- Add a project-local lock around `make init` or the installer scripts that mutate shared `.tooling` compatibility links.
- Keep the lock under `tmp/` or `.tooling/` so it remains repository-scoped.
- Prefer atomic relink behavior for compatibility symlinks where possible, and leave stale-lock cleanup documented if a locking helper is introduced.

Advantages:

- Prevents unrelated parallel tooling commands from failing during environment setup.
- Keeps the sandboxed development environment more reliable for agents and editors.
- Localizes synchronization to generated tool state instead of relying on users to serialize commands.

Disadvantages:

- Adds locking complexity to otherwise simple shell installers.
- Needs careful cleanup behavior so interrupted installs do not permanently block future setup.
- May slightly slow concurrent commands because one setup path must wait for the other.

### 23. [resolved] Verify macOS Ghostty lib-vt install after disabling xcframework emission

Resolution: resolved by the July 21, 2026 GitHub Actions runs on the `macos-14` arm64 image. Both CI jobs completed `install_ghostty_vt.sh` with the pinned Ghostty commit and `-Demit-xcframework=false`, and the v0.1.0 release job built and packaged the Darwin arm64 archive with `libghostty-vt.dylib` successfully.

Problem:

- After rebasing to Ghostty `ae52f97dcac558735cfa916ea3965f247e5c6e9e`, macOS `make init` reached Ghostty's optional lib-vt xcframework install path and failed inside `xcodebuild -create-xcframework`.
- CodeLima does not consume that xcframework; it packages and runtime-loads `libghostty-vt.dylib` directly.
- The installer now passes `-Demit-xcframework=false`, but this checkout is currently being verified from a Linux guest rather than the affected Darwin host.

Suggested solution:

- From macOS, rerun `make init` or `make ghostty-vt` and confirm the Ghostty installer completes without invoking the lib-vt xcframework step.
- Confirm `.tooling/darwin-arm64/ghostty-vt/current/lib/libghostty-vt.dylib` exists and `make build` can link the cgo bridge against it.
- If the Darwin build still fails, capture the first failing command with the updated `-Demit-xcframework=false` invocation and record the new failure separately.

Advantages:

- Closes the platform-specific verification gap for the Ghostling-pinned Ghostty build.
- Confirms the packaged macOS runtime artifact still matches CodeLima's release and `dlopen` expectations.
- Avoids debugging optional xcframework packaging that CodeLima does not use.

Disadvantages:

- Requires a macOS host with Xcode command-line tooling available.
- Does not validate Ghostty's upstream xcframework packaging path, only CodeLima's direct dylib path.
- May still expose a separate Darwin-specific shared-library build issue after the xcframework step is skipped.

### 24. Group kill misses job-control process groups inside the terminal session

Problem:

- `shutdownTerminalProcess` kills the embedded-terminal child's process group (`kill(-pid, …)`). The child is a session leader, so this covers every descendant that stays in its group — all launch chains CodeLima builds today.
- A shell running interactive job control inside a tab places each job in its own process group within the session; those groups survive a group kill of the leader's group.

Suggested solution:

- After killing the leader's group, enumerate remaining session members and signal their groups — `/proc/<pid>/stat` session ids on Linux, `ps -o sess=,pgid=,pid=` on macOS — behind a small platform seam, reusing the same TERM→KILL escalation.

Advantages:

- Closing a tab reclaims every process even under interactive job control; removes the one known leak class left in terminal teardown.

Disadvantages:

- Platform-divergent enumeration code; enumeration races process creation/exit so it can never be fully exhaustive; more time spent in Close.

### 25. `CapturesMouse` reflection races the vaxis term parser goroutine

Problem:

- `CapturesMouse` (`tui_terminal_vaxis.go`) reflects into `term.Model`'s unexported `mode` bool fields without taking the model's mutex, while the widget's parser goroutine mutates those fields on guest DECSET sequences.
- With a live guest toggling mouse reporting while the UI loop polls `CapturesMouse` (Vaxis fallback backend only), the race detector can trip; reads may also observe torn/stale mode state.
- Discovered during work item 0.8; the canary tests sequence their reads after terminal close, so they never race — the hazard is production-only.

Suggested solution:

- Adopt the proposed upstream vaxis accessor (`Model.CapturesMouse()` reading under `vt.mu` — issue draft in work item 0.8's report), pin it once released, and delete the reflection plus its canary; until then, treat occasional stale reads as tolerable (worst case: one misrouted mouse event) since the fallback backend is already de-emphasized.

Advantages:

- Removes a real data race and the last reflection into vaxis widget internals; upstream lock also fixes torn reads.

Disadvantages:

- Depends on upstream acceptance timing; interim status quo knowingly carries a benign-but-real race in the fallback path.

### 26. [resolved] Vaxis fallback terminal double-`cmd.Wait()` data race

Resolution: resolved during Track 4 verification. `vaxisTUITerminal.Close` now signals the process group, waits for the widget's `EventClosed` single-reaper notification, and only then invokes widget cleanup. `make test-race` is a repository Make target and passes.

Problem:

- `TestVaxisTUITerminalPreservesInitialOutputWhenStartedAtPaneSize` intermittently trips `go test -race`. The race is entirely inside the vendored `git.sr.ht/~rockorager/vaxis` `widgets/term.Model` (v0.15.0): its `StartWithSize` monitor goroutine and `Close()` both call `cmd.Wait()` on the same `*exec.Cmd` when the child exits as `Close` runs (`term.go` ~:175 vs ~:545). Independently observed by work items 0.8 and Track 1 PR2; no CodeLima code is on either racing stack. It fires rarely (not reproduced across 13 quiet runs; the reporting agent saw ~1/8 under concurrent load), and only in the Vaxis fallback path, not the Ghostty backend. `make verify` does not run `-race`, so it does not gate CI today.
- Note: Track 0.1's group-kill in `vaxisTUITerminal.Close()` signals the child during `Close`, which may widen the timing window in which the upstream double-`Wait` fires, but the defect is vaxis's, not ours.

Suggested solution:

- File an upstream vaxis issue for the double-`Wait` (the monitor goroutine should own `cmd.Wait` exclusively; `Close` should await it, not call `Wait` again). Pin the fix once released.
- Interim: the Vaxis fallback is de-emphasized once the Ghostty path is daemon-owned (Track 3); if the flake becomes disruptive before then, serialize the test or guard the vendored `Close`/monitor with a `sync.Once` around `cmd.Wait` via the patch flow.

Advantages:

- Removes an intermittent `-race` flake and a real (if rare) double-reap in the fallback backend.

Disadvantages:

- Root fix depends on upstream; a local vendor patch adds to the `ghostty-vt`/vaxis patch-maintenance surface.

### 27. [resolved] Bounded POLLOUT wait in `waitGhosttyPTYWritable`

Resolution: resolved during Track 4. PTY masters are nonblocking and the POLLOUT waiter uses a 100 ms bounded poll, allowing teardown or handoff closure to be observed by the next write attempt on every supported Unix host.

Problem:

- The PTY write backpressure waiter polls POLLOUT with an infinite timeout. If a future caller uses a genuinely non-blocking PTY fd (e.g. the daemon's FD handoff in Track 4), a writer parked in poll while the fd is closed elsewhere may not wake — Linux close-during-poll behavior is unspecified.
- Today this is unreachable in production: `pty.StartWithAttrs` → `Setsize` → `os.File.Fd()` flips the master to blocking mode at spawn (verified against Go 1.24.1 and creack/pty v1.1.18), so production writes block in `write(2)` and never surface EAGAIN. The waiter fires only in tests and any future non-blocking-fd context.

Suggested solution:

- Bounded poll (~100ms) with a re-check of the writer's closed state between waits, so teardown can never hang on a parked poller.

Advantages:

- Teardown can never hang on a parked POLLOUT poller once non-blocking fds enter the picture (daemon handoff).

Disadvantages:

- 10Hz retry while genuinely backpressured; touches a tested helper.

### 28. Complete Lima release qualification on every native platform

Problem:

- ADRs 92–93 and `LIMA_PLAN.md` return CodeLima to Lima 2.x with schema v4. The
  automated suite, real Linux/aarch64 QEMU/KVM create/start/shell/stop/restart,
  running-source clone restoration, observation watch, persistent SSH HTTP
  forwarding, interactive PTY input, delete, and cleanup passed on 2026-07-20
  and 2026-07-21. Evidence is in
  `plans/spike-notes/LIMA_RETURN_SPIKE.md`.
- Native macOS arm64 VZ/VirtioFS, Linux amd64, and the complete interactive
  Ghostty/agent matrix have not all run. Host reboot, macOS sleep/wake,
  five-minute idle CPU, explicit-port conflicts, two-node same-port routing,
  forced-IPv6 `localhost` and `{node}.localhost` routing,
  VirtioFS file-pressure behavior, and ADR 101 nested-virtualization detection
  on both supported and unsupported Apple silicon
  remain release qualification.
- The Linux implementation run was nested and its checkout filesystem was 9p.
  It proved that `LIMA_HOME` on 9p fails OpenSSH control-socket creation and
  that an isolated native-filesystem `LIMA_HOME` succeeds. Native release runs
  must qualify the documented short/local runtime-home requirement.

Suggested solution:

- Run every `QA.md` flow on macOS arm64, Linux amd64, and Linux arm64 using
  Lima 2.1.0 plus the newest supported 2.x release.
- Record VZ/QEMU process CPU after a five-minute idle window, interactive input
  cadence, sleep/wake or reboot recovery, mounted/copy semantics, clone source
  restoration, and observation/SSH reconnection.
- On macOS, run the VirtioFS reclaim flow across at least two 60-second
  intervals, confirming the cadence holds with the host otherwise idle.
- On macOS, confirm `doctor` capability reporting, generated
  `nestedVirtualization` YAML, and `/dev/kvm` inside both a new node and a node
  created before ADR 101. Include an unsupported-host run that creates and
  starts normally without the Lima flag.
- Append exact versions, image digests, commands, outputs, and cleanup proof to
  the Lima spike report before publishing a release.

Advantages:

- Confirms the already implemented contract on both supported native host
  virtualization stacks.
- Separates local implementation completion from release evidence that cannot
  be produced in a nested development guest.
- Confirms Lima's supported VZ and QEMU/KVM paths rather than relying only on a
  nested Linux implementation environment.

Disadvantages:

- Requires all three release architectures and human terminal interaction.
- Host reboot verification is unsuitable for ordinary CI.

### 29. Build and publish a smaller pre-baked default guest image

Problem:

- The safe default is currently Lima's upstream Ubuntu template, which supplies
  a full distribution guest and supports the apt-based built-in bootstraps.
- Pulling and bootstrapping a general-purpose cloud image increases first-node
  startup time, and CodeLima does not control the template's update cadence.

Suggested solution:

- Define a minimal apt-based cloud image/template with systemd, `/bin/sh`, CA
  certificates, curl, git, agent prerequisites, and no embedded user secrets.
- Publish immutable multi-architecture digests, add an image/template build and
  scan Make recipe, then rerun the Lima matrix before changing the default
  configuration's `image` in a new ADR.

Advantages:

- Faster and more reproducible first start.
- CodeLima controls security updates and the exact guest contract.

Disadvantages:

- Adds an image supply chain, registry, vulnerability response, and release
  surface that must be maintained for every supported architecture.

### 30. Extend and harden dynamic node service forwarding after v1

Problem:

- ADRs 70 and 79 deliver automatic HTTP/WebSocket routes at generic
  `localhost:{port}` and explicit `{node}.localhost:{port}`, but raw TCP has no
  HTTP Host header, UDP is not supported by the Lima SSH seam, and
  direct TLS hides the hostname until after connection establishment.
- ADR 92 uses Lima's private per-instance SSH config and identity without
  changing global SSH authorization, so there is no CodeLima key to revoke.
- Live daemon update reconstructs forwarding peers and host listeners after
  commit rather than transferring them with the terminal descriptors.

Suggested solution:

- Evaluate unique per-node loopback IPs plus a local resolver only if raw TCP
  demand justifies privileged host integration; separately evaluate local TLS
  termination and certificate trust as an opt-in feature.
- Transfer or coordinate forwarding listeners during live update if the brief
  route reconstruction window proves disruptive in native qualification.

Advantages:

- Could extend node-name addressing beyond HTTP and reduce forwarding churn
  during daemon replacement.
- Could preserve raw connections across daemon live update if listener handoff
  is later added.

Disadvantages:

- Per-node addresses, DNS, and trusted TLS materially expand host privileges
  and cross-platform support burden.
- Socket handoff couples dynamic routing state to the already-sensitive PTY
  update transaction.

### 31. Validate and surface guest privileges for VirtioFS cache reclamation

Problem:

- Writing `2` to `/proc/sys/vm/drop_caches` requires guest root or the
  equivalent `CAP_SYS_ADMIN` privilege. The current reclaimer executes
  `sh -c 'echo 2 > /proc/sys/vm/drop_caches'` without checking either.
- Lima logs in as its distribution user and CodeLima wraps service-issued guest
  commands — the reclaimer among them — with passwordless `sudo -H --`, so the
  command is expected to run as root for the shipped template; custom templates
  can still violate that elevation contract (ADR 129).
- Unit coverage currently uses a fake guest shell. Native macOS QA has not yet
  demonstrated that the real command can write the sysctl and release host
  descriptors. An unprivileged node reports a generic reclaim error and then
  retries after the ordinary 30-second cooldown.

Suggested solution:

- Add a tested guest privilege/writability probe before reclamation and expose
  an explicit unsupported or insufficient-privilege result in the daemon
  snapshot instead of relying on the redirection failure.
- Keep the direct write behind the centralized root command boundary. If
  non-root guests are intentionally supported, use a non-interactive elevation
  path such as `sudo -n sh -c 'echo 2 > /proc/sys/vm/drop_caches'`; never use
  `sudo echo 2 > ...`, because the calling shell performs the redirection.
- Extend QA Flow 8 to assert the effective guest identity, successful sysctl
  write, and a measurable descriptor reduction on native macOS.

Advantages:

- Prevents silent dependence on a mutable image/user contract and makes a
  disabled reclaim path immediately diagnosable.
- Gives the real privileged operation automated coverage and native release
  evidence.

Disadvantages:

- Supporting `sudo` adds another guest dependency and must remain strictly
  non-interactive so the daemon cannot block on a password prompt.
- A capability probe adds a guest round trip unless its result is cached per
  node runtime.

### 34. Restore the active terminal tab when the TUI reopens

Problem:

- Daemon-owned terminal IDs and their creation order now survive TUI restart, daemon persistence, and live update.
- The active tab remains front-end state, so a newly opened TUI selects the first surviving tab instead of the tab that was active when the prior TUI quit.
- Multiple simultaneous TUIs can legitimately select different active tabs, so a single daemon-global active value would make the windows overwrite each other.

Suggested solution:

- Define whether active selection is persisted per TUI identity, per launch scope, or as a last-detached convenience hint that does not affect already connected clients.
- Store the chosen hint separately from ordered daemon runtime state, validate that the referenced tab still exists on reconnect, and otherwise fall back to the first tab.
- Add model, daemon-session, and multi-TUI tests before marking roadmap priority 7 complete.

Advantages:

- Completes the remaining visible state restoration in roadmap priority 7.
- Returns operators to the tab they were using without changing terminal identity or order.

Disadvantages:

- Requires a durable front-end identity or explicit conflict semantics for simultaneous TUI windows.
- Adds persistence for UI preference state that must be pruned when tabs close.

### 35. Qualify terminal fault containment on native release platforms

Problem:

- Automated tests prove reconnect, queue isolation, live handoff, and a real
  non-returning C renderer call on Linux/aarch64, but release qualification
  still needs native macOS sleep/resume, suspended-TUI, and process-budget
  evidence.
- The current Linux/aarch64 workspace runs as UID 0, and Lima refuses to run as
  root. Retrying the smoke flow as the sandbox's unprivileged user with KVM
  group access created the instance, but the nested QEMU driver exited before
  SSH became available. The node, project smoke directory, and verification-only
  Lima home were cleaned, so this environment still cannot substitute for the
  native Flow 5/7/9 run.
- One renderer process per terminal has an intentional RSS/process-count cost
  that has not yet been recorded for 1, 10, and the expected maximum tab count.
- The pure-Go PTY session actor remains in the daemon. A separate session
  worker would add another fault boundary, but no observed incident currently
  justifies its IPC, adoption, and descriptor-lifecycle complexity.

Suggested solution:

- Run QA Flows 5, 7, and 9 on macOS arm64 and Linux amd64/arm64, including
  host sleep/resume and a TUI held under `SIGSTOP` past socket deadlines.
- Record daemon, TUI, and renderer RSS plus process and descriptor counts at
  1, 10, and the supported maximum terminal count; declare release bounds.
- Capture one forced renderer `SIGSTOP` incident and verify the shell PID,
  daemon PID, and other terminal remain unchanged after generation replacement.
- Extract the Go session actor into a separate process only if production
  evidence shows a terminal-local Go failure can still block unrelated daemon
  control-plane work. Keep PTY escrow and unexpected-daemon adoption separate.

Advantages:

- Converts the remaining platform and capacity assumptions into release data.
- Preserves the smaller renderer-only architecture unless evidence warrants a
  second worker boundary.

Disadvantages:

- Sleep/resume and Activity Monitor measurements require real native hosts.
- A future session-worker extraction would need a new ADR, worker adoption
  protocol, and expanded live-handoff tests.

### 36. Synchronize PTY handoff with the Ghostty read pump

Resolution: resolved by ADR 131. Each read-pump generation now captures an
immutable PTY file, numeric descriptor, quit channel, and completion channel at
launch instead of consulting mutable terminal fields. The descriptor comes
from `SyscallConn` and the pump uses raw nonblocking reads, avoiding `File.Fd()`
silently restoring blocking mode while retaining `os.File.Read`'s EOF
normalization. Handoff closes that generation's quit channel and waits for its
completion before closing the PTY;
the embedded actor drains boundary output during that wait, and rollback starts
one new pump with a new lease. Isolated input admission also stops while the
terminal is quiescing, so the writer cannot refill after its handoff drain.
`TestGhosttyTerminalHandoffTransfersPTYAndRollbackResumes` and
`TestHandoffRollbackReplaysALargeJournalWithinItsBudget` remain in the full
race suite as end-to-end regressions.

Problem:

- `make test-race` reports `TestGhosttyTerminalHandoffTransfersPTYAndRollbackResumes` closing the `os.File` inside `ghosttyPTYWriter.Close` while the terminal read pump concurrently calls `os.File.Fd` through `currentPTYFD`.
- The required `make verify` suite and the CPU telemetry race coverage pass; this race belongs to the existing terminal handoff implementation rather than live node CPU sampling.

Suggested solution:

- Give the read pump a handoff-aware PTY descriptor lease, or stop and acknowledge the read pump before closing/transferring the file.
- Keep rollback able to install the returned PTY and restart reading exactly once.
- Retain the race test in the full suite and add focused handoff/rollback cases for both EOF and active output.

Advantages:

- Removes undefined concurrent access to the Go file wrapper.
- Makes the PTY ownership transition explicit and easier to reason about during live daemon update.

Disadvantages:

- Stopping the read pump adds another acknowledgement to the handoff state machine.
- A descriptor-lease design must avoid delaying handoff indefinitely on a blocked reader.

### 37. Complete native npm agent-bootstrap verification

Problem:

- Automated tests, lint, race tests, and build verification cover the
  user-owned npm definitions, exact migration rules, seed revision 5, and
  executable validation for both built-in agents.
- The available Linux/aarch64 workspace runs as root, so native Lima was
  launched under the workspace's unprivileged user with process-local KVM group
  access. The disposable Ubuntu 25.10 guest reached early kernel boot but
  stopped advancing before SSH: the 1 GiB VM stalled at 4.8 seconds of guest
  boot time, the 2 GiB VM stalled at 6.1 seconds, and the normal 4 GiB `small`
  VM could not allocate memory on the 3.8 GiB host.
- Because SSH never became available, QA Flow 4 could not execute the Node 22,
  npm-prefix ownership, `/usr/local/bin` symlink, `codex --version`, or
  `claude --version` assertions. The disposable VMs, metadata home, Lima home,
  and workspace were removed.

Suggested solution:

- Run `make verify` and every `QA.md` flow on a native supported host with
  enough memory for the `small` profile.
- Pay particular attention to Flow 4: prove Node.js 22 or newer, both package
  trees owned by Lima's login user, npm prefix `~/.local`, both stable symlinks,
  and successful Codex/Claude version commands.
- Repeat with a seed-revision-4 home containing the untouched ADR 69 Codex
  installer and the old Claude native installer, run `doctor --repair`, and
  confirm both environment records migrate while a customized record remains
  unchanged.

Advantages:

- Provides real network, package-registry, guest-user, and PATH evidence beyond
  command-shape tests.
- Qualifies both a fresh home and the existing-home repair path operators will
  use.

Disadvantages:

- Requires a native virtualization host with enough memory and cannot be
  completed reliably in the current nested 3.8 GiB workspace.
- Downloads a full Ubuntu image plus current Node and agent packages.

### 38. Verify the seeded ~/.claude.json ends the Claude first-run login prompt on a real node, and decide whether the collector should honor CLAUDE_CONFIG_DIR

Problem:

- ADR 128's Claude import now seeds a minimal guest `~/.claude.json`
  (`hasCompletedOnboarding` plus the host's `oauthAccount`) beside the
  credentials file, because the CLI gates its login flow on that state and a
  fresh guest without it prompted for a login with valid imported tokens on
  disk. Collector and placement behavior are covered by automated tests, but
  the end-to-end proof — create a node on a native host, run `claude`, land in
  a signed-in session with no login or onboarding screen — still needs a real
  node against the current Claude Code release.
- The collector reads fixed host paths: `~/.claude/.credentials.json`, the
  macOS Keychain, and now `~/.claude.json`. A host that sets
  `CLAUDE_CONFIG_DIR` keeps both files elsewhere, so both artifacts skip and
  the guest falls back to a manual login. Codex's `CODEX_HOME` is already
  honored; Claude's equivalent is not.

Suggested solution:

- On a native host with a signed-in Claude Code, create a fresh node and
  confirm `claude` opens signed in from both the login user and `sudo claude`,
  then confirm the skip path (host without `oauthAccount`) still imports
  credentials and records the `claude_state` skip in `node.auth.imported`.
- If `CLAUDE_CONFIG_DIR` hosts matter, resolve it in `newHostAuthCollector`
  the way `CODEX_HOME` is resolved, and point both Claude reads at it.

Advantages:

- Proves the login-free first run against the real CLI rather than the file
  format it happened to use when the seed was written.
- Removes a silent skip for hosts with relocated Claude state.

Disadvantages:

- Needs a native Lima-capable host with a real Claude subscription login;
  this nested Linux/aarch64 workspace cannot run the flow.
- `CLAUDE_CONFIG_DIR` support widens the undocumented-internals surface ADR
  128 already accepts for the Keychain service name and the state keys.

### 39. Run the two-window seat-arbitration flow (QA 5b) on a native host, then decide the input-follows-seat follow-up

Problem:

- ADR 130 narrowed the daemon input lease to seat arbitration: input and tab
  control from any attached client now dispatch, only `terminal.resize` and
  `terminal.focus` consult the lease, CLI terminal commands no longer claim
  it, and a TUI's resynchronization reclaims it only when focused or already
  holding it. Automated coverage spans the daemon gate contract, the
  conditional resync reclaim, the parked resize reassert, two-client
  interleaved input against a real shell, and the full integration suite —
  but the interactive half (QA Flow 5b: two real windows, focus handoffs,
  SSH drop, Terminal.app/tmux focus-eventless variant) needs a native host
  with two displays or a second machine, which this nested Linux/aarch64
  workspace cannot provide.
- ADR 130 defers one QoL refinement: in a focus-eventless window, typing
  works but geometry stays with the seat holder until an explicit takeover.
  A fire-and-forget seat claim piggybacked on locally originated input
  (ordered ahead of the input by `ClassOwnership`'s read-order guarantee)
  would close it, at the cost of last-typer-wins geometry churn when two
  windows are typed in simultaneously.
- One release-ordering constraint from ADR 130: the CLI `want_input`
  removal must ship in the same release as (or after) the daemon predicate
  change, never before it.

Suggested solution:

- Execute QA Flow 5b on native macOS with a second window over SSH; confirm
  zero observe-only errors, interleaved typing, seat-follows-focus geometry,
  and survival of a seat holder's death.
- Decide input-follows-seat from that session's evidence: implement only if
  the cropped-geometry lag is actually noticeable in real use.

Advantages:

- Confirms the reported intermittent `client is observe-only` failure is
  gone under the exact conditions that produced it (SSH second window,
  event-stream hiccups, CLI interference).

Disadvantages:

- Needs a native host and a second interactive session; cannot be scripted
  headless because focus events and window geometry are the subject.
