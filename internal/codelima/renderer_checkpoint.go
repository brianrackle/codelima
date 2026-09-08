package codelima

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/brianrackle/codelima/internal/terminalstate"
	"slices"
)

const (
	rendererCheckpointVersion        = 1
	rendererCheckpointMaxHeaderBytes = 64 << 10
)

var errRendererCheckpointBuild = errors.New("renderer checkpoint native build mismatch")

type rendererCheckpointPolicy struct {
	WidthPolicy     string `json:"width_policy"`
	ScrollbackLines int    `json:"scrollback_lines"`
}

func currentRendererCheckpointPolicy() rendererCheckpointPolicy {
	return rendererCheckpointPolicy{WidthPolicy: "ghostty-default", ScrollbackLines: 10000}
}

// rendererCheckpoint is an application envelope around the opaque native
// FINISH stream. Its digest also covers identity, watermark and excluded state,
// so altered metadata cannot select the wrong replay tail for intact payloads.
type rendererCheckpoint struct {
	Version            int                      `json:"version"`
	BuildID            string                   `json:"build_id"`
	TerminalID         string                   `json:"terminal_id"`
	RendererGeneration uint64                   `json:"renderer_generation"`
	AppliedEventID     uint64                   `json:"applied_event_id"`
	Partial            bool                     `json:"partial"`
	Policy             rendererCheckpointPolicy `json:"policy"`
	State              terminalCheckpointState  `json:"state"`
	SHA256             string                   `json:"sha256"`
}

func newRendererCheckpoint(build, terminal string, generation, applied uint64, partial bool, state terminalCheckpointState) (*rendererCheckpoint, error) {
	checkpoint := &rendererCheckpoint{
		Version: rendererCheckpointVersion, BuildID: build, TerminalID: terminal,
		RendererGeneration: generation, AppliedEventID: applied, Partial: partial,
		Policy: currentRendererCheckpointPolicy(), State: state,
	}
	if err := checkpoint.validateBounds(); err != nil {
		return nil, err
	}
	checkpoint.State.Data = slices.Clone(state.Data)
	checkpoint.State.Colors = terminalstate.CloneColors(state.Colors)
	digest, err := checkpoint.digest()
	if err != nil {
		return nil, err
	}
	checkpoint.SHA256 = digest
	return checkpoint, nil
}

func (c *rendererCheckpoint) validate(build, terminal string) error {
	if err := c.validateIntegrity(terminal); err != nil {
		return err
	}
	if c.BuildID != build {
		return errRendererCheckpointBuild
	}
	if c.Policy != currentRendererCheckpointPolicy() {
		return errors.New("renderer checkpoint policy mismatch")
	}
	return nil
}

func (c *rendererCheckpoint) validateIntegrity(terminal string) error {
	if c == nil {
		return errors.New("renderer checkpoint is missing")
	}
	if err := c.validateBounds(); err != nil {
		return err
	}
	if c.TerminalID != terminal {
		return errors.New("renderer checkpoint belongs to another terminal")
	}
	digest, err := c.digest()
	if err != nil {
		return err
	}
	if digest != c.SHA256 {
		return errors.New("renderer checkpoint integrity mismatch")
	}
	return nil
}

func (c *rendererCheckpoint) validateBounds() error {
	if c.Version != rendererCheckpointVersion || c.BuildID == "" || len(c.BuildID) > 256 || c.TerminalID == "" || len(c.TerminalID) > 256 || c.RendererGeneration == 0 || c.AppliedEventID >= rendererInputEventBit {
		return errors.New("renderer checkpoint identity or version is invalid")
	}
	state := c.State
	if err := terminalstate.ValidateColors(state.Colors); err != nil {
		return err
	}
	if !rendererValidPixels(state.CellWidth, state.CellHeight) {
		return errors.New("renderer checkpoint pixel geometry is invalid")
	}
	if len(state.Data) == 0 || len(state.Data) > rendererCheckpointMaxBytes {
		return fmt.Errorf("renderer checkpoint size must be within 1..%d bytes", rendererCheckpointMaxBytes)
	}
	if !rendererValidGeometry(state.Cols, state.Rows) {
		return errors.New("renderer checkpoint geometry is invalid")
	}
	if state.ViewportOffset < 0 || state.ViewportOffset > currentRendererCheckpointPolicy().ScrollbackLines+state.Rows {
		return errors.New("renderer checkpoint viewport is invalid")
	}
	return nil
}

func rendererValidGeometry(cols, rows int) bool {
	return cols > 0 && rows > 0 && cols <= 65535 && rows <= 65535 && cols <= (1<<20)/rows
}

func rendererValidPixels(width, height int) bool {
	return (width == 0 && height == 0) || (width > 0 && height > 0 && width <= 4096 && height <= 4096)
}

func (c *rendererCheckpoint) digest() (string, error) {
	header := *c
	header.State.Data = nil
	header.SHA256 = ""
	metadata, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	if len(metadata) > rendererCheckpointMaxHeaderBytes {
		return "", errors.New("renderer checkpoint metadata exceeds its bound")
	}
	hash := sha256.New()
	_, _ = hash.Write(metadata)
	_, _ = hash.Write(c.State.Data)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// tailAfter preserves the bounded raw journal for cross-build fallback while
// extracting only events not already represented by the checkpoint. A resize
// span may deliberately replace intermediate geometries; an uncovered output
// ID makes the checkpoint unusable rather than silently omitting that output.
func (j *rendererJournal) tailAfter(applied uint64) (rendererJournalSnapshot, bool) {
	return j.Snapshot().tailAfter(applied)
}

func (j rendererJournalSnapshot) tailAfter(applied uint64) (rendererJournalSnapshot, bool) {
	result := rendererJournalSnapshot{Cols: j.Cols, Rows: j.Rows, LastID: j.LastID, CellWidth: j.CellWidth, CellHeight: j.CellHeight}
	if applied > j.LastID {
		return result, false
	}
	expected := applied + 1
	for _, event := range j.Events {
		if event.ID <= applied {
			continue
		}
		first := event.FirstID
		if first == 0 {
			first = event.ID
		}
		if first > expected || event.ID < expected {
			return result, false
		}
		event.Data = slices.Clone(event.Data)
		result.Events = append(result.Events, event)
		result.Bytes += rendererJournalEventBytes(event)
		expected = event.ID + 1
	}
	return result, expected == j.LastID+1
}
