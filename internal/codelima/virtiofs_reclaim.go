package codelima

import (
	"context"
	"log/slog"
	"runtime"
	"sync"
	"time"
)

const (
	defaultVirtioFSReclaimInterval     = 60 * time.Second
	guestFilesystemCacheReclaimTimeout = 30 * time.Second
)

// virtioFSReclaimSupported gates the workaround to macOS hosts. Dropping guest
// dentry/inode caches works around a bug in Apple Virtualization's VirtioFS
// share, so it is pointless anywhere that share does not exist: Linux hosts
// mount guest workspaces over 9p.
func virtioFSReclaimSupported() bool {
	return runtime.GOOS == "darwin"
}

type guestFilesystemCacheReclaimer func(context.Context) (int, error)

type virtioFSReclaimSnapshot struct {
	IntervalSeconds    int       `json:"interval_seconds"`
	LastRunAt          time.Time `json:"last_run_at"`
	NextRunAt          time.Time `json:"next_run_at"`
	LastReclaimedNodes int       `json:"last_reclaimed_nodes"`
	LastError          string    `json:"last_error,omitempty"`
}

// virtioFSReclaimTicker runs the Apple VirtioFS workaround on an unconditional
// fixed interval while it is enabled. It deliberately observes nothing: an
// earlier version sampled the host file table and only reclaimed above a
// threshold, which made a correctness workaround fire or not fire based on
// unrelated host activity. Cadence is the whole contract.
type virtioFSReclaimTicker struct {
	reclaim  guestFilesystemCacheReclaimer
	interval time.Duration
	now      func() time.Time
	logger   *slog.Logger

	runMu sync.Mutex
	mu    sync.RWMutex
	state virtioFSReclaimSnapshot

	lifecycleMu sync.Mutex
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

func newVirtioFSReclaimTicker(reclaim guestFilesystemCacheReclaimer, interval time.Duration) *virtioFSReclaimTicker {
	if interval <= 0 {
		interval = defaultVirtioFSReclaimInterval
	}
	return &virtioFSReclaimTicker{
		reclaim:  reclaim,
		interval: interval,
		now:      time.Now,
		logger:   discardLogger(),
		state:    virtioFSReclaimSnapshot{IntervalSeconds: int(interval / time.Second)},
	}
}

func (t *virtioFSReclaimTicker) Start(ctx context.Context) {
	t.lifecycleMu.Lock()
	defer t.lifecycleMu.Unlock()
	if t.cancel != nil {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	t.cancel = cancel
	t.recordNextRun(t.now().UTC().Add(t.interval))
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		ticker := time.NewTicker(t.interval)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				t.runOnce(runCtx)
			}
		}
	}()
}

func (t *virtioFSReclaimTicker) Close() {
	t.lifecycleMu.Lock()
	cancel := t.cancel
	t.cancel = nil
	t.lifecycleMu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	t.wg.Wait()
}

func (t *virtioFSReclaimTicker) Snapshot() virtioFSReclaimSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.state
}

// runOnce reclaims every running mounted node once. A slow guest never queues a
// second pass behind itself: the tick that arrives mid-run is absorbed by the
// ticker's own drop-on-full channel, and runMu keeps a manually driven caller
// (tests, future on-demand reclaim) from overlapping.
func (t *virtioFSReclaimTicker) runOnce(ctx context.Context) {
	t.runMu.Lock()
	defer t.runMu.Unlock()

	startedAt := t.now().UTC()
	reclaimedNodes, err := t.reclaim(ctx)

	t.mu.Lock()
	t.state.LastRunAt = startedAt
	t.state.NextRunAt = startedAt.Add(t.interval)
	t.state.LastReclaimedNodes = reclaimedNodes
	if err != nil {
		t.state.LastError = err.Error()
	} else {
		t.state.LastError = ""
	}
	t.mu.Unlock()

	if err != nil {
		t.logger.Warn("failed to reclaim mounted-node filesystem metadata caches", "error", err)
		return
	}
	if reclaimedNodes > 0 {
		t.logger.Info("reclaimed mounted-node filesystem metadata caches", "nodes", reclaimedNodes)
	}
}

func (t *virtioFSReclaimTicker) recordNextRun(at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state.NextRunAt = at
}
