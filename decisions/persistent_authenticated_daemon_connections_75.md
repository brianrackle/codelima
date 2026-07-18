# Keep authenticated daemon connections alive while idle

Status: Accepted

## Context and Problem Statement

The TUI holds one authenticated request connection to `_daemon/daemon.sock` for
its lifetime. The server reapplied its 30-second read deadline before every
request, so an otherwise healthy TUI that sat idle for 30 seconds lost the
connection and later received `write: broken pipe` when it tried to open a
terminal. Which parts of the private local protocol should have read deadlines?

## Decision Drivers

* A TUI session must remain usable after arbitrary idle time.
* Unauthenticated connections must not occupy daemon resources indefinitely.
* Event clients must either subscribe promptly or be disconnected.
* The existing exact-version handshake, same-user peer check, frame limit, and
  input-ownership rules must remain unchanged.

## Considered Options

* Reconnect and replay every failed client operation.
* Increase the idle timeout.
* Limit read deadlines to authentication and event subscription.

## Decision Outcome

Chosen option: "limit read deadlines to authentication and event
subscription", because authenticated request connections represent active TUI
sessions even when they are temporarily idle.

The configured read timeout bounds the initial `hello` on both sockets and the
interval between event-socket authentication and `events.subscribe`. After a
request client authenticates, or an event client subscribes, the server clears
the connection deadline. Request processing, frame-size validation, exact
version checks, and peer authorization are unchanged.

### Positive Consequences

* Opening a terminal or changing a node works after the TUI has been idle.
* No retry layer can accidentally duplicate a mutating request.
* Unauthenticated and unsubscribed connections remain bounded.

### Negative Consequences

* An authenticated same-user client may keep a daemon connection open until it
  disconnects or the daemon stops.
* Recovery from an actual daemon restart still requires a new client session.

## Pros and Cons of the Options

### Reconnect and replay failed operations

* Good, because it could also recover from a daemon restart.
* Bad, because replaying a request after an ambiguous write can duplicate a
  mutating operation.
* Bad, because it treats a server-created idle disconnect as a client concern.

### Increase the idle timeout

* Good, because it reduces how often the failure occurs.
* Bad, because every finite idle timeout eventually breaks a long-lived TUI.

### Limit deadlines to authentication and subscription

* Good, because deadlines protect only protocol phases that must make prompt
  progress.
* Good, because normal request connections remain simple and stateful.
* Bad, because authenticated idle connections consume a small descriptor and
  goroutine until their client exits.

## Links

* Refines [ADR 65](exact_version_json_lines_daemon_protocol_65.md).
* Preserves daemon ownership from
  [ADR 64](daemon_owned_terminal_runtimes_64.md).
