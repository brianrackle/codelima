package codelima

import (
	"slices"
	"sync"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

const defaultRendererJournalBytes = daemon.MaxHandoffReplayBytesPerTerminal

// rendererResizeEventBytes is the nominal retention cost charged to a resize
// event. Resizes carry no payload, but every retained event is replayed inside
// a replacement renderer's init deadline, so an untrimmed resize history is
// exactly as expensive as untrimmed output.
const rendererResizeEventBytes = 64

type rendererJournalEvent struct {
	ID   uint64 `json:"id"`
	Type string `json:"type"`
	Data []byte `json:"data,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

type rendererJournalSnapshot struct {
	Events  []rendererJournalEvent
	Partial bool
	Bytes   int
	Cols    int
	Rows    int
}

// rendererJournalStats reports journal accounting without copying the retained
// events, so supervisor decisions that only need sizes never pay for a deep
// copy of the history.
type rendererJournalStats struct {
	Events  int
	Bytes   int
	Partial bool
	Cols    int
	Rows    int
}

type rendererJournal struct {
	mu       sync.Mutex
	maxBytes int
	nextID   uint64
	events   []rendererJournalEvent
	bytes    int
	partial  bool
	cols     int
	rows     int
}

func newRendererJournal(maxBytes int) *rendererJournal {
	if maxBytes <= 0 {
		maxBytes = defaultRendererJournalBytes
	}
	return &rendererJournal{maxBytes: maxBytes}
}

func (j *rendererJournal) AppendOutput(data []byte) rendererJournalEvent {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.nextID++
	event := rendererJournalEvent{ID: j.nextID, Type: "output", Data: slices.Clone(data)}
	j.events = append(j.events, event)
	j.bytes += rendererJournalEventBytes(event)
	j.trimLocked()
	return event
}

// AppendResize records a geometry change. Consecutive resizes are coalesced:
// only the newest geometry is state-affecting, so a window-drag storm collapses
// to a single retained event instead of bloating every future replay.
func (j *rendererJournal) AppendResize(cols, rows int) rendererJournalEvent {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.nextID++
	event := rendererJournalEvent{ID: j.nextID, Type: "resize", Cols: cols, Rows: rows}
	if last := len(j.events) - 1; last >= 0 && j.events[last].Type == "resize" {
		j.bytes -= rendererJournalEventBytes(j.events[last])
		j.events[last] = event
	} else {
		j.events = append(j.events, event)
	}
	j.bytes += rendererJournalEventBytes(event)
	j.cols, j.rows = cols, rows
	j.trimLocked()
	return event
}

func (j *rendererJournal) Snapshot() rendererJournalSnapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
	events := make([]rendererJournalEvent, len(j.events))
	for index, event := range j.events {
		events[index] = event
		events[index].Data = slices.Clone(event.Data)
	}
	return rendererJournalSnapshot{
		Events:  events,
		Partial: j.partial,
		Bytes:   j.bytes,
		Cols:    j.cols,
		Rows:    j.rows,
	}
}

func (j *rendererJournal) Stats() rendererJournalStats {
	j.mu.Lock()
	defer j.mu.Unlock()
	return rendererJournalStats{
		Events:  len(j.events),
		Bytes:   j.bytes,
		Partial: j.partial,
		Cols:    j.cols,
		Rows:    j.rows,
	}
}

// Reset drops the retained history after replaying it has repeatedly killed
// replacement renderers. The recorded geometry survives so the next renderer
// still starts at the right size, and the journal reports partial recovery
// because the screen can no longer be reconstructed.
func (j *rendererJournal) Reset() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.events) == 0 {
		return
	}
	j.events = nil
	j.bytes = 0
	j.partial = true
}

func (j *rendererJournal) trimLocked() {
	for j.bytes > j.maxBytes && len(j.events) > 0 {
		removed := j.events[0]
		j.events = j.events[1:]
		j.bytes -= rendererJournalEventBytes(removed)
		j.partial = true
	}
}

func rendererJournalEventBytes(event rendererJournalEvent) int {
	if event.Type == "resize" {
		return rendererResizeEventBytes
	}
	return len(event.Data)
}
