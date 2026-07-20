# Claim Daemon Input When an Interactive TUI Connects

## Context and Problem Statement

The daemon grants input to the first connection that requests it and makes later input-seeking clients observe-only. `Service.connectTUIDaemon` accepted an observe-only handshake as a successful TUI connection, so the TUI could start, perform non-terminal work, sit idle, and only discover the missing ownership when `terminal.open` failed with a request for manual `input.takeover`.

## Decision Drivers

* An interactive TUI must be ready to open and control terminals as soon as it starts.
* The failure must not be deferred until the first terminal action.
* A newer explicitly launched TUI should supersede a stale or older interactive client.
* Ownership revocation must remain visible to the previous client.
* Authenticated connections must continue to survive arbitrary idle time.

## Considered Options

* Leave ownership explicit and show instructions to run `terminal takeover`.
* Retry `terminal.open` by taking ownership only after an observe-only error.
* Complete TUI connection setup by taking ownership whenever `hello` reports observe-only.

## Decision Outcome

Chosen option: "Complete TUI connection setup by taking ownership whenever `hello` reports observe-only", because an interactive TUI is an input client by definition and should establish that invariant before rendering a usable session.

### Positive Consequences

* A TUI never enters its event loop as an observe-only request client.
* Terminal creation succeeds after idle time without a manual takeover.
* The daemon broadcasts `input.revoked` to the former owner, which remains connected for observation.
* The initial owner path avoids an unnecessary takeover call.
* Mutating terminal calls are not automatically replayed after ambiguous failures.

### Negative Consequences

* Launching a second TUI immediately revokes input from the first TUI.
* Users who intentionally want a read-only TUI do not currently have a separate TUI mode.

## Pros and Cons of the Options

### Keep manual takeover

* Good, because ownership never changes without a separate explicit command.
* Bad, because the TUI appears healthy until a later terminal action fails.
* Bad, because ordinary interactive use exposes a protocol-level recovery step.

### Take ownership after a failed terminal open

* Good, because ownership changes only when input is first needed.
* Bad, because retrying a mutating request after a transport failure can duplicate work.
* Bad, because the TUI still begins in a state that cannot satisfy its advertised controls.

### Take ownership during TUI connection

* Good, because connection success guarantees interactive capability.
* Good, because takeover is a distinct idempotent protocol request before any terminal mutation.
* Bad, because the newest TUI wins even if an older TUI remains active.

## Links

* Refines [Keep authenticated daemon connections alive while idle](persistent_authenticated_daemon_connections_75.md)
* Preserves [Exact-version JSON-lines daemon protocol](exact_version_json_lines_daemon_protocol_65.md)
* Refined by [Reclaim daemon input when a TUI window gains focus](reclaim_daemon_input_when_a_tui_window_gains_focus_80.md)
