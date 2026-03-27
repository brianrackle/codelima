# TODO

## Open Work

### 1. Feed the host terminal background into the Ghostty backend

Problem:

- The current Ghostty background fix treats any cell whose background matches Ghostty's default background as transparent.
- This makes the pane inherit the host terminal background, but Ghostty is not yet configured with the host terminal's actual background color.
- Because of that, if an application explicitly paints the same RGB value as Ghostty's default background, the renderer cannot distinguish that from "use terminal default".

Suggested solution:

- Query the host terminal background through Vaxis during TUI startup.
- Pass that color into the Ghostty terminal configuration at creation time.
- Keep the current transparent-default rendering behavior, but compare against the configured host-matched default background instead of Ghostty's internal default alone.
- If needed, refresh that value when the host terminal emits a color-theme change event.

Advantages:

- Better visual accuracy for non-black host themes.
- Reduces the chance of explicit application backgrounds being mistaken for default-background cells.
- Keeps the current Ghostty-plus-Vaxis architecture intact.

Disadvantages:

- Adds startup coordination between the Vaxis host terminal and the Ghostty backend.
- Requires timeout and failure handling around host color queries.
- Theme changes become more stateful if existing terminals need to be updated in place.

### 2. Add a reliable fullscreen TUI visual verification path

Problem:

- Raw PTY/script captures are useful for text and escape-sequence inspection, but they do not reliably preserve the final fullscreen color state of the TUI in this harness.
- That makes visual regressions like background rendering harder to verify end to end without manual human inspection.

Suggested solution:

- Add a TUI verification harness that can capture rendered screen state or screenshots with color information intact.
- Use that harness for terminal-pane visual regressions such as background rendering, hyperlink styling, and selection overlays.

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

### 7. Stop runtime validation from leaking raw `limactl list --json` output

Problem:

- During manual QA, some runtime-backed commands such as `node start` printed raw `limactl list --json` output before their normal command result.
- The commands still succeeded, but the leaked JSON makes CLI output noisy and undermines the documented table- and record-oriented command contracts.
- This looks separate from the existing duplicate-output issue in non-interactive shell mode because it occurred on lifecycle commands rather than `shell -- <cmd>`.

Suggested solution:

- Trace the runtime validation and Lima client paths that call `limactl list --json` before node lifecycle operations.
- Ensure that probing output is captured for internal parsing instead of being written to command stdout.
- Add a focused regression test that asserts `node start` and similar lifecycle commands do not emit unrelated Lima JSON records.

Advantages:

- Restores predictable CLI output for both human and scripted use.
- Keeps lifecycle command output aligned with the documented user-facing contract.
- Makes manual QA less brittle because command success is not mixed with unrelated probe output.

Disadvantages:

- Requires care around shared Lima client plumbing so progress streaming for real long-running operations still works.
- May reveal other places where stdout/stderr routing is broader than intended.
- Could require test doubles or refactoring around Lima readiness checks to isolate probe output cleanly.

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
- The only remaining gap is the interactive `TUI Verification` checklist, which still needs a real terminal session for keyboard focus changes, mouse selection, hyperlink activation, and embedded-terminal behavior checks.
- That leaves one operator-facing end-to-end verification flow incomplete even though the Lima-backed CLI flows were exercised locally.

Suggested solution:

- Run the `TUI Verification` section from `QA.md` in a real terminal session on a host with working Lima boot support.
- Confirm the interactive focus toggles, preserved per-node terminal state, mouse-copy behavior, hyperlink opening, and streamed progress overlay behavior.
- Confirm cleanup completes afterward so no verification-only Lima instances or metadata remain.

Advantages:

- Closes the last remaining manual verification gap for the current host-backed QA matrix.
- Exercises terminal-UI behavior that automated tests and CLI-only QA flows cannot fully cover.
- Produces higher confidence that the documented interactive operator workflow still holds on a real machine.

Disadvantages:

- Requires an interactive terminal session plus Lima-backed guest boot support.
- Takes materially longer than the automated test and lint verification already completed here.
- May expose environment-specific Lima issues that are not reproducible in the current sandbox.

### 10. Widen the runtime-loaded Ghostty bridge if the current symbol surface blocks newer input and render APIs

Problem:

- The embedded terminal now uses Ghostty's key encoder through the existing `dlopen` bridge, but Ghostling uses a broader Ghostty API surface for keyboard-mode state, mouse encoding, viewport scrolling, and richer render semantics.
- The current bridge in `internal/codelima/tui_terminal_ghostty_cgo.go` still exposes only a reduced subset of the available Ghostty C API.
- If newer Ghostty APIs remain unavailable because the runtime-loaded bridge does not surface them, future Ghostty-aligned improvements will stall behind bridge work instead of the TUI behavior itself.

Suggested solution:

- Audit the currently packaged `libghostty-vt` exports against the Ghostling-style APIs we want next.
- Add optional symbol loading for the needed Ghostty surfaces before considering a packaging-model change.
- Only revisit direct linking if runtime loading itself, rather than the missing symbol bridge, is what blocks the next useful Ghostty integration step.

Advantages:

- Keeps the current runtime-library discovery and packaging model intact.
- Makes future Ghostty-backed input and rendering improvements incremental instead of all-or-nothing.
- Separates "missing bridge surface" from "runtime loading is the wrong architecture".

Disadvantages:

- Increases the size and maintenance burden of the cgo bridge layer.
- Some newer Ghostty APIs may still need packaging or header updates even after the symbol bridge widens.
- Optional-symbol compatibility paths add more branches to test.

### 11. Fix `node delete` so runtime cleanup cannot orphan Lima instances after metadata removal

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
