# Route the Codex login callback to the newest listener

Status: Accepted

## Context and Problem Statement

Codex CLI browser authentication returns credentials to a callback server on
`localhost:1455`. CodeLima's dynamic forwarder historically assigned every
generic host port to its first active VM claimant. If an older VM still had a
listener on port 1455 when `codex login` started in another VM, the browser
callback reached the older VM instead of the login process that initiated it.

## Decision Drivers

* Browser authentication must return to the VM where the newest Codex login
  listener appeared.
* Generic routing for ordinary development services must remain stable when a
  second VM starts using the same port.
* Node-qualified `{node}.localhost` routing must remain deterministic.
* The route must recover automatically when the newest callback listener
  disappears.

## Considered Options

* Keep first-claimant routing for every port and require device authentication.
* Move every generic port to its newest claimant.
* Give only the Codex login callback port newest-claimant semantics.

## Decision Outcome

Chosen option: "give only the Codex login callback port newest-claimant
semantics", because the appearance of a new listener on Codex's fixed callback
port is the narrowest available signal of login intent and does not destabilize
ordinary shared development ports.

The daemon continues to assign ordinary generic `localhost` and `127.0.0.1`
routes to the earliest active claimant. For port 1455, it re-evaluates the
claimant on every reconciliation and chooses the route with the newest
discovery time. Node-qualified routes do not use claimant selection. When the
newest port-1455 route disappears, the next-newest active route takes over.

### Positive Consequences

* Starting `codex login` in a second VM moves the browser callback to that VM.
* Existing first-claimant behavior remains unchanged for application ports.
* Callback ownership transfers and recovers within the existing one-second
  reconciliation loop.
* The behavior is observable through the daemon forwarding snapshot's
  `default_node` field.

### Negative Consequences

* Two concurrent Codex browser logins cannot both own the same host callback
  port; the newest listener wins.
* Port 1455 has product-specific routing semantics that must track Codex's
  documented default callback port.
* A VM that deliberately opens port 1455 later can take the generic route;
  generic forwarding is not an authentication boundary, and node-qualified
  routes remain available for non-OAuth traffic.

## Pros and Cons of the Options

### Keep first-claimant routing and require device authentication

* Good, because forwarding has no product-specific behavior.
* Bad, because the default Codex browser login remains broken in a common
  multi-VM workflow.
* Bad, because device authentication may be disabled by account or workspace
  policy.

### Move every generic port to its newest claimant

* Good, because the latest listener consistently wins.
* Bad, because starting a second development server would silently move
  existing `localhost` traffic away from the first VM.

### Use newest-claimant routing only for port 1455

* Good, because it repairs the reported login failure without changing ordinary
  service routing.
* Good, because listener appearance and disappearance are already discovered by
  the daemon.
* Bad, because the callback port is an external product contract.

## Links

* Refines [ADR 109](bind_dynamic_forwarding_to_both_host_loopbacks_109.md).
* OpenAI guidance: [Codex authentication](https://learn.chatgpt.com/docs/auth#login-on-headless-devices).
