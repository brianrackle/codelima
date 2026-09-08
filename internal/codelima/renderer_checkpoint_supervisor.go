//go:build darwin || linux

package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/brianrackle/codelima/internal/terminalstate"
)

// At most one worker capture/encode is in flight per daemon. Together with
// the retained pool this bounds temporary checkpoint payloads as well as the
// long-lived recovery cache, without parking the supervisor health loop.
var rendererCheckpointCaptureSlot = make(chan struct{}, 1)

// Periodic captures are single-flight and do not block health supervision.
// The raw journal stays bounded and retained independently for build-skew
// fallback. A checkpoint is useful only while its entire newer tail survives.
func (s *rendererSupervisor) maybeCheckpoint(now time.Time) {
	s.mu.Lock()
	stats := s.journal.Stats()
	due := !s.closed && s.acceptFrames && !s.checkpointBusy && now.Sub(s.lastCheckpoint) >= time.Second &&
		(s.checkpoint == nil || stats.LastID != s.checkpoint.AppliedEventID || s.checkpointDirty || s.publishedRevision > s.checkpoint.State.Generation)
	if !due {
		s.mu.Unlock()
		return
	}
	s.checkpointBusy = true
	s.lastCheckpoint = now
	s.checkpointTasks.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.checkpointTasks.Done()
		ctx, cancel := s.shutdownContext()
		defer cancel()
		err := s.captureCheckpoint(ctx)
		s.mu.Lock()
		if err != nil {
			s.checkpointDirty = true
		}
		s.checkpointBusy = false
		s.mu.Unlock()
	}()
}

func (s *rendererSupervisor) captureCheckpoint(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, rendererReadDeadlineFactor*s.options.CommandTimeout)
	defer cancel()
	select {
	case rendererCheckpointCaptureSlot <- struct{}{}:
		defer func() { <-rendererCheckpointCaptureSlot }()
	case <-ctx.Done():
		return ctx.Err()
	}
	s.mu.Lock()
	link, generation := s.link, s.generation
	// Clear before capture so a mutation admitted while native capture runs
	// remains dirty and receives its own later checkpoint.
	s.checkpointDirty = false
	ready := !s.closed && s.acceptFrames && link != nil
	s.mu.Unlock()
	if !ready {
		return errRendererUnavailable
	}
	through := s.journal.Stats().LastID
	raw, err := link.CallResult(ctx, "checkpoint", rendererCheckpointParams{Through: through})
	if err != nil {
		return err
	}
	var checkpoint rendererCheckpoint
	if err := json.Unmarshal(raw, &checkpoint); err != nil {
		return err
	}
	if err := checkpoint.validate(rendererNativeBuildIdentity(), s.terminalID); err != nil {
		return err
	}
	if checkpoint.RendererGeneration != generation || checkpoint.AppliedEventID < through {
		return errors.New("renderer checkpoint generation or applied watermark is invalid")
	}
	// Native I/O and envelope hashing stay outside the lifecycle mutex. An old
	// worker may finish after replacement; it must never replace recovery state.
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !s.acceptFrames || s.link != link || s.generation != generation {
		return errRendererUnavailable
	}
	if checkpoint.AppliedEventID > s.journal.Stats().LastID {
		return errors.New("renderer checkpoint watermark is ahead of journal")
	}
	if s.checkpoint == nil || checkpoint.AppliedEventID >= s.checkpoint.AppliedEventID {
		if !processRendererCheckpointQuota.reserve(s, len(checkpoint.State.Data)+rendererCheckpointMaxHeaderBytes) {
			return errors.New("daemon retained checkpoint memory quota is full")
		}
		s.checkpoint = &checkpoint
	}
	return nil
}

func (s *rendererSupervisor) restoreCheckpoint(checkpoint *rendererCheckpoint) bool {
	if checkpoint == nil || checkpoint.validate(rendererNativeBuildIdentity(), s.terminalID) != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !processRendererCheckpointQuota.reserve(s, len(checkpoint.State.Data)+rendererCheckpointMaxHeaderBytes) {
		return false
	}
	s.checkpoint = checkpoint
	return true
}

func (s *rendererSupervisor) handoffRecovery(journal rendererJournalSnapshot, includeCheckpoint ...bool) []byte {
	s.mu.Lock()
	checkpoint := s.checkpoint
	colors := terminalstate.CloneColors(s.colors)
	s.mu.Unlock()
	if len(includeCheckpoint) > 0 && !includeCheckpoint[0] {
		checkpoint = nil
	}
	if checkpoint != nil {
		if _, complete := journal.tailAfter(checkpoint.AppliedEventID); !complete {
			checkpoint = nil
		}
	}
	data, err := encodeRendererRecovery(s.terminalID, checkpoint, journal, colors)
	if err != nil {
		data, _ = encodeRendererRecovery(s.terminalID, nil, journal, colors)
	}
	return data
}

func (s *rendererSupervisor) restoreColors(colors *TerminalColors) {
	if colors == nil {
		return
	}
	s.mu.Lock()
	if !s.closed {
		s.colors = terminalstate.CloneColors(colors)
		s.colorsRevision++
		s.checkpointDirty = true
	}
	s.mu.Unlock()
}
