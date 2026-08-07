# Reconcile the observation cache on a timer and bypass it for mutating decisions

## Context and Problem Statement

ADR 37 made Lima the runtime-status source for read surfaces, and the daemon later put a `limactl watch` stream in front of it so those reads cost nothing. The resulting observation cache is filled once at observation start and then updated only by watch events — and a healthy watch never ends, so on a daemon that has been up for days the cache is only ever as complete as that one startup list.

Three consequences followed. `limactl watch` reports running/exiting transitions and nothing else, so an instance created, deleted or repaired outside CodeLima never appears, and an entry synthesized from an event carries no SSH config path — which is the metadata dynamic forwarding needs to build a route. A failed *initial* list still marked the cache started, so an empty cache was served as an authoritative "there are no instances", tearing down every route in one reconcile tick. And `NodeStart` consulted the same cache to decide whether the VM was already running, so a stale entry did not produce a stale display, it produced a wrong action: skip the boot, then run bootstrap commands against a machine that is not there.

## Decision Drivers

* Read surfaces must stay cheap; that is the entire reason the cache exists.
* A cache that has never been filled must be distinguishable from a cache that says "empty".
* Drift that the watch stream structurally cannot report has to be repaired by something.
* A decision that mutates a VM must not be made from data the system is allowed to serve stale.

## Considered Options

* Keep the cache as-is and widen the connect-path fallbacks that already work around it.
* Drop the cache and list directly everywhere.
* Reconcile the cache from a full list on a timer, gate its authority on a successful list, and read through it everywhere except decisions that gate a runtime mutation.

## Decision Outcome

Chosen option: "Reconcile the cache from a full list on a timer, gate its authority on a successful list, and read through it everywhere except decisions that gate a runtime mutation", because it fixes the three failures at their source instead of at each call site, and it keeps ADR 37's economics for the reads that motivated the cache in the first place.

Concretely: a reconciliation goroutine owned by the same observation as the watch replaces the cache from `limactl list --json` every 60 seconds, recording-but-preserving the previous cache when a list fails; the cache reports itself non-authoritative until at least one full list has landed, so reads fall through to a direct list rather than being told nothing exists; and `Service` reaches for an optional `ListUncached` on the runtime client for the start decision, which `LimaClient` answers from `limactl list` directly.

### Positive Consequences

* Drift the watch cannot report — new instances, deleted instances, missing SSH metadata on event-synthesized entries — is bounded at one minute instead of unbounded.
* A transient `limactl` failure at daemon startup no longer produces an authoritative empty world.
* Start/stop decisions cost one extra `limactl list` and are correct; every read surface is unchanged.
* `daemon snapshot` now reports whether the cache is authoritative and how many reconciliations have failed, so the condition is diagnosable without a rebuild.

### Negative Consequences

* One `limactl list --json` per minute per daemon, forever, whether or not anything changed.
* Two cache-consultation policies now exist (read vs. mutating decision), and every new call site has to pick one.
* `ListUncached` is an optional interface, so a runtime client that forgets to implement it silently gets cached behavior with no compile-time signal.

## Pros and Cons of the Options

### Keep the cache as-is and widen the connect-path fallbacks

Leave the cache alone and let each consumer that gets burned add its own direct-list fallback, as `ForwardingSSHConfig` already does.

* Good, because it requires no change to the observation lifecycle.
* Good, because the existing fallback demonstrably works for the forwarding path.
* Bad, because it spreads the same workaround across every consumer, and each one has to rediscover the problem first.
* Bad, because it does nothing for drift that no consumer notices — a deleted instance simply stays in the cache.

### Drop the cache and list directly everywhere

Remove the observation cache and answer every read from `limactl list --json`.

* Good, because there is then exactly one source of truth and no staleness to reason about.
* Good, because it deletes code.
* Bad, because it reintroduces a subprocess round-trip into every TUI refresh and every forwarder reconcile tick, which is what ADR 37's batching and the watch stream were built to avoid.

### Reconcile on a timer, gate authority, and bypass for mutating decisions

Keep the cache for reads, repair it periodically, refuse to serve it until it has been filled once, and answer mutation-gating questions from a direct list.

* Good, because each of the three failures is fixed where it originates rather than at its symptoms.
* Good, because the expensive path is taken only by operations that are already about to boot or stop a VM, where one list is noise.
* Good, because "authoritative" becomes an explicit, observable property instead of an implicit consequence of `started`.
* Bad, because it adds a background goroutine and a second consultation policy that future call sites must choose between.

## Links

* Refines [ADR 37](use_lima_as_runtime_status_source_for_read_surfaces_37.md)
* Relates to [ADR 121](degrade_forwarding_state_before_removing_it_121.md)
