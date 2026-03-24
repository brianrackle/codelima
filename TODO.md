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

### 8. Fix the environment-config QA flow where updated config commands do not reach later node bootstrap verification

Problem:

- During the full `QA.md` rerun, the matrix repeatedly stopped in `Environment Config Verification` after `environment update qa-shared --env-command 'pwd >/dev/null'` and a later `node create --project qa-env-b --slug qa-env-b-node`.
- The failure appears before the TUI-specific checks for the current task and is unrelated to the header/menu copy changes.
- The likely fault is that the updated reusable environment config is not being reflected in the created node's resolved `bootstrap.json` as the QA flow expects.

Suggested solution:

- Trace the environment-config update and node bootstrap resolution path to confirm whether updated config commands replace the previous command list for future nodes.
- Add a focused regression that updates a reusable environment config, creates a fresh node from a referencing project, and asserts the new command appears in that node's bootstrap state.
- Fix either the environment-config persistence path or the project command resolution path so new nodes always receive the latest referenced config commands.

Advantages:

- Restores the documented `QA.md` environment-config verification flow.
- Improves confidence that reusable configs behave predictably after updates.
- Prevents stale bootstrap command sets from being baked into newly created nodes.

Disadvantages:

- May require refactoring around config update semantics versus append/replace semantics.
- Could surface compatibility assumptions in current tests or user workflows.
- Needs real Lima-backed verification because the issue appears in the full end-to-end flow rather than only unit tests.

### 9. Design and implement a replacement for the removed patch-based file return flow

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
