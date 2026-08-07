package codelima

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// The workaround must fire on cadence alone. Nothing here samples the host, and
// no threshold or cooldown can suppress a run: repeated ticks produce repeated
// reclaims for as long as the ticker is running.
func TestVirtioFSReclaimTickerRunsRepeatedlyOnItsInterval(t *testing.T) {
	t.Parallel()
	runs := make(chan struct{}, 16)
	ticker := newVirtioFSReclaimTicker(func(context.Context) (int, error) {
		select {
		case runs <- struct{}{}:
		default:
		}
		return 1, nil
	}, 2*time.Millisecond)

	ticker.Start(context.Background())
	defer ticker.Close()

	deadline := time.After(5 * time.Second)
	for observed := 0; observed < 3; observed++ {
		select {
		case <-runs:
		case <-deadline:
			t.Fatalf("reclaim ran %d times, want at least 3", observed)
		}
	}

	state := ticker.Snapshot()
	if state.LastReclaimedNodes != 1 {
		t.Fatalf("last reclaimed nodes = %d, want 1", state.LastReclaimedNodes)
	}
	if state.LastRunAt.IsZero() || !state.NextRunAt.After(state.LastRunAt) {
		t.Fatalf("cadence not reported: %+v", state)
	}
}

func TestVirtioFSReclaimTickerDefaultsToOneMinute(t *testing.T) {
	t.Parallel()
	ticker := newVirtioFSReclaimTicker(func(context.Context) (int, error) { return 0, nil }, 0)
	if ticker.interval != time.Minute {
		t.Fatalf("default interval = %v, want 60 seconds", ticker.interval)
	}
	if got := ticker.Snapshot().IntervalSeconds; got != 60 {
		t.Fatalf("reported interval = %d seconds, want 60", got)
	}
}

func TestVirtioFSReclaimTickerRecordsRunOutcomes(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	reclaimErr := errors.New("reclaim failed")
	var fail atomic.Bool
	ticker := newVirtioFSReclaimTicker(func(context.Context) (int, error) {
		if fail.Load() {
			return 0, reclaimErr
		}
		return 2, nil
	}, 30*time.Second)
	ticker.now = func() time.Time { return now }

	ticker.runOnce(context.Background())
	state := ticker.Snapshot()
	if state.LastReclaimedNodes != 2 || state.LastError != "" {
		t.Fatalf("successful run state = %+v", state)
	}
	if !state.LastRunAt.Equal(now) || state.NextRunAt.Sub(state.LastRunAt) != 30*time.Second {
		t.Fatalf("run timestamps = %+v", state)
	}

	fail.Store(true)
	now = now.Add(30 * time.Second)
	ticker.runOnce(context.Background())
	if got := ticker.Snapshot().LastError; got != reclaimErr.Error() {
		t.Fatalf("reclaim error = %q, want %q", got, reclaimErr)
	}

	// A failed run must not latch: the next successful run clears the error, and
	// nothing about the failure changes when the next run happens.
	fail.Store(false)
	now = now.Add(30 * time.Second)
	ticker.runOnce(context.Background())
	state = ticker.Snapshot()
	if state.LastError != "" || state.NextRunAt.Sub(state.LastRunAt) != 30*time.Second {
		t.Fatalf("recovered run state = %+v", state)
	}
}

func TestVirtioFSReclaimTickerCloseCancelsActiveReclaim(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{})
	ticker := newVirtioFSReclaimTicker(func(ctx context.Context) (int, error) {
		close(entered)
		<-ctx.Done()
		return 0, ctx.Err()
	}, time.Millisecond)
	ticker.Start(context.Background())

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("ticker did not begin reclaim")
	}

	closed := make(chan struct{})
	go func() {
		ticker.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("ticker Close did not cancel active reclaim")
	}
	if got := ticker.Snapshot().LastError; got != context.Canceled.Error() {
		t.Fatalf("canceled reclaim error = %q", got)
	}
}

// The workaround exists for Apple Virtualization's VirtioFS share, so the
// platform gate — not a sampler probing the host — decides whether the ticker
// is constructed at all.
func TestDaemonHostGatesVirtioFSReclaimOnPlatformAndSetting(t *testing.T) {
	t.Parallel()
	if got, want := virtioFSReclaimSupported(), runtime.GOOS == "darwin"; got != want {
		t.Fatalf("virtioFSReclaimSupported() = %v on %s, want %v", got, runtime.GOOS, want)
	}

	service, _ := newTestService(t)
	service.cfg.Daemon.VirtioFSReclaim = true
	host := newDaemonHost(service)
	if host.virtioFSSupported != virtioFSReclaimSupported() {
		t.Fatalf("supported = %v, want %v", host.virtioFSSupported, virtioFSReclaimSupported())
	}
	if virtioFSReclaimSupported() && host.virtioFSReclaimTicker == nil {
		t.Fatal("enabled reclaim did not construct a ticker on a supported host")
	}
	if !virtioFSReclaimSupported() && host.virtioFSReclaimTicker != nil {
		t.Fatal("unsupported host constructed a reclaim ticker")
	}

	service.cfg.Daemon.VirtioFSReclaim = false
	if disabled := newDaemonHost(service); disabled.virtioFSReclaimTicker != nil {
		t.Fatal("disabled reclaim constructed a ticker")
	}
}

func TestDaemonSnapshotReportsVirtioFSReclaimState(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)
	ticker := newVirtioFSReclaimTicker(func(context.Context) (int, error) { return 0, nil }, time.Minute)
	ticker.now = func() time.Time { return time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC) }
	ticker.runOnce(context.Background())

	host := newDaemonHost(service)
	host.virtioFSSupported = true
	host.virtioFSReclaimTicker = ticker
	result, err := host.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	snapshot := result.(map[string]any)
	status := snapshot["virtiofs_reclaim"].(map[string]any)
	if status["enabled"] != true || status["supported"] != true {
		t.Fatalf("VirtioFS reclaim status = %#v", status)
	}
	if status["interval_seconds"] != 60 || status["last_reclaimed_nodes"] != 0 {
		t.Fatalf("VirtioFS reclaim cadence = %#v", status)
	}
	if status["last_run_at"] == nil || status["next_run_at"] == nil {
		t.Fatalf("VirtioFS reclaim run timestamps missing: %#v", status)
	}
	// The pressure-sampling surface is gone; the snapshot must not resurrect it.
	for _, retired := range []string{"threshold_percent", "used_files", "limit_files", "last_sample_at", "last_attempt_at", "next_attempt_at", "last_released_files"} {
		if _, ok := status[retired]; ok {
			t.Fatalf("VirtioFS reclaim status still reports retired field %q: %#v", retired, status)
		}
	}
}
