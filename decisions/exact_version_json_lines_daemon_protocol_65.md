# Use an exact-version JSON-lines local daemon protocol

Status: Accepted

## Context and Problem Statement

The CLI and TUI need a small local control protocol with typed errors, event delivery, and terminal snapshot support. Client and daemon always ship in the same binary, so compatibility shims would add ambiguity without enabling an independent release cadence.

## Decision Outcome

Use newline-delimited JSON request/response objects over `_daemon/daemon.sock` and events over `_daemon/client.sock`. `hello` must be first and requires an exact binary version plus protocol version. Requests are capped at 1 MiB, handshake connections have deadlines, and remote errors preserve the `AppError` category and exit code. Socket creation requests mode 0600, and the server independently rejects a peer whose kernel-reported effective UID differs from the daemon owner; this preserves the private boundary on shared filesystems that do not honor Unix-socket chmod. One client owns input; later clients observe until an explicit takeover. Protocol 3 adds the semantic terminal-paste request. Daemon lifecycle management is the only compatibility exception: explicit update and startup recovery may authenticate with the running daemon's persisted protocol-2-or-newer identity solely to replace or stop that daemon. Ordinary application RPCs remain exact-version.

### Consequences

* Protocol inspection and tests need only the standard JSON tooling.
* A version mismatch has one remedy: use lifecycle recovery to restart or live-update the daemon.
* Terminal snapshots remain pull-based and carry a generation number for stale-frame rejection.

## Links

* Refined by [ADR 75](persistent_authenticated_daemon_connections_75.md),
  which removes idle deadlines after authentication.
* Refined by [ADR 81](preserve_bracketed_paste_across_daemon_terminals_81.md),
  which batches Vaxis paste keys into bounded semantic terminal events.
* Refined by [ADR 84](use_the_caller_binary_for_cross_protocol_daemon_update_84.md),
  which limits compatibility to explicit replacement and supplies the caller binary.
