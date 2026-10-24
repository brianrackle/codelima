# Skip queued cache reclaims after cancellation

## Context and Problem Statement

The full verification suite exposed a shutdown race in the VirtioFS reclaim
ticker. When cancellation and a buffered timer tick were both ready, Go's
select could choose the tick and invoke the reclaim callback again during
shutdown. The active-reclaim cancellation regression detected this as a second
callback invocation.

## Considered Options

* Permit another callback with a canceled context and weaken the test.
* Check cancellation after acquiring the pass lock, before invoking reclaim.

## Decision Outcome

Check the context after acquiring the pass lock. A canceled queued pass does
not invoke reclaim or replace the last actual outcome. Cadence, supported
platforms and the active pass's cancellation behavior remain the same. A
deterministic canceled-pass test covers the race independently of scheduler
timing; the existing active-pass test continues to verify prompt shutdown.

### Positive Consequences

* Shutdown does not initiate redundant guest work from queued timer events.
* The regression is reproducible without sleeps or scheduler assumptions.

### Negative Consequences

* A cancellation racing after the check still relies on the active reclaim's
  existing context handling, as every in-progress pass already does.
