//go:build cgo && (darwin || linux)

package ghostty

// #include "ghostty_bridge_compat.h"
import "C"

import (
	"fmt"
	"os"
	"time"
)

const (
	ghosttyCompressionIdle = 500 * time.Millisecond
	ghosttyCompressionStep = 10 * time.Millisecond
)

// CompressionStats measures scheduling, not logical terminal state. A completed
// pass leaves no timer armed until native compression activity changes again.
type CompressionStats struct {
	Enabled   bool
	Pending   bool
	Wakeups   uint64
	Steps     uint64
	LastError string
}

type idleCompressionState struct {
	stats    CompressionStats
	activity uint64
	observed bool
	deadline time.Time
}

type cmdCompression struct {
	Set     bool
	Enabled bool
	Reply   chan CompressionStats
}

func (t *ghosttyTUITerminal) SetIdleCompression(enabled bool) (CompressionStats, error) {
	return t.compressionCommand(cmdCompression{Set: true, Enabled: enabled})
}

func (t *ghosttyTUITerminal) CompressionStatus() (CompressionStats, error) {
	return t.compressionCommand(cmdCompression{})
}

func (t *ghosttyTUITerminal) compressionCommand(command cmdCompression) (CompressionStats, error) {
	command.Reply = make(chan CompressionStats, 1)
	select {
	case t.commands <- actorEnvelope{cmd: command}:
	case <-t.actorDone:
		return CompressionStats{}, errTerminalClosed
	}
	select {
	case status := <-command.Reply:
		return status, nil
	case <-t.actorDone:
		return CompressionStats{}, errTerminalClosed
	}
}

func (t *ghosttyTUITerminal) configureCompression(command cmdCompression) CompressionStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	if command.Set {
		t.compression.stats.Enabled = command.Enabled
		t.compression.stats.Pending = false
		t.compression.stats.LastError = ""
		t.compression.observed = false
	}
	return t.compression.stats
}

func (t *ghosttyTUITerminal) initializeCompression() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.compression.stats.Enabled = os.Getenv("CODELIMA_GHOSTTY_IDLE_COMPRESSION") != "0"
	if t.term == nil || !t.compression.stats.Enabled {
		return
	}
	var activity C.uint64_t
	if C.ghostty_bridge_terminal_compression_activity(t.term, &activity) == C.GHOSTTY_SUCCESS {
		t.compression.activity = uint64(activity)
		t.compression.observed = true
	}
}

func (t *ghosttyTUITerminal) compressionDeadline(now time.Time) (time.Time, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	state := &t.compression
	if !state.stats.Enabled || t.closed || t.term == nil || t.nativeErr != nil {
		return time.Time{}, false
	}
	var activity C.uint64_t
	result := C.ghostty_bridge_terminal_compression_activity(t.term, &activity)
	if result != C.GHOSTTY_SUCCESS {
		state.stats.Enabled = false
		state.stats.LastError = fmt.Sprintf("compression activity: native result %d", int(result))
		return time.Time{}, false
	}
	if !state.observed || state.activity != uint64(activity) {
		state.activity, state.observed = uint64(activity), true
		state.stats.Pending = true
		state.deadline = now.Add(ghosttyCompressionIdle)
	}
	return state.deadline, state.stats.Pending
}

func (t *ghosttyTUITerminal) compressIdle(now time.Time) {
	deadline, pending := t.compressionDeadline(now)
	if !pending || now.Before(deadline) {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state := &t.compression
	state.stats.Wakeups++
	var progress C.GhosttyTerminalCompressionResult
	result := C.ghostty_bridge_terminal_compress(t.term, &progress)
	if result != C.GHOSTTY_SUCCESS || progress == C.GHOSTTY_TERMINAL_COMPRESSION_RESULT_UNSUPPORTED {
		state.stats.Enabled = false
		state.stats.Pending = false
		state.stats.LastError = fmt.Sprintf("compression unavailable: native result %d, progress %d", int(result), int(progress))
		return
	}
	state.stats.Steps++
	state.stats.Pending = progress == C.GHOSTTY_TERMINAL_COMPRESSION_RESULT_PENDING
	state.deadline = now.Add(ghosttyCompressionStep)
}
