# Route clipboard writes to the seat owner's event connection

## Context and Problem Statement

After the libghostty-vt adoption, a short OSC 52 probe worked directly in host
Ghostty but failed inside CodeLima. The daemon's private clipboard delivery
required the input-owning physical connection to be subscribed to events.
The TUI uses separate request and event connections, so no connection qualified.
A regression test using the actual TUI connection supervisor reproduced the
drop before the change and delivers clipboard events after it.

## Decision Drivers

* Restore guest clipboard writes through the outer terminal's OSC 52 handling.
* Deliver only to the current seat's frontend, without broadcasting to observers.
* Preserve physical input ownership, bounded queues and no replay on reconnect.
* Prevent an old event stream from replacing or outliving its successor as a
  clipboard recipient.

## Considered Options

* Route to the owner's current subscribed event connection.
* Broadcast clipboard events to all subscribers.
* Move input ownership onto the event connection.

## Decision Outcome

Chosen option: "Route to the owner's current subscribed event connection".
The daemon registers the newest successfully synchronized event connection ID
for each frontend instance. Request and event connections already share that
instance ID. The physical request connection continues to hold the input lease.

Clipboard admission captures the lease, epoch and event recipient and rechecks
all three under the server mutex before queuing. A later subscription on an
older connection cannot replace a newer recipient. Retain the recipient ID
while any connection for the instance remains, even if the recipient disconnects,
so older streams cannot become a fallback. Remove the record when the last
connection for the instance closes. A new subscription registers its own ID;
past clipboard effects are never replayed.

The existing event wire format, zero shared-state sequence, limits and OSC 52
host delivery stay in place. There is no protocol change or native clipboard
command preference.

### Positive Consequences

* Normal TUI clipboard requests can reach their event consumer.
* Observer windows and superseded connections do not receive private effects.
* Real socket tests cover delivery across the request/event split and reconnect.
* A CLI integration test exercises a real shell's OSC 52 output through the
  built native renderer and daemon before and after event reconnection.

### Negative Consequences

* The daemon retains one recipient ID per connected frontend instance.
* Copies during disconnect or takeover may still be discarded rather than
  replayed; OSC 52 does not acknowledge host clipboard completion.
* Physical Ghostty, multi-window and SSH clipboard verification is still needed.

## Pros and Cons of the Options

### Route to the owner's current subscribed event connection

* Good, because it matches the existing TUI transport and ownership contracts.
* Bad, because it requires tracking the event connection separately from input.

### Broadcast clipboard events to all subscribers

* Good, because it reaches the TUI's event connection without extra tracking.
* Bad, because unrelated attached frontends could overwrite their clipboards.

### Move input ownership onto the event connection

* Good, because the original delivery predicate would find a subscriber.
* Bad, because geometry and focus requests on the request connection would lose
  their physical lease and require broader changes to input arbitration.

## Links

* Amends the clipboard connection rule in
  [ADR 132](adopt_static_libghostty_vt_with_bounded_worker_contracts_132.md).
* Preserves [OSC 52 host delivery](sync_guest_clipboard_with_osc52_46.md).
* Manual qualification remains in [TODO 55](../TODO.md).
