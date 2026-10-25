# Own node restart across stop and start

## Context and Problem Statement

The fleet design includes restart for selected and marked nodes. The existing
`r` binding only resumed failed bootstrap. Independent frontend stop and start
commands would release ownership between shutdown and boot and depend on the
frontend staying alive.

## Considered Options

* Independent stop and start requests.
* A guest reboot that bypasses the existing lifecycle checks.
* One daemon restart receipt retaining the node's lifecycle token.

## Decision Outcome

Ordinary protocol 23 adds `restart`. Resolve the node and hold one liveness token
while the existing uncached stop confirms shutdown and the existing start path
boots and validates it. Internal start/stop helpers require the caller to own
that token. No metadata lock spans runtime work. Cancellation or a failed stop
prevents boot. Completed bootstrap stays frozen; incomplete setup uses ordinary
start semantics. Explicit retry remains the suffix-resume action.

One receipt contains Stop and the startup phases, persisted before side effects.
Both stop and boot streams are retained once. Restart participates in Activity,
latest-start inspection, readiness and cancel-and-stop. Caller exit only ends
waiting; explicit daemon shutdown records interruption without replay.

CLI `node restart` shares this admission. TUI restart honors stop-confirmation
preferences and lists marked targets once. Detail actions affect only the open
node, regardless of fleet marks. Unmarked failed nodes keep `r` for retry;
marked restart performs full restart. The existing sequential frontend queue
submits individual batch requests; durable ownership of the entire queue remains
separate work in TODO #57.

### Positive Consequences

* Competing lifecycle operations cannot enter between stop and start.
* Observation, bootstrap, readiness and cancellation rules are reused.
* Other frontends can inspect and cancel the admitted restart.

### Negative Consequences

* Restart ends guest sessions and requires normal stop confirmation.
* Older clients require the normal explicit daemon update for protocol 23.
* Shared helper changes require full lifecycle regression coverage.

## Pros and Cons of the Options

Independent requests are simple but leave an ownership and lifetime gap. Guest
reboot avoids full shutdown but bypasses configuration migration and readiness
validation. One receipt preserves established behavior and durable progress at
the cost of a new operation kind and an internal helper ownership contract.

## Links

* [Fleet interactions](../plans/tui-vnext/reference/canvas.json)
* [Delivery ledger](../plans/tui-vnext/README.md)
