package codelima

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

const (
	defaultVirtioFSPressureThresholdPercent = 20
	defaultVirtioFSPressurePollInterval     = 2 * time.Second
	defaultVirtioFSReclaimCooldown          = 30 * time.Second
	guestFilesystemCacheReclaimTimeout      = 30 * time.Second
)

type hostFilePressure struct {
	used  uint64
	limit uint64
}

func (p hostFilePressure) atOrAbove(thresholdPercent int) bool {
	if p.limit == 0 || thresholdPercent <= 0 {
		return false
	}
	// Rearrange used/limit >= threshold/100 so integer division does not
	// truncate values immediately below the threshold.
	return p.used >= (p.limit*uint64(thresholdPercent)+99)/100
}

type hostFilePressureSampler func() (hostFilePressure, error)
type guestFilesystemCacheReclaimer func(context.Context) (int, error)

type virtioFSPressureOptions struct {
	thresholdPercent int
	pollInterval     time.Duration
	cooldown         time.Duration
}

type virtioFSPressureSnapshot struct {
	ThresholdPercent   int       `json:"threshold_percent"`
	UsedFiles          uint64    `json:"used_files"`
	LimitFiles         uint64    `json:"limit_files"`
	LastSampleAt       time.Time `json:"last_sample_at"`
	LastAttemptAt      time.Time `json:"last_attempt_at"`
	LastReclaimedNodes int       `json:"last_reclaimed_nodes"`
	LastReleasedFiles  uint64    `json:"last_released_files"`
	NextAttemptAt      time.Time `json:"next_attempt_at"`
	LastError          string    `json:"last_error,omitempty"`
}

type virtioFSPressureController struct {
	sample  hostFilePressureSampler
	reclaim guestFilesystemCacheReclaimer
	options virtioFSPressureOptions
	now     func() time.Time
	logger  *slog.Logger

	checkMu sync.Mutex
	mu      sync.RWMutex
	state   virtioFSPressureSnapshot

	lifecycleMu sync.Mutex
	cancel      context.CancelFunc
	wg          sync.WaitGroup
}

func newVirtioFSPressureController(sample hostFilePressureSampler, reclaim guestFilesystemCacheReclaimer, options virtioFSPressureOptions) *virtioFSPressureController {
	if options.thresholdPercent == 0 {
		options.thresholdPercent = defaultVirtioFSPressureThresholdPercent
	}
	if options.pollInterval <= 0 {
		options.pollInterval = defaultVirtioFSPressurePollInterval
	}
	if options.cooldown <= 0 {
		options.cooldown = defaultVirtioFSReclaimCooldown
	}
	return &virtioFSPressureController{
		sample: sample, reclaim: reclaim, options: options,
		now: time.Now, logger: discardLogger(),
		state: virtioFSPressureSnapshot{ThresholdPercent: options.thresholdPercent},
	}
}

func (c *virtioFSPressureController) Start(ctx context.Context) {
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	if c.cancel != nil {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.check(runCtx)
		ticker := time.NewTicker(c.options.pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				c.check(runCtx)
			}
		}
	}()
}

func (c *virtioFSPressureController) Close() {
	c.lifecycleMu.Lock()
	cancel := c.cancel
	c.cancel = nil
	c.lifecycleMu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	c.wg.Wait()
}

func (c *virtioFSPressureController) Snapshot() virtioFSPressureSnapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

func (c *virtioFSPressureController) check(ctx context.Context) {
	c.checkMu.Lock()
	defer c.checkMu.Unlock()

	now := c.now().UTC()
	before, err := c.sample()
	if err != nil {
		c.recordSample(now, hostFilePressure{}, err)
		c.logger.Warn("failed to sample macOS host file pressure", "error", err)
		return
	}
	c.recordSample(now, before, nil)
	if !before.atOrAbove(c.options.thresholdPercent) {
		return
	}

	c.mu.RLock()
	nextAttemptAt := c.state.NextAttemptAt
	c.mu.RUnlock()
	if now.Before(nextAttemptAt) {
		return
	}

	reclaimedNodes, reclaimErr := c.reclaim(ctx)
	c.mu.Lock()
	c.state.LastAttemptAt = now
	c.state.LastReclaimedNodes = reclaimedNodes
	c.state.LastReleasedFiles = 0
	if reclaimErr != nil {
		c.state.LastError = reclaimErr.Error()
		c.state.NextAttemptAt = now.Add(c.options.cooldown)
	}
	c.mu.Unlock()
	if reclaimErr != nil {
		c.logger.Warn("failed to reclaim mounted-node filesystem metadata caches", "error", reclaimErr)
		return
	}
	if reclaimedNodes == 0 {
		c.mu.Lock()
		c.state.LastError = ""
		c.state.NextAttemptAt = now.Add(c.options.cooldown)
		c.mu.Unlock()
		return
	}

	after, sampleErr := c.sample()
	if sampleErr != nil {
		c.mu.Lock()
		c.state.LastError = fmt.Sprintf("sample host pressure after reclaim: %v", sampleErr)
		c.state.NextAttemptAt = now.Add(c.options.cooldown)
		c.mu.Unlock()
		c.logger.Warn("failed to verify VirtioFS cache reclaim", "error", sampleErr)
		return
	}

	released := uint64(0)
	if before.used > after.used {
		released = before.used - after.used
	}
	eligibleAt := now.Add(c.options.cooldown)
	c.mu.Lock()
	c.state.UsedFiles = after.used
	c.state.LimitFiles = after.limit
	c.state.LastReleasedFiles = released
	c.state.NextAttemptAt = eligibleAt
	c.state.LastError = ""
	c.mu.Unlock()
	c.logger.Info("reclaimed mounted-node filesystem metadata caches",
		"nodes", reclaimedNodes,
		"host_files_before", before.used,
		"host_files_after", after.used,
		"next_attempt_at", eligibleAt,
	)
}

func (c *virtioFSPressureController) recordSample(at time.Time, pressure hostFilePressure, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.LastSampleAt = at
	if err != nil {
		c.state.LastError = err.Error()
		return
	}
	c.state.UsedFiles = pressure.used
	c.state.LimitFiles = pressure.limit
}
