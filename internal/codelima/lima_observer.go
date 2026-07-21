package codelima

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	limaWatchInitialBackoff = 250 * time.Millisecond
	limaWatchMaximumBackoff = 30 * time.Second
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
	go func() {
		defer close(done)
		c.watchLoop(ctx)
	}()
	return nil
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
	if !c.observer.lastList.IsZero() {
		result["last_reconciliation_at"] = c.observer.lastList
	}
	if c.observer.lastError != "" {
		result["last_error"] = c.observer.lastError
	}
	return result
}
