# TODO

## Open Work

### 1. Feed the host terminal background into the Ghostty backend

Problem:

- Embedded-terminal rendering now uses Ghostty's explicit-versus-default cell semantics, so pane rendering no longer depends on guessing based on RGB equality.
- Ghostty itself still keeps its own internal default colors, and guest applications that query terminal defaults can still observe those Ghostty-side values rather than the outer host terminal theme.
- If upstream Ghostty eventually exposes configurable terminal default colors, CodeLima may still want to pass the host terminal colors through so guest-visible default-color queries align with the outer terminal theme too.

Suggested solution:

- Query the host terminal foreground and background through Vaxis during TUI startup when a matching Ghostty configuration surface exists.
- Pass those colors into Ghostty's terminal defaults or palette configuration instead of relying only on Vaxis-side `ColorDefault` rendering.
- Refresh that configuration when the host terminal emits a color-theme change event if Ghostty terminals need to stay aligned during a long-running TUI session.

Advantages:

- Makes guest-visible default-color queries align better with the outer terminal theme.
- Keeps the embedded Ghostty model closer to the colors the user actually sees in the host terminal.
- Builds on the current Ghostty-plus-Vaxis architecture instead of reintroducing RGB-equality guessing.

Disadvantages:

- Depends on Ghostty exposing a supported way to configure terminal default colors at runtime or startup.
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

- Project create and update plus environment config create, update, and delete now avoid Lima runtime validation because they only mutate local metadata.
- Other mutating service paths may still call the broader runtime validation helper even when they do not need `limactl` or live Lima state.
- That keeps some metadata-only commands slower and harder to use from environments that only need the local store.

Suggested solution:

- Audit all mutating `Service` methods and classify them as metadata-only or runtime-backed.
- Keep the current dependency validation only on runtime-backed operations such as node lifecycle, shell, clone, and patch apply.
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

### 8. Design and implement a replacement for the removed patch-based file return flow

Problem:

- The user-facing patch proposal and apply workflow has been removed from the CLI and TUI.
- There is no replacement yet for moving file changes from VM-local copied workspaces back to the host when `workspace_mode=copy`.
- Users still need a deliberate path for synchronizing guest-side edits back to the host without switching every node to `mounted`.

Suggested solution:

- Design a new explicit export or sync flow for copied-workspace nodes that does not depend on lineage patch proposals.
- Decide whether that replacement should be node-scoped, project-scoped, or workspace-scoped, and whether it should sync whole trees or a selected diff.
- Once the product direction is settled, remove or rework the remaining internal patch implementation to match the new transfer model.

Advantages:

- Replaces the removed feature with a clearer workflow that better matches the copy-versus-mounted workspace model.
- Avoids preserving an outdated patch UX while the new file-return model is being designed.
- Creates a cleaner boundary between project lineage management and workspace synchronization.

Disadvantages:

- Users in copy mode temporarily lose any built-in way to push guest-side changes back to the host.
- The final solution may require larger storage and workflow changes than the removed patch surface.
- Deferring the replacement leaves unused internal patch code in the codebase for now.

### 9. Complete the interactive `TUI Verification` flow from `QA.md` on a real terminal session

Problem:

- This change is covered by automated tests, `make verify`, and host-side manual runs of the non-interactive `QA.md` flows: `List Verification`, `Doctor And Incomplete Node Cleanup Verification`, `Tree Verification`, `Shell Verification`, `Workspace Mode Verification`, `Environment Config Verification`, `Clone Verification`, `Workspace Rebind Verification`, and `Packaging Verification`.
- The only remaining gap is the interactive `TUI Verification` checklist, which still needs a real terminal session for keyboard focus changes, project-terminal preview, sticky `i` info toggling, right-pane dialog and selector flows, host-bypass text selection, hyperlink activation, modified-key input such as `Shift+Enter`, and embedded-terminal behavior checks.
- That leaves one operator-facing end-to-end verification flow incomplete even though the Lima-backed CLI flows were exercised locally.

Suggested solution:

- Run the `TUI Verification` section from `QA.md` in a real terminal session on a host with working Lima boot support.
- Confirm the interactive focus toggles, preserved project and node terminal state, sticky `i` pane restoration, right-pane transient-view behavior, host-bypass text selection, hyperlink opening, modified-key input such as `Shift+Enter`, and streamed progress output.
- Confirm cleanup completes afterward so no verification-only Lima instances or metadata remain.

Advantages:

- Closes the last remaining manual verification gap for the current host-backed QA matrix.
- Exercises terminal-UI behavior that automated tests and CLI-only QA flows cannot fully cover.
- Produces higher confidence that the documented interactive operator workflow still holds on a real machine.

Disadvantages:

- Requires an interactive terminal session plus Lima-backed guest boot support.
- Takes materially longer than the automated test and lint verification already completed here.
- May expose environment-specific Lima issues that are not reproducible in the current sandbox.

### 10. Fix `node delete` so runtime cleanup cannot orphan Lima instances after metadata removal

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

### 11. Investigate Lima-backed `node start` hangs when the optional containerd readiness check never completes

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

### 12. Replace the embedded-terminal width-growth redraw shim with a terminal-native fix

Problem:

- Embedded Ghostty terminal sessions now send `Ctrl-L` to shell-like primary-screen apps after width growth so readline prompts repaint cleanly instead of leaving duplicated wrapped fragments behind.
- That workaround fixes the reproduced `bash` prompt corruption, but it relies on application-level redraw behavior rather than solving the underlying mismatch between Ghostty resize reflow and shell `SIGWINCH` cleanup sequences.
- A narrower terminal-native fix would reduce the chance of surprising behavior in other primary-screen applications that happen to match the current guard.

Suggested solution:

- Reproduce the prompt-redraw sequence against upstream Ghostty VT behavior and confirm whether the long-term fix belongs in Ghostty, in the bridge, or in how CodeLima sequences PTY resize and emulator resize.
- Replace the `Ctrl-L` shim with a terminal-level approach once a clean fix exists, then keep the current `bash` width-growth regression test as coverage for the final behavior.
- If the fix requires an upstream Ghostty patch, vendor that patch through the existing `ghostty-vt-codelima.patch` flow and document the narrower integration contract in the relevant ADR.

Advantages:

- Removes unsolicited redraw input from primary-screen applications.
- Makes embedded-terminal resize behavior rely on terminal semantics rather than shell conventions.
- Keeps the prompt-corruption regression covered while reducing workaround-specific behavior.

Disadvantages:

- The root cause may depend on upstream Ghostty VT internals outside this repository.
- A true terminal-level fix is likely more invasive than the current localized workaround.
- Validating the final behavior may require more real-terminal integration testing than the current automated regression test.

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

- Long-running TUI mutations now run as background tasks and reject conflicting follow-up actions on the same node or project.
- The footer and action hotkeys still reflect the persisted node or project state, so a selected busy node can continue to advertise actions like `start`, `stop`, `delete`, or `clone` even though pressing them will now return an in-progress error.
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
- That repair is intentionally scoped to the default guest shell path, which is bash on the Lima images CodeLima provisions today.
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

### 19. Run the full interactive `QA.md` pass against the info-first split-pane TUI

Problem:

- The TUI now defaults the split pane to `[Info] Terminal` and defers terminal preview session startup until the operator toggles into terminal mode or focuses fullscreen terminal view.
- Automated coverage now verifies the new default, the inverted tab order, sticky pane-mode behavior, and the affected mouse and node-action paths.
- The full manual `QA.md` flows still need a human-run pass against a real terminal and Lima environment to confirm the updated startup path, fullscreen restoration, link handling, and node lifecycle interactions end to end.

Suggested solution:

- Run the complete `QA.md` verification set from a host terminal with Lima available, using the updated TUI flow that starts in info mode and toggles into terminal mode with `i`.
- Confirm both project and node selections restore the expected pane mode after fullscreen terminal focus and that stopped-node terminal placeholders still behave correctly after the default change.
- Record any discrepancies back into `TODO.md` or a follow-up ADR if the info-first behavior exposes a broader product decision.

Advantages:

- Closes the remaining verification gap for the new info-first default.
- Confirms the real host-terminal and Lima interactions match the updated automated expectations.
- Reduces the chance of a UI mismatch between the documented `QA.md` flow and the actual interactive experience.

Disadvantages:

- Requires an interactive terminal and Lima runtime rather than sandbox-only automation.
- Takes longer than the automated suite because the flow exercises project, node, terminal, and link interactions manually.
- May uncover environment-specific issues that require a second round of follow-up changes.
