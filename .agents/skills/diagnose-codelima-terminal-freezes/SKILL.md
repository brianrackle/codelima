---
name: diagnose-codelima-terminal-freezes
description: Capture and interpret live CodeLima daemon evidence when terminals freeze across tabs, nodes, VMs, or TUI instances. Use while the failure is still occurring to distinguish daemon control-plane, Go session, per-terminal Ghostty renderer, event fan-out, reconnect, client snapshot, and Lima transport faults without restarting or mutating the daemon.
---

# Diagnose CodeLima Terminal Freezes

Capture the live failure before attempting recovery. Preserve daemon and terminal state; never turn an intermittent freeze into an unreproducible restart.

## Safety boundary

- Run diagnostics on the host that owns the daemon, not inside a guest VM.
- Treat `daemon stop`, `daemon update`, rebuilding, `SIGQUIT`, terminal input, input takeover, and tab closure as out of scope until capture finishes.
- Never use `sudo`, attach a debugger, upload artifacts, or signal a process automatically.
- Write artifacts under the repository `tmp/` directory by default.
- Warn that logs, argv, paths, and terminal metadata may be sensitive before sharing.

## Workflow

1. Tell the user that a read-only incident capture is starting and that the daemon will remain untouched.
2. Confirm the affected TUIs use the same `CODELIMA_HOME`. Record separate daemon PIDs when multiple homes are involved.
3. Run the bundled capture script from the CodeLima repository:

   ```sh
   make diagnose-terminal-freeze
   ```

   The script reads `CODELIMA_HOME` and otherwise defaults to `~/.codelima`. Prefer `--binary ./bin/<host-os>-<host-arch>/codelima` when the shared compatibility symlink may point at a guest build. Pass `--terminal-id <id>` to select a known frozen tab; otherwise the script probes at most the first listed terminal.
4. Keep the resulting directory intact. Do not restart the daemon until the script has completed its native process sample.
5. Read `references/interpretation.md` completely, then inspect:
   - `summary.md`
   - probe exit files and stderr
   - `daemon-sample.txt` on macOS
   - `renderer-sample.txt` on macOS when the selected terminal has a renderer
   - `renderer-pids.txt` and `renderers-ps.txt`
   - `daemon.log.tail` and `codelima.log.tail`
   - daemon identity, session, process, and open-file captures
6. Report observed evidence separately from hypotheses. Name the narrowest failing boundary and quote stack-function names, probe results, daemon PID, renderer PID/generation, shell PID, and capture timestamp.
7. Recommend recovery only after capture. Explain that forced daemon termination may lose live shell processes and that live update can itself block when terminal actors are wedged.

## Capture script

Use `scripts/capture.sh`. It performs only:

- daemon status, terminal list, and daemon snapshot reads
- at most one terminal screen read
- metadata and log copies
- process inspection
- non-terminating macOS `sample` captures of the daemon and selected renderer when available

The script continues when an individual probe fails and records every exit status. A successful script exit means the evidence bundle was created, not that the daemon is healthy.

If the script cannot run, reproduce the same sequence manually without adding mutating RPCs. Do not substitute `SIGQUIT`; Go terminates after emitting that dump.

## Completion criteria

Finish only after:

- the capture directory exists and `summary.md` is readable
- the shared or distinct daemon PID relationship is stated
- control-plane and terminal-actor probe outcomes are classified
- renderer state, generation, pending operation, and shell-PID continuity are classified when available
- native stack evidence is interpreted when available
- sensitive artifacts remain local unless the user explicitly chooses to share them
