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
