# Version the renderer worker protocol and compact its cell encoding

## Context and Problem Statement

ADR 108 moved every libghostty call into a per-terminal `codelima-renderer-worker` process. The daemon resolves that worker strictly beside the running executable and speaks a length-prefixed JSON frame protocol to it — a protocol that carried no version field at all. The two files are separately installed artifacts, so they can disagree: an upgrade that rewrites `codelima` but leaves an older worker behind, a package layout that installs them in different prefixes, a half-written install directory. When they disagree the frames still parse as JSON; they just mean something else. The failure is silent misinterpretation of a screen, not a rejection.

The same protocol is also where the snapshot cost lives. `SnapshotCell` had two independent declarations — one in the parent package with no JSON tags at all, one in `daemon` with descriptive tags — and the untagged one is what the worker link serialized. A 160×50 grid is 8000 cells published up to 20 times a second per terminal, so the key names *were* the payload: `{"Grapheme":" ","Width":1,"FGDefault":true,…}` repeated 8000 times, 1.8 MB per publish, with `Strikethrough:false` spelled out on every blank cell. Two declarations of the same shape also meant the producer's encoding and the consumer's could drift apart without a compile error.

Both are the same question: what does this protocol promise, and how does a reader know it is being kept?

## Decision Drivers

* Binary skew must fail loudly at the handshake, not quietly in the pixels.
* A version mismatch is a property of two files on disk, not a transient fault; treating it like a crash spends the restart budget rediscovering the same answer.
* Invariant I4: whatever the renderer is doing, the PTY read pump must never block and the journal must keep absorbing.
* Snapshot bytes are on the hot path, and are paid per cell per publish per terminal.
* One shape must have one declaration, or the two ends can drift.

## Considered Options

* Compare build versions out of band (an env var, a file next to the worker, a `--version` probe before spawning).
* Version the frame envelope, on every frame.
* Version the handshake: the worker announces a protocol version in its `init` reply, and the supervisor refuses the link unless it matches exactly.

## Decision Outcome

Chosen option: "Version the handshake", with the per-cell encoding compacted under the same bump.

`init` is already the worker's first frame and already a request/response exchange the supervisor waits on before installing the link, so nothing has been interpreted yet when the check runs. The reply carries `{"protocol":N,"ready":true}`; the supervisor compares it to its own constant and, on any disagreement, fails the link with a distinct error naming both the worker path and the two versions. A worker built before the field existed sends no `protocol` at all, which decodes as 0 — and 0 is treated as a mismatch, because "announces nothing" and "speaks a different protocol" are the same situation.

Mismatch is handled as permanent rather than transient. The supervisor latches it, and the automatic restart path degrades immediately with the mismatch as the recorded cause instead of charging the restart budget; only a forced attempt — the degraded cooldown, or the operator's manual renderer restart — retries, uncharged, because the file on disk may have been replaced since. The terminal lands stale-but-alive, exactly as it does for an exhausted budget: the shell keeps running, the journal keeps absorbing, output and input are rejected with a visible error.

A live update needs no separate check. `BeginHandoff` closes the outgoing daemon's renderer before the PTY is transferred; the handoff carries a PTY, a child PID, geometry and journal replay bytes, and no renderer handle of any kind; and the importing daemon always constructs a fresh supervisor, which resolves the worker beside *its own* executable. A worker therefore cannot outlive the binary that spawned it, and the only skew that can reach an adopted terminal is a stale worker file — which the spawn-time handshake already rejects, falling back to the journal replay that is the ordinary restart path.

Under the same version bump, `SnapshotCell` becomes one declaration in `daemon` (the parent package aliases it) with one- and two-character tags and `omitempty` throughout. The daemon RPC `ProtocolVersion` is bumped alongside it per ADR 65's exact-match policy, since the same cells cross that protocol too.

### Positive Consequences

* On-disk skew produces a stated cause that names the stale file, instead of a corrupted screen.
* A mismatched worker cannot spin the restart budget: the terminal degrades once, with the reason attached, and self-heals if the file is fixed.
* Measured on a 160×50 grid: worker-link cells drop from 1,864,001 to 488,001 bytes (**73.8% smaller**); daemon RPC cells from 720,001 to 488,001 (**32.2% smaller**). At the 20 Hz publish ceiling that is 35.5 MiB/s → 9.3 MiB/s per terminal. The proportions barely move with cell density.
* The cell encoding has exactly one definition, so producer and consumer cannot drift, and the next encoding change is a single edit.

### Negative Consequences

* The wire is no longer human-readable at a glance: `{"g":" ","w":1,"fd":true}` needs the field-name mapping in the struct doc to interpret.
* Two version constants now have to be bumped together whenever a snapshot shape changes, and nothing enforces the pairing beyond review and the golden tests.
* Bumping the daemon `ProtocolVersion` means an in-flight client of the previous build is turned away and must reconnect after the daemon updates — correct, but user-visible.
* A mismatch latches until a forced retry, so a renderer that would have recovered on the next automatic restart no longer gets one. This is deliberate, but it is a behaviour a future transient failure could be misclassified into.

## Pros and Cons of the Options

### Compare build versions out of band

Stamp the build version into an env var or a sidecar file, or run `codelima-renderer-worker --version` before spawning.

* Good, because it can reject a bad worker before a process is even started.
* Bad, because build version is the wrong granularity: it forces a mismatch on every release whether or not the protocol moved, and it says nothing about a worker whose build string was not updated.
* Bad, because a probe is a second process spawn on every renderer start, on a path whose whole budget is a couple of seconds.
* Bad, because it validates a claim about the binary rather than the protocol it actually speaks.

### Version the frame envelope

Put the version in `rendererWorkerFrame` and check it on every frame.

* Good, because no frame can ever be interpreted under an unagreed version.
* Bad, because it pays the check on the hot path — every PTY write, every published screen — to detect a condition that is settled once, at startup, and cannot change while the process lives.
* Bad, because it grows the frame that the compaction above is trying to shrink.

### Version the handshake

The worker announces its protocol version in the `init` reply; the supervisor verifies before installing the link.

* Good, because the check happens exactly once, before anything has been interpreted, at zero steady-state cost.
* Good, because it reuses the exchange the supervisor already blocks on, so no new round trip and no new failure mode in startup ordering.
* Good, because "sent no version" naturally falls out as a mismatch, which is the actual upgrade case.
* Bad, because a worker that lies about its version, or that diverges after `init`, is not caught — the handshake is a compatibility declaration, not a proof.

## Links

* Refines [ADR 108](isolate_ghostty_in_per_terminal_renderer_processes_108.md)
* Relates to [ADR 113](chunk_renderer_replay_during_live_handoff_113.md)
* Relates to [ADR 65](exact_version_json_lines_daemon_protocol_65.md)
