package codelima

import (
	"slices"
	"sync"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

const defaultRendererJournalBytes = daemon.MaxHandoffReplayBytesPerTerminal

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
}

type rendererJournal struct {
	mu       sync.Mutex
	maxBytes int
	nextID   uint64
	events   []rendererJournalEvent
	bytes    int
	partial  bool
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
	j.bytes += len(event.Data)
	j.trimLocked()
	return event
}

func (j *rendererJournal) AppendResize(cols, rows int) rendererJournalEvent {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.nextID++
	event := rendererJournalEvent{ID: j.nextID, Type: "resize", Cols: cols, Rows: rows}
	j.events = append(j.events, event)
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
	return rendererJournalSnapshot{Events: events, Partial: j.partial, Bytes: j.bytes}
}

func (j *rendererJournal) trimLocked() {
	for j.bytes > j.maxBytes && len(j.events) > 0 {
		removed := j.events[0]
		j.events = j.events[1:]
		j.bytes -= len(removed.Data)
		j.partial = true
	}
}
