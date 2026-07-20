# Reclaim Daemon Input When a TUI Window Gains Focus

## Context and Problem Statement

ADR 78 makes a newly connected interactive TUI the daemon input owner. When two TUI windows remain open, however, the newer connection revokes the older window and the older window stays observe-only after the user returns to it. Its next terminal action then fails with `client is observe-only; request input.takeover first`, even though restarting that TUI appears to fix the problem by repeating connection-time takeover.

## Decision Drivers

* Switching between open interactive TUI windows must not require restarting either process.
* The window the user intentionally focuses should become the authoritative input client.
* Background refresh, rendering, terminal polling, and resize activity must not steal ownership.
* Expected focus handoffs must not appear as persistent errors in the TUI footer.
* Recovery must not replay a terminal mutation whose outcome could be ambiguous.
* The existing single-owner and revocation protocol should remain unchanged.

## Considered Options

* Keep ownership fixed at TUI connection time.
* Detect an observe-only terminal error, take over, and retry the failed mutation.
* Send the idempotent `input.takeover` request when Vaxis reports that the host window gained focus.

## Decision Outcome

Chosen option: "Send the idempotent `input.takeover` request when Vaxis reports that the host window gained focus", because focus is an explicit user handoff that happens before the next key or mouse action and does not confuse background terminal activity with user intent.

The TUI always sends takeover on `vaxis.FocusIn`; it cannot rely on `hello.input_owner`, because that value only describes connection-time ownership and becomes stale after another client takes over. The TUI event stream does not subscribe to input events because `input.revoked` is an expected consequence of a successful focus handoff, not a user-actionable error. A failed focus takeover is surfaced in TUI status without closing the client. Terminal mutations are not automatically replayed.

### Positive Consequences

* Returning to any still-running TUI window makes it interactive before its next terminal action.
* Repeated switching between windows follows host focus and keeps exactly one daemon input owner.
* Routine switching does not leave an ownership-revoked error in either window.
* Idle authenticated connections remain reusable without a restart.
* Background redraw and polling cannot create ownership ping-pong.
* The daemon protocol, wire version, and ownership rules do not change.

### Negative Consequences

* Each host window-focus transition performs one local daemon request.
* Unexpected revocation is discovered on the next terminal mutation rather than announced proactively.
* Input handoff depends on the terminal emitting the focus event enabled and decoded by Vaxis.
* A user cannot keep host focus on a TUI while intentionally leaving it observe-only.

## Pros and Cons of the Options

### Keep ownership fixed at TUI connection time

* Good, because no additional request is sent after connection.
* Bad, because returning to an older window leaves its interactive controls unusable.
* Bad, because restarting the process remains the only automatic recovery path.

### Retry after an observe-only terminal error

* Good, because clients that never mutate do not request ownership again.
* Bad, because the user still sees a failed first action.
* Bad, because generalized retry after a transport or response failure can duplicate a terminal mutation.
* Bad, because background mutations such as resize or focus synchronization can become accidental ownership triggers.

### Take over on host window focus

* Good, because the handoff matches explicit user attention.
* Good, because takeover is distinct, idempotent, and precedes the next terminal mutation.
* Good, because existing daemon revocation makes the previous window observe-only.
* Bad, because the focus event becomes part of the interactive ownership contract.

## Links

* Refines [Claim daemon input when an interactive TUI connects](claim_daemon_input_when_an_interactive_tui_connects_78.md)
* Preserves [Exact-version JSON-lines daemon protocol](exact_version_json_lines_daemon_protocol_65.md)
