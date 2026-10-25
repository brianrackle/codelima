# Gate application routes and own published TCP in the daemon

## Context and Problem Statement

The Ports and Starting/Failed designs hold application routes until a node is
ready. Discovery previously admitted every running VM, and frozen HOST:GUEST
mappings became static Lima forwards during boot. Hiding those routes in the
TUI would not enforce readiness. Telemetry must remain available during setup.

## Considered Options

* Gate discovery only and retain Lima's static forwarding.
* Stop running VMs automatically to replace their forwarding configuration.
* Move raw TCP forwarding into the daemon and migrate stopped instances.

## Decision Outcome

Choose daemon-owned published TCP with migration before an explicit node start.
New Lima templates disable all Lima application forwarding. The frozen node
record keeps each HOST:GUEST mapping. After successful creation, or after a
successful `limactl edit --tty=false --set` on a stopped instance, persist
`daemon_port_forwarding: true`. Clone records inherit the source marker.
Migration preserves other Lima settings and never restarts a running VM.
An unmarked running legacy VM retains its existing Lima mappings; callers must
report that exception until the user stops and starts it.

Application discovery requires completed bootstrap and a ready lifecycle.
Provisioning, registration, failure and termination remove application routes,
while guest probing and telemetry continue. A cached stopped observation alone
does not revoke a previously ready responsive guest: the existing confirmed-stop
grace remains authoritative.

Each published host port reserves both available loopback families and relays
bytes over the existing node SSH peer to its configured guest port, including
ports below 1024. A bind failure releases any partial bind and retries on the
next reconciliation. Multiple ready nodes reserving the same port fail closed.
Raw TCP reserves the entire host port; overlapping HTTP hostname routes report
a conflict instead of silently reaching the TCP owner. A lapsed tunnel retains
the listener but closes new TCP connections until recovery. Confirmed stop,
delete, failed readiness and daemon shutdown close listeners, cancel pending
dials and drain owned connections. Half-close preserves responses written after
a client finishes sending. Daemon restarts interrupt existing TCP connections.

The diagnostic snapshot includes `published_tcp`, and System counts include
these listeners. The interactive Ports projection, pin persistence and shared
Activity representation remain part of the ongoing Ports delivery.

### Positive Consequences

* Readiness controls HTTP and published TCP without suppressing setup telemetry.
* Remapped ports, binary protocols and half-close use one daemon-owned lifecycle.
* Existing running VMs retain their sessions until an explicit migration.

### Negative Consequences

* Raw TCP now depends on the daemon, and active streams end during replacement.
* Running legacy mappings remain an explicit readiness exception.
* A published mapping prevents hostname multiplexing on that host port.

## Pros and Cons of the Options

Discovery-only gating is smaller but leaves bootstrap services exposed through
static mappings. Automatic VM restarts would make migration uniform at the cost
of interrupting user workloads. Daemon ownership supplies consistent readiness
and recovery while requiring connection lifecycle management and legacy labels.

## Links

* [Lima 2.1 edit implementation](https://github.com/lima-vm/lima/blob/v2.1.0/cmd/limactl/edit.go)
* [Ports reference](../plans/tui-vnext/reference/Ports.dc.html)
