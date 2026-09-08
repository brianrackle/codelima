# libghostty-vt adoption measurements

2026-09-07. Exact upstream `82232ecde55405559dec29c5466cb9e39938cb41`, Zig 0.16.0, ReleaseSmall, Linux aarch64, static CodeLima profile including the three owned patches. The fixture is `internal/ghostty/testdata/ghostty_compression_bench.c`; reproduce with `make benchmark-ghostty-compression`.

## Idle compression

Four paired fresh-process runs loaded 10,000 repeated physical rows into a 200-column, 32-row terminal, then compared no compression with caller-driven incremental compression. Current residency comes from Linux `/proc/self/statm`, not peak RSS or Go heap accounting. Input timings cover native ingestion only, not daemon IPC, PTY I/O, or UI presentation.

| Observation | Compression disabled | Incremental compression |
| --- | --- | --- |
| RSS immediately before measurement | 18,501,632–18,505,728 bytes | 18,501,632–18,505,728 bytes |
| RSS after measurement | 18,632,704–18,636,800 bytes | 2,924,544–2,928,640 bytes |
| Native incremental steps | 0 | 48 |
| Total compression CPU/wall time | — | 1.22–1.33 ms |
| Longest individual step | — | 53–71 μs |
| Resumed input p95, 1,000 short writes | 42 ns | 42 ns |
| Longest resumed input write | 0.42–0.83 μs | 1.38–1.79 μs |
| First frame after scrolling to old history | 0.28–0.34 ms | 0.33–0.35 ms |

The clock is visibly quantized: nanosecond output should not be read as nanosecond measurement precision. The repeated-content fixture demonstrates approximately 84% lower residency, not a universal workload guarantee. Native allocation counts are not exposed by the public API and were not inferred from Go allocation statistics.

Decision: enable actor-owned idle compression by default, with `CODELIMA_GHOSTTY_IDLE_COMPRESSION=0` as a disable switch. Start after 500 ms without a changed native activity token; perform one native incremental step per timer firing, yielding 10 ms between pending steps. Stop the timer after completion until activity changes. This bounds each foreground interruption and removes recurring wakeups once caught up; a 48-step pass therefore produces 48 scheduled compression wakeups, not an always-running ticker. Existing 64 MiB/10,000-row native history limits remain in effect.

Native contract tests cover formatter/frame ownership, semantic paste/effects, strict checkpoint continuation/FINISH handling, selection/search, static image generations/geometry/deletion, disabled animation/file medium, and checkpoint ineligibility for images on either screen or an unfinished image transfer. Go tests additionally exercise the actual PNG callback and allocator-owned RGBA conversion, payload-free frame metadata, checkpoint eligibility after deletion, and idle scheduling/history equivalence.

Qualification limits: native Darwin measurements, mixed high-entropy history, multi-worker aggregate residency, and end-to-end loaded UI latency remain separate qualification work. Do not claim those were measured by this fixture. Graphics budgets are 16 MiB stored/loading/extracted bytes, 4 million decoded pixels, 4 MiB APC buffering, 256 visible assets, and 1,024 visible placements. Native core checkpoints omit graphics; the bridge refuses checkpoint creation while either screen retains images, placements, or a chunked transfer, preserving raw-replay recovery.

## Restore allocation hardening and dirty-row reuse

The subsequent fourth owned patch, `ghostty-vt-snapshot-allocator.patch`, adds an opt-in decoder allocator for native page pools. A supplied `GhosttyAllocator` alone is insufficient upstream: OS page backing bypasses it. CodeLima enables the option and retains one zero-initializing allocator with a 128 MiB live-allocation limit for decoded heap, native page pools, continuation, and render-state allocations. Page-aligned requests use anonymous mappings so native decommit/recommit never operates on shared malloc metadata. The allocator survives decoder destruction and is released only after its terminal and native handles. This is allocation accounting, not a process RSS cap; input bytes, Go copies, extraction frames, and separately bounded search/graphics work have their own ownership and limits.

`make test-ghostty-bridge` on the rebuilt Linux archive (`2e205255ce6d39bbfdf6f314824a31f12c8de3bbf32fac535a3d01e13289626c`) reports a 1,016,817-byte valid snapshot of 5,000 200-column repeated rows producing 8,855,792 live and 8,860,136 peak allocated bytes. The same production decoder with a 4 MiB test quota accepts a normal terminal but rejects this expansion before exceeding its limit, leaving the prior terminal unchanged. CRC corruption, strict FINISH/truncation handling, repeated replacement, and the production 128 MiB restore path are covered. Restore reapplies the host's 64 MiB/10,000-row history policy rather than trusting permissive encoded policy.

The same contract fixture now measures real extraction work: a clean four-row publication performs **zero row decodes and four row reuses**; changing one text row while moving the cursor performs two row decodes and two reuses. The C bridge skips native cell, grapheme, and hyperlink traversal for clean rows and retains immutable owned frame storage. Go reuses private immutable row data, copies public cell arrays to prevent caller mutation, and stores text per row so a single changed row cannot retain a whole obsolete frame. Selection/cursor metadata is refreshed, and colors/geometry/viewport/full-dirty changes invalidate appropriately. Output arrays and text spans still require bounded copies; this is not zero-copy publication. Tests also retain old frames across native mutation/destruction and mutate returned Go snapshots without affecting the cache.
