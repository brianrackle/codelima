# Chunk Renderer Replay During Live Daemon Handoff

## Context and Problem Statement

Live handoff version 3 encoded every terminal's renderer replay inside one JSON
manifest capped at 1 MiB. A terminal may retain 1 MiB of raw replay by design,
and JSON base64 expands that single journal to roughly 1.4 MiB before adding
session metadata. A real update therefore rolled back with `handoff message
size 1469626 is outside 1..1048576`.

## Decision Drivers

* Full bounded renderer journals must survive live update without restarting
  their shell processes.
* Every individual handoff frame must remain below the existing 1 MiB parser
  and allocation bound.
* Replay byte counts and aggregate allocations must remain explicitly bounded.
* PTY descriptor authentication, staging, commit, and rollback must not change.
* A new importer should still accept the previous stream format when its old
  sender can produce a valid frame.

## Considered Options

* Raise the maximum size of every daemon protocol message.
* Reduce renderer journal capacity until one encoded manifest usually fits.
* Keep a metadata-only manifest and transfer each replay in bounded chunks.

## Decision Outcome

Chosen option: "keep a metadata-only manifest and transfer each replay in
bounded chunks", because it preserves the full terminal recovery window without
weakening the general daemon protocol bound or relying on terminal count.

Handoff version 4 replaces the inline replay field with its raw byte count.
After the manifest, the sender emits ordered replay frames for each terminal.
Each frame carries at most 512 KiB of raw bytes, keeping its base64-expanded
JSON below the existing 1 MiB frame maximum. The receiver requires the expected
terminal ID and exact contiguous offset, rejects descriptors on replay frames,
and rejects empty, oversized, overrun, or out-of-order chunks.

One terminal remains capped at 1 MiB of replay, and one handoff is capped at 64
MiB total. The renderer journal imports the same per-terminal constant so the
producer and transport cannot drift. Descriptor batches continue only after all
declared replay bytes arrive.

A version-4 stream importer also accepts version-3 inline manifests that fit
the old bound. Version-2 `unixpacket` compatibility remains restricted to that
legacy transport. A running version-3 daemon whose manifest already exceeds its
own compiled 1 MiB sender limit cannot self-upgrade: it rolls back safely, and
the operator must either close enough high-history terminals to fit the old
manifest or perform a terminal-restarting daemon stop/start once.

### Positive Consequences

* A full 1 MiB terminal journal transfers in multiple bounded frames.
* Multiple terminal replays no longer compete for one manifest frame.
* General request/response messages remain capped at 1 MiB.
* Malformed replay ordering and allocation requests fail before descriptor
  adoption.
* Existing handoff rollback and shell-preservation semantics remain unchanged.

### Negative Consequences

* Handoff has another independently versioned wire format.
* Import holds bounded replay bytes until matching PTYs arrive.
* Version-3 daemons already above their sender limit need a one-time operator
  recovery choice because candidate code cannot change the running sender.
* A handoff with more than 64 MiB of total replay is rejected explicitly.

## Pros and Cons of the Options

### Raise every daemon message limit

* Good, because it is a small code change.
* Bad, because one terminal already consumes more than the old bound after
  base64 expansion and multiple terminals remain unbounded as a group.
* Bad, because unrelated daemon clients would inherit a much larger allocation
  surface.

### Reduce renderer journal capacity

* Good, because smaller replay lowers update memory and bandwidth.
* Bad, because even modest multi-tab sessions can exceed one shared manifest
  frame.
* Bad, because renderer recovery and live-update fidelity would regress.

### Chunk replay after a metadata-only manifest

* Good, because every frame retains the established bound.
* Good, because replay capacity and terminal count no longer multiply inside
  one JSON value.
* Good, because ordering and declared sizes are independently validated.
* Bad, because sender and receiver need an additional replay phase.

## Links

* Refines [Use framed Unix streams for portable daemon handoff](use_framed_unix_streams_for_portable_daemon_handoff_85.md).
* Preserves [Transfer live PTYs with authenticated SCM_RIGHTS handoff](authenticated_scm_rights_daemon_handoff_67.md).
* Preserves renderer reconstruction from [Isolate Ghostty in per-terminal renderer processes](isolate_ghostty_in_per_terminal_renderer_processes_108.md).
