# Kill terminal process groups instead of direct child PIDs

## Context and Problem Statement

Embedded terminal tabs spawn multi-process chains: a node tab runs `codelima shell <node>`, which shells out to `sh -lc "limactl shell …"`, which runs `ssh`. Closing a tab previously called `cmd.Process.Kill()` on the top PID only, so the descendants reparented to init and could keep a VM shell alive after the tab was gone. Both terminal backends already start the child as a session leader (`Setsid: true`), so its process-group id equals its pid and the whole group can be signalled with `kill(-pid, sig)`.

## Decision Drivers

* Closing a tab must terminate every process the tab spawned, including grandchildren that ignore SIGHUP.
* `Close` must reap the direct child before returning so no zombie survives, and `cmd.Wait` may only ever run once.
* Give well-behaved children a hangup grace first; shells save history on SIGHUP.
* The same termination policy must serve the Ghostty backend, the Vaxis fallback backend, and the future daemon-owned registry, on both Linux and macOS (no `/proc`).

## Considered Options

* Keep killing only the direct child PID.
* Escalating process-group termination through one shared shutdown helper.
* Enumerate and signal every session member individually.

## Decision Outcome

Chosen option: "Escalating process-group termination through one shared shutdown helper", because the child is already a session leader, making group signalling available for free, and a single helper gives both backends (and later the daemon) identical, testable semantics without platform-specific process enumeration.

The helper is `shutdownTerminalProcess(pid, closeIO, done)` in `tui_terminal_shutdown.go`. Escalation: close the writer and PTY master (the master close hangs up the line, delivering SIGHUP to the foreground process group) → 250ms grace → `SIGTERM` to the group → 250ms grace → `SIGKILL` to the group → reap with a 2s deadline at 20ms polling. Liveness is probed with `kill(-pid, 0)`; `ESRCH` means the group is gone and is success, not an error. The 250ms/20ms constants are field-tested escalation values reimplemented from their description, not copied. Reaping stays single-owner: the Ghostty backend's `Start` launches one goroutine that runs the `waitOnce`-guarded `wait()` and closes a `waitDone` channel; teardown selects on that channel instead of calling `cmd.Wait` again. `Close` and `finish` now share one internal `teardown` that captures and nils resources under the mutex, so the cgo handle frees happen exactly once regardless of how the two paths interleave. The Vaxis fallback keeps the `*exec.Cmd` it started and calls the same helper after `model.Close()` (the widget owns that backend's `cmd.Wait`, so `done` is nil and the helper relies on group polling).

### Positive Consequences

* Closing a tab kills SIGHUP-ignoring grandchildren (the `limactl`/`ssh` chain) on both backends.
* `Close` never returns while the direct child is an unreaped zombie.
* One teardown body replaces the duplicated `Close`/`finish` resource release, eliminating the double-free/skipped-free class of bugs.
* The helper is PTY-free and backend-agnostic, ready for the daemon-owned terminal registry.

### Negative Consequences

* Closing a tab whose processes ignore SIGHUP now blocks up to ~500ms (grace periods), and up to ~2s in pathological cases, where it previously returned almost immediately.
* A shell running job control can place jobs in their own process groups inside the session; those escape a group kill of the leader's group. Enumerating session members is deferred hardening, recorded in TODO.md.

## Pros and Cons of the Options

### Keep killing only the direct child PID

Signal only `cmd.Process` and let descendants reparent.

* Good, because `Close` returns immediately.
* Good, because no signal ever reaches a process we did not start.
* Bad, because orphaned `limactl shell`/`ssh` descendants keep VM shells alive after the tab closes — the bug this decision fixes.

### Escalating process-group termination through one shared shutdown helper

Group-signal the session leader's process group with a SIGHUP → SIGTERM → SIGKILL ladder and bounded reaping.

* Good, because `Setsid` children make `kill(-pid, sig)` cover the real launch chains with no process enumeration.
* Good, because `kill(-pid, 0)` liveness polling works identically on Linux and macOS.
* Good, because graceful processes get SIGHUP/SIGTERM time before SIGKILL.
* Bad, because processes that move themselves into new process groups inside the session are not covered.

### Enumerate and signal every session member individually

Walk `/proc` (Linux) or process listings (macOS) to find every process in the child's session and signal each.

* Good, because it would also catch job-control process groups inside the session.
* Bad, because it needs divergent platform-specific code (`/proc` does not exist on macOS).
* Bad, because enumeration races process creation and exit, so it is still not exhaustive.

## Links

* Implements work item 0.1 of [plans/IMPROVEMENT_PLAN.md](../plans/IMPROVEMENT_PLAN.md)
