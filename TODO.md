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

### 6. Surface incomplete node metadata directories in doctor and cleanup tooling

Problem:

- Failed `node create` attempts used to leave behind `CODELIMA_HOME/nodes/<id>/` directories that contained a generated Lima template but no `node.yaml`.
- The runtime now ignores those incomplete directories so existing homes recover automatically, but operators do not get any explicit signal that cleanup was needed.
- That leaves silent metadata drift on disk and makes it harder to explain why a machine recovered after upgrade.

Suggested solution:

- Teach `doctor` to scan `CODELIMA_HOME/nodes/` for directories missing `node.yaml`.
- Report those directories as warnings and optionally add a dedicated cleanup command that removes incomplete node metadata directories after confirmation.
- Consider logging a one-time TUI/CLI warning when such directories are skipped during startup.

Advantages:

- Makes metadata repair visible and diagnosable.
- Gives operators a supported cleanup path for stale directories left by older builds.
- Helps distinguish silently skipped partial nodes from intentionally deleted nodes.

Disadvantages:

- Adds more store-health logic and another case to maintain in `doctor`.
- A cleanup command needs careful confirmation semantics to avoid deleting the wrong data.
- Startup warnings could become noisy if not rate-limited or deduplicated.
