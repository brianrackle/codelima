# Observe global host capacity outside manager rendering

## Context and Problem Statement

The fleet design displays host CPU, memory and free space alongside allocations
to active VMs. Directory scopes, search filters and bootstrap failures must not
make those allocations disappear. Host sampling must not block the event loop
or create a Lima home during a read.

## Decision Drivers

* Use real observations and represent missing measurements explicitly.
* Include failed nodes whose runtime is still up.
* Keep filesystem work outside rendering and input handling.
* Support Linux and macOS without new runtime executables or dependencies.

## Considered Options

* Infer host capacity from configured node sizes or reference data.
* Execute platform commands in the renderer.
* Read native host information in the existing refresh worker and join it with
  the complete node snapshot before applying directory scope.

## Decision Outcome

Use the refresh worker. The `internal/hostinfo` package reads Linux procfs or
macOS sysctl for identity and memory, the Go runtime for usable logical CPUs,
and statfs for available space on the LIMA_HOME volume. A nonexistent home uses
its nearest existing ancestor without creating directories. Unavailable values
are nil; capacity is not a measurement of instantaneous host consumption.
Linux block counts use fragment size (`f_frsize`), which can differ from the
preferred transfer size on VirtioFS; Darwin uses `f_bsize`.

Load the global authoritative node list once. Sum frozen CPU and memory sizes
only for existing runtimes observed running or installing, independently of
node lifecycle status. Include failed-but-running VMs and report nodes lacking
an observation as unknown. Scope the fleet after this join. Publish both values
in the same generation-tagged completion event; an older refresh cannot replace
the current capacity snapshot. A failed node-list read marks allocations
unavailable while retaining the independently measured host capacity.

System presents Doctor, Daemon, Host and Settings panels. Wide, tall terminals
show all four; smaller terminals show the focused panel, with Tab/Shift-Tab and
mouse navigation. Scrollable panel contents remain reachable after resize.
Daemon metadata is read through the existing RPC outside the event loop.

### Positive Consequences

* Filters and scopes cannot underreport configured VM allocations.
* Native observations require no subprocesses or persistent host state.
* Missing values remain distinct from a measured zero.
* The sampler can be compiled for both macOS architectures independently of
  native terminal-renderer dependencies.

### Negative Consequences

* Capacity updates follow lifecycle pushes and the existing refresh safety net;
  this is not an instantaneous host utilization monitor.
* Only CodeLima-managed VMs are included in the allocation total.
* Native macOS runtime readings still require macOS qualification.

## Pros and Cons of the Options

Reference/configuration-derived capacity would be cheap but misleading. Commands
inside rendering would add latency and platform dependencies. Native reads in
the existing worker keep observations explicit and presentation deterministic.

## Links

* [Fleet manager](../plans/tui-vnext/README.md)
* [Editable settings](persist_manager_preferences_and_launch_managed_shells_152.md)
* [Linux filesystem statistics](https://man7.org/linux/man-pages/man2/statfs.2.html)
