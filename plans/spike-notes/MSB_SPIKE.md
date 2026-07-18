# Microsandbox Phase 0 Spike

Status: **PASS — E1–E10 passed in the current automated environment; broader release coverage remains**

Date: 2026-07-09

Decision impact: ADR 55 is accepted and the local implementation gate is open.

## Scope and environment

The original default-init close test failed, then a
focused retest proved that microsandbox's supported `--init` configuration
removes that failure. At the user's direction, the full automatable E1 matrix
was then rerun in this available nested Linux/aarch64 environment for both
embedded transports. It passed. Native two-platform and human-only visual
coverage remain release qualifications rather than blockers to implementation.

The available machine was a Linux/aarch64 CodeLima guest running under Apple's
virtualization stack, not a native Linux host and not macOS:

```text
$ uname -a
Linux lima-codelima-codelima-node-019d49c9 6.17.0-40-generic #40-Ubuntu SMP PREEMPT_DYNAMIC Fri Jun 19 16:24:16 UTC 2026 aarch64 GNU/Linux

$ systemd-detect-virt
apple

$ ls -l /dev/kvm
crw-rw---- 1 root kvm 10, 232 Jul  9 14:42 /dev/kvm

$ id
uid=502(brianrackle) gid=1000(brianrackle) groups=1000(brianrackle),44(video),991(render),992(kvm),999(systemd-journal)
```

This environment was sufficient for microsandbox's own host preflight and for
starting a nested microVM, but it does not satisfy the plan's required native
macOS/Apple Silicon plus native Linux/KVM coverage. No macOS result is claimed.

## Installation and version

The official installer was run with all persistent and temporary state rooted
under the repository:

```sh
mkdir -p ./tmp/m ./tmp/mi
HOME="$PWD/tmp/m" \
MSB_HOME="$PWD/tmp/m/.msb" \
TMPDIR="$PWD/tmp/mi" \
sh -c 'curl -fsSL https://install.microsandbox.dev | sh'
```

Relevant installer and version output:

```text
info Detected platform: linux-aarch64
info Latest version: v0.6.6
done Checksum verified
done Installed msb to /Users/brianrackle/projects/codelima/tmp/m/.msb/bin/msb
msb 0.6.6
```

`v0.6.6` was the upstream latest release published on 2026-07-07. This is the
exact candidate version tested and the production client now requires it via
`requiredMicrosandboxVersion`.

Host preflight passed:

```text
$ HOME="$PWD/tmp/m" MSB_HOME="$PWD/tmp/m/.msb" ./tmp/m/.local/bin/msb doctor
info Platform: Linux aarch64
info Version: v0.6.6
info MSB_HOME: /Users/brianrackle/projects/codelima/tmp/m/.msb
   ✓ msb          /Users/brianrackle/projects/codelima/tmp/m/.msb/bin/msb
   ✓ libkrunfw    /Users/brianrackle/projects/codelima/tmp/m/.msb/lib/libkrunfw.so.5.5.0
   ✓ KVM device   /dev/kvm
   ✓ KVM access   read/write
done Host setup is ready.
```

### Setup constraint discovered

The first project-local home was too long for microsandbox's derived Unix
socket names:

```text
error: invalid config: agent relay socket path is too long: shortest derived path is 116 bytes, but Unix socket paths on this platform must be shorter than 108 bytes; set MSB_HOME or paths.sandboxes to a shorter directory
```

Using `./tmp/m/.msb` resolved setup. A future integration would need to keep
microsandbox state below a short path rather than nest it deeply under
`CODELIMA_HOME`.

## E1 — embeddability gate

The sandbox was created with:

```sh
HOME="$PWD/tmp/m" MSB_HOME="$PWD/tmp/m/.msb" \
  ./tmp/m/.local/bin/msb create \
  --name codelima-spike-e1 --cpus 2 --memory 1G debian:bookworm
```

The `debian:bookworm` writable layer was used. `python3`, `vim-tiny`, and
`procps` were installed inside it solely to exercise the specified E1 probes.

### Result matrix

| E1 check | `msb exec -t` | `msb ssh` | Notes |
|---|---|---|---|
| 1. Guest PTY allocation | PASS in embedded Ghostty | PASS in embedded Ghostty | `/dev/pts/*`, `-t 0`, sane termios, and `TERM=xterm-256color` were observed. |
| 2. Resize propagation | PASS in embedded Ghostty | PASS in embedded Ghostty | Both transports reached `40 100`; htop also survived a live resize to `35 120`. |
| 3. Raw-mode input | PASS in embedded Ghostty | PASS in embedded Ghostty | Python received Up as `1b5b41`; Vim insert, Escape, Up, edit, and save produced `hello-up`. |
| 4. Signals, job control, and close | PASS with `--init auto` | PASS with `--init auto` | Ctrl+C, Ctrl+Z/`jobs`/`fg`, host-client reap, and zero guest processes/zombies passed. The default agent-PID-1 configuration remains unsupported. |
| 5. Full-screen agent session | Partial automated PASS | Partial automated PASS | A real Claude process launched, owned the PTY, resized, and interrupted cleanly; no credentials were copied, so authenticated prompts and minutes-long human observation remain untested. |
| 6. Escape-sequence passthrough | PASS in the automated seam | PASS in the automated seam | Truecolor cells, OSC 52 decoding, OSC 8 targets, htop rendering, mouse-capture mode, and mouse input encoding passed. Host-OS clicking/clipboard side effects were not human-observed. |
| 7. Non-interactive path | PASS | **FAIL for piped remote-command SSH** | The required exec pipe returned `hi` and status 0; exit status 37 propagated. `echo hi \| msb ssh ... -- cat` hung and needed termination. |
| 8. Latency sanity | PASS automated | PASS automated | Twenty embedded echo round trips averaged ~11.3 ms with ~12.2 ms maximum for both transports; subjective side-by-side feel was not assessed. |

### Baseline PTY evidence

Both transports allocated a real PTY when used as interactive transports:

```text
transport=exec-t
/dev/pts/0
interactive=yes
size=24 80
term=xterm-256color

transport=ssh-interactive
/dev/pts/0
interactive=yes
size=24 80
term=xterm-256color
```

A controlled host-PTY resize probe produced the same result for both
transports:

```text
# exec -t
initial=24 80
final=40 100

# ssh
initial=24 80
final=40 100
```

For both interactive transports, `sleep 100` was interrupted by Ctrl+C and was
stopped/resumed by Ctrl+Z, `jobs`, and `fg` in the host-PTY baseline.

### Embedded Ghostty evidence

A temporary Go test used CodeLima's real `newGhosttyTUITerminal` actor and
Ghostty library, started `msb exec -t codelima-spike-e1 -- bash`, resized through
the actor, encoded real Vaxis key events, observed OSC callbacks, and called the
same `Close` path as a node tab. The temporary test was intentionally removed
after the spike.

The successful checks completed before close were:

```text
PTY:             /dev/pts/1 and [ -t 0 ] == true
resize:          stty size -> 40 100
Python raw Up:   1b5b41
Vim saved line:  hello-up
signals:         Ctrl+C, Ctrl+Z, and fg all returned control
OSC 52 payload:  embedded-msb
OSC 8 target:    https://example.com/msb
```

The close assertion failed exactly as follows:

```text
=== RUN   TestTemporaryMSBEmbeddedGhosttyProbe/exec-t
    msb_spike_temp_test.go:161: timed out waiting for exec-t guest shell to reap its active sleep after terminal close
--- FAIL: TestTemporaryMSBEmbeddedGhosttyProbe/exec-t (10.85s)
```

The host `msb` client had been reaped, but the guest retained this process-table
entry:

```text
  PID  PPID STAT COMMAND ARGS
 3528     1 Z    sleep   [sleep] <defunct>
```

The process was no longer executing, but it had become an unreaped zombie
adopted by guest PID 1. It could not be killed and remained until sandbox
teardown. Repeating terminal sessions would therefore accumulate guest process
entries, which directly violates E1.4's requirement that closing a tab leave no
orphaned guest session.

### Supported-init remediation retest

Inspection of the microsandbox 0.6.6 source and documentation narrowed the
failure:

- By default, microsandbox's agent is guest PID 1.
- Relay disconnect cleanup sends SIGKILL to the exec session's process group.
- An interactive Bash foreground job uses a separate guest process group. It
  dies when the PTY closes, but is then orphaned to PID 1.
- The agent waits for the direct session process but does not run a general PID
  1 child-reaping loop during normal operation. This explains the observed
  defunct grandchild. This causal chain is an inference from the source plus the
  captured process table.
- Microsandbox officially supports handing PID 1 to a real init with `--init`;
  the agent continues running as its child.

The focused retest used the upstream systemd-equipped Debian image:

```sh
HOME="$PWD/tmp/m" MSB_HOME="$PWD/tmp/m/.msb" \
  ./tmp/m/.local/bin/msb create \
  --name codelima-spike-init --cpus 2 --memory 1G \
  --init auto --shell /bin/bash \
  ghcr.io/superradcompany/debian-systemd:12
```

Guest PID 1 was verified before the close test:

```text
pid1=systemd
shell=/usr/bin/bash
no-zombies
```

A temporary automated probe then used CodeLima's exact embedded Ghostty
`Close` path for both login transports. Each session ran a foreground
`sleep 100`; after close, the host `msb` client had to be reaped and no guest
process with command `(sleep)` could remain in any state, including `Z`:

```text
=== RUN   TestTemporaryMSBInitReapsClosedTerminalJob/exec-t
=== RUN   TestTemporaryMSBInitReapsClosedTerminalJob/ssh
--- PASS: TestTemporaryMSBInitReapsClosedTerminalJob (0.75s)
    --- PASS: TestTemporaryMSBInitReapsClosedTerminalJob/exec-t (0.39s)
    --- PASS: TestTemporaryMSBInitReapsClosedTerminalJob/ssh (0.36s)
```

This turns the original blocker into a guest-contract requirement: CodeLima's
default microsandbox image must provide a real PID 1 reaper and creation must
enable it. The tested choice was systemd. A smaller init such as `tini` or s6
may preserve a more minimal guest, but that alternative has not been tested.

The original `debian:bookworm` image cannot simply add `--init auto`; it has no
init binary. A focused create attempt exited 1, and its system log reported:

```text
agentd: handoff failed: init error: auto: no init binary found, checked: /sbin/init, /lib/systemd/systemd, /usr/lib/systemd/systemd
```

The default image must therefore change or become a CodeLima-built Debian image
that adds a lightweight reaper.

### Current-environment automated E1 rerun

The real-init sandbox was recreated from scratch and prepared with Python, Vim,
htop, procps, curl, and git. The already-installed host Claude Code 2.1.206
binary was copied into the disposable guest without copying credentials or
configuration.

A temporary test drove both `msb exec -t <name> -- bash` and `msb ssh <name>`
through CodeLima's real embedded Ghostty actor. For each transport it verified:

- a guest `/dev/pts/*` TTY, `[ -t 0 ]`, and `TERM=xterm-256color`;
- ordered resize propagation to `40x100`, plus a live htop resize to `35x120`;
- multiline paste preservation;
- Python raw-mode Up input as `1b5b41`;
- Vim alternate-screen insert, Escape, arrow, edit, save, and exit;
- htop full-screen rendering, guest mouse-capture mode, and encoded mouse
  press/release input;
- a real Claude process launching on the PTY, surviving resize, and exiting on
  Ctrl+C (no credentials were copied, so an authenticated permission-prompt
  workflow was not exercised);
- Ctrl+C, Ctrl+Z, `jobs`, and `fg`;
- twenty input-to-render echo round trips under 500 ms;
- an explicit truecolor cell (`#ff5000`), OSC 8 hyperlink target, and decoded
  OSC 52 clipboard event;
- CodeLima terminal close reaping the host client and leaving no guest sleep,
  Claude process, or zombie.

Exact test result:

```text
=== RUN   TestTemporaryMSBE1RealInitEmbeddedGhostty/exec-t
    msb_e1_temp_test.go:195: exec-t embedded echo RTT: avg=11.329453ms max=12.154753ms
=== RUN   TestTemporaryMSBE1RealInitEmbeddedGhostty/ssh
    msb_e1_temp_test.go:195: ssh embedded echo RTT: avg=11.3551ms max=12.058479ms
--- PASS: TestTemporaryMSBE1RealInitEmbeddedGhostty (4.11s)
    --- PASS: TestTemporaryMSBE1RealInitEmbeddedGhostty/exec-t (2.40s)
    --- PASS: TestTemporaryMSBE1RealInitEmbeddedGhostty/ssh (1.71s)
```

The standalone command path and final process-table check also passed:

```text
hi
pipe_status=0
exit_status=37
pid1=systemd
no-zombies
```

This is a PASS for the automatable E1 contract in the current environment. It
does not claim a native macOS result, a native Linux-host result, or human
eyeballing of an authenticated agent session over several minutes.

### Non-interactive evidence

The required exec path behaved correctly:

```text
$ echo hi | msb exec codelima-spike-e1 -- cat
hi
pipe_status=0
exit_status=37
```

By contrast, piping into `msb ssh codelima-spike-e1 -- cat` hung after input EOF.
Terminating its parent also left the SSH client and guest `cat` until they were
explicitly cleaned up. This does not affect a design that reserves SSH for
interactive login and exec for commands, but it prevents treating the two
transports as interchangeable fallbacks under the plan's E1 wording.

## E2–E10 results

| Experiment | Result | Evidence / production consequence |
|---|---|---|
| E2 JSON status | PASS | `msb ls --format json` returned one JSON array with `name`, `status`, `image`, and `created_at`; status values were `Running`/`Stopped`. |
| E3 clone | PASS | `snapshot create --from` followed by `run --snapshot ... --name ... --detach --init auto` preserved `/workspace` and `$HOME`; the temporary snapshot could then be removed. Creation flags must be repeated on the target. |
| E4 mount UID | PASS | Guest root read host files and wrote back; the guest-created host file was `502:1000`, exactly the invoking user/group, mode 0600. Host edits were immediately visible in the guest. |
| E5 process ownership | PASS | Detached VMM: `msb sandbox --name ...`, PPID 1, its own PGID/SID. Graceful `msb stop` reaped it; `--force` is the explicit immediate-kill path. |
| E6 persistence | PASS locally | Rootfs files and installed `jq` survived stop/start, as did the mount. Full physical-host reboot remains release-platform qualification. |
| E7 ports | PASS | Declared `18080:8080` reached the guest HTTP server; undeclared 18081 was unreachable. No runtime port-add command exists. |
| E8 version | PASS | Exact output `msb 0.6.6`, parsed by `^msb X.Y.Z$`. |
| E9 identity | PASS | Default guest identity is root (`uid=0`) and `sudo` is absent; bootstrap uses direct `apt-get`. |
| E10 names | PASS | Duplicate exit 1 contains `sandbox already exists`. Names start alphanumeric; alphanumeric, dot, hyphen, underscore are legal; uppercase/dots and 128 characters work; spaces, slash, and 129 characters fail. |

## Gate decision

E1–E10 are **PASS in the current environment**. All local hard gates passed,
so ADR 55 is accepted and the production swap may proceed. Native macOS/Linux,
physical reboot, and human authenticated-agent observation remain release
qualification rather than an implementation blocker.

## Observed CLI drift from the draft plan

The 0.6.6 CLI differs from several draft commands in the migration plan:

- `create` takes the image positionally: `msb create [OPTIONS] <IMAGE>`, not
  `--image <IMAGE>`.
- `create` already boots in the background; it does not take the draft `-d`.
- Machine-readable commands advertise `--format json`, not `--json`.
- `copy`/`cp` is now a first-class command.
- Snapshot create/list/inspect/export/import commands exist.
- `exec` has explicit `--stream`, `--tty`, and `--no-tty` modes.
- `ssh` has `connect`, `serve`, and `authorize` subcommands.

These observations were incorporated into the production command templates.

## Production integration QA

The completed CodeLima integration was then exercised against this same real
0.6.6 installation. A fresh v2 home passed create/start/stop/delete, copied
workspace isolation, one-off shell output, declared-only host ports, apt package
and file persistence, snapshot clone, mounted workspace writes with host UID/GID,
runtime reconciliation, and the three-layer smoke flow.

This run exposed one important lifecycle detail: both `msb create` and
snapshot-backed `msb run --detach` leave the new sandbox running. CodeLima's
default create and clone command lists now stop the new sandbox before returning,
so `node start` remains the sole bootstrap transition. The real run confirmed
new and cloned nodes report stopped until explicitly started.

Daemon QA on the shared project filesystem passed lifecycle, double-start
rejection, stale PID/socket recovery, session respawn, peer-UID authenticated
connections, a 1–30 counter with no missing or duplicated records across live
handoff, and injected importer failure with the old PID and terminal remaining
usable. The shared filesystem reported socket mode 0666 despite the requested
0600; kernel peer credentials therefore provide the authoritative same-user
boundary in this environment. A PTY-driven TUI launch rendered the scoped tree,
attached to daemon sessions, exited cleanly, and left the daemon-owned terminals
available.

## Dynamic node-hostname forwarding QA

ADR 70 was verified on 2026-07-10 in the same nested Linux/aarch64 environment
with a restored project-local `msb 0.6.6` installation. Two fresh CodeLima
nodes, `forward-a` and `forward-b`, each started an undeclared Python HTTP
listener on guest `127.0.0.1:18082`. One daemon listener routed both names:

```text
forward-a.localhost:18082 -> forward-a
forward-b.localhost:18082 -> forward-b
```

The daemon snapshot reported `authorized: true`, two SSH peers, two routes, and
one `127.0.0.1:18082` host listener. A raw HTTP Upgrade probe received `101
Switching Protocols` through port 18083. Routes recovered after daemon stop and
start without restarting the guest services. A deliberate host conflict on
18084 reported `conflicted`, then recovered on the next poll after the owner
exited. Stopping `forward-a` removed only its route while `forward-b` remained
reachable. Both sandboxes, the daemon, the local Microsandbox installation,
and all forwarding QA state were removed; the final `msb ls --format json`
returned `[]`.

## Go SDK migration QA

ADR 71 was verified on 2026-07-10 in the same nested Linux/aarch64 environment
with the official Go SDK and runtime pinned to 0.6.6. A fresh CodeLima home
passed SDK/runtime doctor matching, typed create/start/stop/delete and list,
recursive copied-workspace seeding, stdin/stdout/stderr streaming with typed
cwd, non-zero exit propagation, direct PTY `Attach`, snapshot clone with
temporary snapshot cleanup, and writable bind mounts.

The first copy attempt exposed that SDK 0.6.6 `FS.CopyFromHost` accepts one
file rather than a directory tree. Production now walks directories, creates
guest directories through the SDK, copies regular files individually, and
recreates symbolic links with typed guest exec. The corrected real flow and
new unit regression passed.

Dynamic forwarding was exercised through child processes of the form
`codelima __sdk-ssh-serve`; process inspection showed those helpers parented by
the CodeLima daemon. The only `msb` processes were the detached VM owner
processes launched internally by the SDK and reparented to PID 1. A failing
PATH-shadow `msb` executable was not invoked by CodeLima status or shell
operations. One guest-loopback HTTP listener was reached at its
`{node}.localhost` URL through the SDK helper.

The daemon, helper, three test sandboxes, temporary snapshot, project-local
0.6.6 runtime installation, CodeLima home, and workspace were removed. Final
sandbox and snapshot lists were both `[]`.

## Required follow-up before reconsideration

1. Before release, repeat E1 on native macOS/Apple Silicon and native Linux/KVM
   and human-check an authenticated agent session over several minutes.
2. Exercise a physical host reboot for E6 persistence.
3. Optionally report the default agent-PID-1 zombie behavior upstream, but do
   not block the supported-init path on an upstream change.

## Upstream references

- [Microsandbox repository and CLI overview](https://github.com/superradcompany/microsandbox)
- [Microsandbox 0.6.6 release](https://github.com/superradcompany/microsandbox/releases/tag/v0.6.6)
- [Microsandbox custom init documentation](https://docs.microsandbox.dev/sandboxes/customize#custom-init-system)
- [Official installer](https://install.microsandbox.dev)

## Cleanup

The sandbox, installed packages, downloaded runtime, temporary probe source,
and project-local microsandbox state were verification-only artifacts and were
removed after recording this report.
