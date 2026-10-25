# Observe guest architecture and KVM access in existing telemetry

## Context and Problem Statement

Overview's Identity panel needs guest architecture and KVM availability. The
runtime's nested-virtualization setting describes configuration; it cannot
prove that the guest exposes `/dev/kvm` to its SSH user.

## Considered Options

* Extend the existing guest telemetry sample with architecture and device access.
* Infer both values from host architecture and nested-virtualization settings.
* Add a separate platform probe, cache and refresh schedule.

## Decision Outcome

Extend the current SSH telemetry command with `uname -m` and shell tests for a
readable/writable `/dev/kvm` character device. There are no new connections,
timers or routing dependencies. The existing telemetry user and timeout apply.
This is device accessibility, not a claim that a nested VM has booted.

Parse architecture and KVM independently. Normalize common kernel architecture
names to Go-style names (`aarch64` to `arm64`, `x86_64` to `amd64`); bound other
names to 32 inert ASCII characters. Invalid or missing fields stay unknown,
and an invalid later duplicate clears the earlier value. KVM has three states:
accessible, inaccessible and unknown. Absence is never treated as false.

Carry these facts in current `NodeUsageDetails`, using the same immutable copy,
push/poll and clearing paths as other telemetry. Do not persist them in node
metadata or duplicate them into every historical metric point. Ordinary daemon
protocol becomes 31; session/renderer/handoff versions remain 2/6/7.

Overview shows a separate guest row so architecture and KVM access remain
visible beside long image paths at compact widths. Configured nested
virtualization stays in the runtime row. Expired, future, stopped or disconnected
samples are explicitly last observations. Missing values remain question marks.

### Positive Consequences

* Guest facts come from the actual guest and sampling identity.
* Both widths can distinguish configuration, access and unavailable evidence.
* Existing sampling, transport and lifecycle ownership are reused.

### Negative Consequences

* Device access is a necessary condition, not an end-to-end virtualization test.
* Stopped guests with no retained sample have unknown platform observations.
* Ordinary clients need an explicit update of an older daemon.

## Pros and Cons of the Options

### Extend telemetry

Adds a small read to an existing sample and inherits its freshness contract.
It cannot provide live guest information when that sample is unavailable.

### Infer from configuration

Avoids guest reads but confuses host/configuration intent with guest facts.

### Separate probe

Allows an independent refresh rate but adds scheduling and stale-state ownership
for information already available through the telemetry connection.

## Links

* Extends [selected preview evidence](read_selected_fleet_preview_from_retained_metrics_and_runtime_configuration_188.md).
* Verification: `make test-metrics METRICS_TEST_FLAGS=-race`, `make test-overview`
  and the Overview identity checks in `QA.md`.
