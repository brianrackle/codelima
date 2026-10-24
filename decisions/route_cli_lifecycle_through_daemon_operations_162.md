# Route CLI lifecycle work through durable daemon operations

## Context and Problem Statement

ADR 161 moved TUI lifecycle work into the daemon, but CLI commands still ran
inline. Closing a CLI could interrupt a mutation, and other frontends could
not inspect or cancel it through shared Activity. A daemon executes in its
own working directory, so forwarding an unresolved create path would also
change the meaning of `node create --directory .`.

## Decision Drivers

- Give CLI and TUI mutations the same execution lifetime and arbitration.
- Preserve CLI result envelopes, typed errors and caller-relative paths.
- Keep help and metadata reads local, and preserve direct SDK behavior.
- Never replace an operation-owning daemon implicitly during startup.

## Considered Options

- Keep CLI mutations inline with separate filesystem arbitration.
- Route CLI lifecycle commands through the existing operation protocol.
- Make every SDK service and metadata command require a daemon.

## Decision Outcome

Use the existing operation protocol for CLI node create, start, retry, stop,
clone and delete. Configure a lazy connection only on these command paths;
argument parsing and help do not start a daemon. Respect daemon autostart and
never fall back to inline execution after a connection error. Direct SDK
services continue inline; the daemon's execution copy clears both frontend
connection mechanisms to prevent recursive admission.

Resolve create directories in the caller before submission. Deliver retained
progress to CLI stderr, leaving stdout for the existing result envelope. A
lost submission response is ambiguous: return its client-generated operation
ID and inspection guidance. Losing an admitted operation's wait has the same
guidance and preserves the underlying cancellation error. These failures use
the existing dependency-unavailable exit code; they do not claim the operation
failed or cancel it. Completed failures keep their stored typed error.

Add `operation list` (also the group default), `operation show ID` and
`operation cancel ID`. Listing supports bounded history cursors and always
includes running work. Cancel acknowledges the request; users inspect the
receipt to learn whether cleanup finished. The commands reuse protocol 11.

If startup finds a responsive protocol-11-or-newer daemon, require explicit
`daemon update` instead of automatically stopping it after a version mismatch.
Checking its active work and then stopping it would leave an admission race.
The explicit update path already excludes admission while deciding whether
replacement is allowed. Recovery of older or unresponsive daemons retains its
existing behavior.

### Positive Consequences

- CLI work survives disconnects and appears in the same Activity history.
- Operators can inspect and cancel receipts without opening the TUI.
- Existing SDK callers retain their context-controlled execution contract.

### Negative Consequences

- CLI mutations now require a compatible daemon; disabled autostart requires
  an explicit start, and version mismatch requires explicit update.
- CLI process exit no longer cancels admitted work. Scripts that need
  cancellation must use the explicit receipt command.
- General Activity messages and phase-level progress remain separate work.

## Pros and Cons of the Options

### Inline CLI work

Keeps the old dependency footprint, but disconnects can interrupt mutations
and shared Activity cannot represent the complete operation set.

### Existing operation protocol

Reuses tested persistence and arbitration without a wire change. It requires
clear disconnect semantics and daemon connection errors in the CLI.

### Daemon-dependent SDK and metadata commands

Would unify all call paths, but unnecessarily changes embedded callers and
prevents metadata inspection when runtime infrastructure is unavailable.

## Links

- Extends [ADR 161](daemon_owned_node_operations_and_retained_history_161.md).
