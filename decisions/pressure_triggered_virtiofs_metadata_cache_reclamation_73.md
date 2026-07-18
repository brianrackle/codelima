# Reclaim guest filesystem metadata caches under macOS host file pressure

Status: Accepted

## Context and Problem Statement

Apple Virtualization.framework's VirtioFS implementation can retain host file descriptors for guest dentries and inodes until Linux sends FUSE forget requests. Large mounted-workspace traversals can therefore exhaust the macOS file table and prevent the Microsandbox daemon from opening its node state, even though the guest is otherwise healthy. CodeLima needs a bounded mitigation that preserves live read/write mounted-workspace semantics.

## Decision Drivers

* Keep mounted workspaces live and read/write; copy-mode synchronization is not an acceptable substitute.
* Intervene before macOS returns `ENFILE` while avoiding continuous guest cache churn.
* Never force dirty writes or pause active filesystem operations.
* Limit the workaround to the affected macOS host integration and expose its state for diagnosis.
* Bound repeated cache churn without delaying retries for several minutes.

## Considered Options

* Wait for an Apple or Microsandbox VirtioFS fix.
* Drop guest filesystem metadata caches on a fixed timer.
* Trigger guest metadata-cache reclamation from macOS host file-table pressure and verify its effect.
* Run `sync` and drop all guest caches under pressure.

## Decision Outcome

Chosen option: "trigger guest metadata-cache reclamation from macOS host file-table pressure and verify its effect." The daemon samples the host-wide `kern.num_files` and `kern.maxfiles` every two seconds on macOS. At 20 percent utilization it asks every running mounted node to execute `echo 2 > /proc/sys/vm/drop_caches`, then samples the host again. Every attempt has a 30-second cooldown; there is no separate ineffective-reclaim backoff. The setting is enabled by default, configurable from 1 through 95 percent, and visible in `daemon snapshot`.

The command intentionally omits `sync`. Linux value `2` targets clean, reclaimable slab objects such as dentries and inodes; active objects and dirty data remain pinned. The intervention can make subsequent path lookup slower while caches warm again, but it does not change mounted-workspace consistency semantics.

### Positive Consequences

* VirtioFS can release large sets of retained host descriptors before global exhaustion.
* Mounted workspaces remain the source of truth and stay available during reclamation.
* Host sampling is cheap and no guest command runs below the configured threshold.
* Effect verification distinguishes useful VirtioFS reclamation from unrelated system pressure.
* Snapshot state makes the threshold, last attempt, reclaimed nodes, released files, and errors inspectable.

### Negative Consequences

* This mitigates an Apple VirtioFS retention problem rather than correcting its underlying FUSE-forget behavior.
* Filesystem metadata lookups can be temporarily slower after a reclaim.
* System-wide host pressure is an indirect signal; unrelated processes can cause a reclaim attempt.
* A two-second polling window and percentage threshold cannot guarantee intervention before every abrupt exhaustion event.
* Linux and non-Apple virtualization hosts report the feature as unsupported.

## Pros and Cons of the Options

### Wait for an upstream fix

* Good, because it adds no host-specific policy to CodeLima.
* Good, because a framework fix can release descriptors at the correct FUSE lifecycle boundary.
* Bad, because current mounted-workspace sessions can still exhaust the host file table.
* Bad, because CodeLima cannot control the Apple framework or Microsandbox release schedule.

### Drop metadata caches on a fixed timer

* Good, because it does not require host pressure APIs.
* Good, because it bounds the lifetime of reclaimable guest metadata.
* Bad, because it imposes repeated cache-warmup cost even when the host has ample capacity.
* Bad, because a long interval can still miss a fast traversal while a short interval creates churn.

### Trigger and verify metadata-cache reclamation

* Good, because work occurs only after measurable host pressure.
* Good, because `drop_caches=2` is narrow, non-destructive, and effective for the observed VirtioFS retention.
* Good, because before/after sampling exposes whether an attempt actually released host file-table entries.
* Bad, because global file-table utilization does not identify which process owns the descriptors.

### Run `sync` and drop all caches

* Good, because it maximizes the amount of immediately reclaimable guest cache.
* Bad, because `sync` forces unrelated dirty writes and can cause large latency spikes.
* Bad, because dropping page cache is broader than the dentry/inode problem.
* Bad, because durability policy should remain with applications and normal kernel writeback.

## Links

* Extends daemon-owned runtime responsibilities from [ADR 64](daemon_owned_terminal_runtimes_64.md).
* Preserves the mounted workspace contract established by [ADR 55](replace_lima_with_microsandbox_as_sole_runtime_55.md).
