# Use the Caller Binary for Cross-Protocol Daemon Update

Status: Accepted

## Context and Problem Statement

`codelima daemon update` accepted an optional replacement path, but an omitted path was resolved inside the running daemon with `os.Executable`. A newly built client therefore asked the old daemon to replace itself with the old daemon binary, allowing a no-op update to report success. This became destructive to usability when semantic paste was added: development builds shared binary version `0.0.0-dev` and protocol 2, so a new TUI could connect to the unchanged daemon, send the new `paste` event type, and have that asynchronous request rejected with no pasted text appearing.

## Decision Drivers

* Make the documented no-argument update install the binary the operator invoked.
* Prevent clients with incompatible terminal request shapes from connecting normally.
* Preserve live PTYs while updating from the immediately previous daemon protocol.
* Keep compatibility limited to the explicit update operation; all other commands remain exact-version.
* Retain rollback when the replacement cannot import or start.

## Considered Options

* Keep resolving an omitted path inside the daemon.
* Require an explicit binary path for every update.
* Stop the daemon whenever binary or protocol versions differ.
* Resolve the caller executable in the CLI and allow an update-only handshake using the running daemon's persisted identity.

## Decision Outcome

Chosen option: "resolve the caller executable in the CLI and allow an update-only handshake using the running daemon's persisted identity", because it gives the no-argument command its expected meaning while retaining authenticated SCM_RIGHTS handoff. Before connecting, the client resolves its own absolute executable path and always sends that path in `daemon.update`. The update client may read the same-home daemon identity and use its recorded binary and protocol versions for the initial `hello`, currently for handoff-capable protocols 2 through 3. If another TUI owns input, the explicit update client sends `input.takeover` before the mutation. The server still verifies the peer UID and resulting input ownership before accepting the update.

All ordinary CLI and TUI clients continue to send the current exact binary and protocol versions. Semantic paste changes the private request contract, so the current daemon protocol is bumped from 2 to 3. A stale protocol-2 daemon therefore rejects normal protocol-3 clients instead of accepting input it cannot understand. Only `daemon update` crosses that boundary, and the replacement importer remains responsible for validating the handoff manifest and rolling back on failure.

### Positive Consequences

* `codelima daemon update` without arguments installs the binary that issued the command.
* A stale development daemon cannot silently discard semantic paste while appearing compatible.
* Protocol-2 terminals can be handed to protocol 3 without stopping their VMs or PTYs.
* Explicit update paths remain supported and are normalized to absolute resolved paths.
* Failed replacement imports leave the old daemon and terminals active.
* An open TUI cannot make an operator-requested update fail merely by owning input.

### Negative Consequences

* The update command has a narrowly scoped compatibility path that ordinary clients intentionally do not have.
* A corrupt or stale identity file can make the first update connection fail; the client retries the current identity before reporting failure.
* Daemons older than the handoff-capable protocol 2 still require a stop/start replacement.
* Existing TUI request/event connections are not transferred; until automatic reconnection lands, the TUI must be reopened after update.

## Pros and Cons of the Options

### Resolve the default inside the daemon

* Good, because the server needs no path from the client.
* Bad, because `os.Executable` necessarily names the old binary.
* Bad, because a successful response does not prove any new code was installed.

### Require an explicit path

* Good, because replacement identity is unambiguous.
* Bad, because it makes the existing no-argument command misleading or invalid.
* Bad, because ordinary rebuild workflows must repeat a path already known to the caller.

### Stop and restart on every mismatch

* Good, because no compatibility handshake is needed.
* Good, because the new daemon starts from a clean process.
* Bad, because live terminal PTYs are lost instead of handed off.

### Send the caller path and bridge only update

* Good, because it preserves the documented command and live handoff.
* Good, because compatibility authority is limited to an explicit mutating operation.
* Good, because normal clients still fail fast on stale protocol state.
* Bad, because update dialing must safely consult persisted daemon identity.

## Links

* Refines [Use an exact-version JSON-lines local daemon protocol](exact_version_json_lines_daemon_protocol_65.md)
* Preserves [Authenticated SCM_RIGHTS daemon handoff](authenticated_scm_rights_daemon_handoff_67.md)
* Makes the protocol boundary explicit for [Preserve bracketed paste across daemon terminals](preserve_bracketed_paste_across_daemon_terminals_81.md)
* Startup recovery later extends compatibility to daemon lifecycle management
  in [ADR 86](use_the_daemon_lock_for_shutdown_and_startup_recovery_86.md).
