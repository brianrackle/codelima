package codelima

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	limaWatchInitialBackoff = 250 * time.Millisecond
	limaWatchMaximumBackoff = 30 * time.Second
	// limaObserverReconcileInterval bounds how far the observation cache may
	// drift from limactl's own view while the watch is healthy.
	//
	// `limactl watch` reports running/exiting transitions and nothing else: an
	// instance created, deleted or repaired outside CodeLima never appears, and
	// an entry synthesized from an event carries no SSH config path, no dir and
	// no VM type. The daemon holds one watch open for days at a time, so without
	// a full list on a timer the cache diverges without bound — and every read
	// surface (NodeList, the TUI, the forwarder's reconcile loop) reads through
	// it. One minute is short enough that drift is never user-visible for long
	// and long enough that the list costs nothing measurable.
	limaObserverReconcileInterval = time.Minute
)

type LimaObservationRuntime interface {
	StartObservation(context.Context) error
	StopObservation() error
	ObservationSnapshot() map[string]any
}

type limaWatchEvent struct {
	Instance string `json:"instance"`
	Event    struct {
		Time   time.Time `json:"time"`
		Status struct {
			Running bool `json:"running"`
			Exiting bool `json:"exiting"`
		} `json:"status"`
	} `json:"event"`
}

func (c *LimaClient) StartObservation(parent context.Context) error {
	if c.observer == nil {
		c.observer = newLimaObservationService()
	}
	c.observer.mu.RLock()
	alreadyStarted := c.observer.started
	c.observer.mu.RUnlock()
	if alreadyStarted {
		return nil
	}

	// A failed initial list deliberately does not populate the cache, and
	// replace is the only thing that stamps lastList — which is what cached()
	// tests for authority. Starting the watch with an empty, unstamped cache
	// therefore leaves reads falling through to a direct list rather than
	// serving "no instances exist" as if it were an answer: an authoritative
	// empty cache yanks every forwarding route in one tick (plans §6d).
	observations, listErr := c.listDirect(parent)
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	if listErr == nil {
		c.observer.replace(observations)
	}
	c.observer.mu.Lock()
	if c.observer.started {
		c.observer.mu.Unlock()
		cancel()
		return nil
	}
	c.observer.started = true
	c.observer.cancel = cancel
	c.observer.done = done
	if listErr != nil {
		c.observer.lastError = listErr.Error()
	} else {
		c.observer.lastError = ""
	}
	c.observer.mu.Unlock()
	// Both loops are owned by this observation: they are cancelled by
	// StopObservation's cancel and done is not closed until both have returned,
	// so a stopped observation never leaves a goroutine listing behind it.
	go func() {
		defer close(done)
		var loops sync.WaitGroup
		loops.Add(2)
		go func() {
			defer loops.Done()
			c.watchLoop(ctx)
		}()
		go func() {
			defer loops.Done()
			c.reconcileLoop(ctx)
		}()
		loops.Wait()
	}()
	return nil
}

// reconcileLoop replaces the cache from a full `limactl list` on a timer for as
// long as the observation runs. It is the only thing that repairs drift while
// the watch is healthy, because a healthy watch never ends and the post-restart
// list in watchLoop only runs after the watch has failed.
//
// A failed reconciliation records the error and preserves the previous cache:
// the last known-good view of the world is strictly better than an empty one,
// and cached() keeps reporting authority from the earlier successful list.
func (c *LimaClient) reconcileLoop(ctx context.Context) {
	interval := c.ReconcileInterval
	if interval <= 0 {
		interval = limaObserverReconcileInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		observations, err := c.listDirect(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			c.observer.mu.Lock()
			c.observer.lastError = err.Error()
			c.observer.reconcileFailures++
			c.observer.mu.Unlock()
			continue
		}
		c.observer.replace(observations)
		c.observer.mu.Lock()
		c.observer.lastError = ""
		c.observer.mu.Unlock()
	}
}

func (c *LimaClient) StopObservation() error {
	if c.observer == nil {
		return nil
	}
	c.observer.mu.Lock()
	cancel, done := c.observer.cancel, c.observer.done
	c.observer.cancel = nil
	c.observer.done = nil
	c.observer.started = false
	c.observer.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			return errors.New("timed out stopping Lima observation watcher")
		}
	}
	return nil
}

func (c *LimaClient) watchLoop(ctx context.Context) {
	backoff := limaWatchInitialBackoff
	for ctx.Err() == nil {
		watchStartedAt := time.Now()
		err := c.runWatch(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(watchStartedAt) >= time.Second {
			backoff = limaWatchInitialBackoff
		}
		c.observer.mu.Lock()
		c.observer.restarts++
		c.observer.lastError = err.Error()
		c.observer.mu.Unlock()

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if observations, listErr := c.listDirect(ctx); listErr == nil {
			c.observer.replace(observations)
		}
		backoff = min(backoff*2, limaWatchMaximumBackoff)
	}
}

func (c *LimaClient) runWatch(ctx context.Context) error {
	command := exec.CommandContext(ctx, c.binary(), "watch", "--json")
	configureCommandCancellation(command)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr limitedBuffer
	stderr.limit = 64 * 1024
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return err
	}
	c.observer.mu.Lock()
	c.observer.connected = true
	c.observer.lastError = ""
	c.observer.mu.Unlock()
	defer func() {
		c.observer.mu.Lock()
		c.observer.connected = false
		c.observer.mu.Unlock()
	}()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), limaOutputLimit)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event limaWatchEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			_ = command.Cancel()
			_ = command.Wait()
			return fmt.Errorf("parse limactl watch event: %w", err)
		}
		if strings.TrimSpace(event.Instance) == "" {
			_ = command.Cancel()
			_ = command.Wait()
			return errors.New("limactl watch event omitted instance")
		}
		c.applyWatchEvent(event)
	}
	if err := scanner.Err(); err != nil {
		_ = command.Cancel()
		_ = command.Wait()
		return err
	}
	if err := command.Wait(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("limactl watch exited: %w: %s", err, boundedText(stderr.Bytes(), 4096))
	}
	return errors.New("limactl watch exited unexpectedly")
}

func (c *LimaClient) applyWatchEvent(event limaWatchEvent) {
	name := strings.ToLower(strings.TrimSpace(event.Instance))
	c.observer.mu.Lock()
	observation, exists := c.observer.observations[name]
	if !exists {
		observation = RuntimeObservation{Name: event.Instance, Exists: true}
	}
	switch {
	case event.Event.Status.Running:
		observation.Status = ObservationRunning
	case event.Event.Status.Exiting:
		observation.Status = ObservationStopped
	}
	c.observer.observations[name] = observation
	if event.Event.Time.IsZero() {
		c.observer.lastEvent = time.Now().UTC()
	} else {
		c.observer.lastEvent = event.Event.Time.UTC()
	}
	c.observer.lastError = ""
	c.observer.mu.Unlock()
}

func (c *LimaClient) ObservationSnapshot() map[string]any {
	if c.observer == nil {
		return map[string]any{"connected": false}
	}
	c.observer.mu.RLock()
	defer c.observer.mu.RUnlock()
	result := map[string]any{
		"connected":     c.observer.started && c.observer.connected,
		"restart_count": c.observer.restarts,
		"instances":     len(c.observer.observations),
	}
	if !c.observer.lastEvent.IsZero() {
		result["last_event_at"] = c.observer.lastEvent
	}
	// Authority, not liveness: a started observation whose every list has failed
	// serves nothing, and operators need to see that rather than infer it.
	result["authoritative"] = c.observer.started && !c.observer.lastList.IsZero()
	if !c.observer.lastList.IsZero() {
		result["last_reconciliation_at"] = c.observer.lastList
	}
	if c.observer.reconcileFailures > 0 {
		result["reconciliation_failures"] = c.observer.reconcileFailures
	}
	if c.observer.lastError != "" {
		result["last_error"] = c.observer.lastError
	}
	return result
}
