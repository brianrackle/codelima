package codelima

import (
	"errors"
	"fmt"
	"time"
)

const rendererJournalMaxPendingEvents = 256

// rendererJournalOrder is owned by the worker reader. A resize and PTY output
// may enqueue concurrently after receiving journal IDs; this bounded reorder
// buffer makes native application and checkpoint watermarks follow journal
// order rather than goroutine scheduling.
type rendererJournalOrder struct {
	applied  uint64
	pending  map[uint64]rendererJournalEvent
	bytes    int
	gapSince time.Time
}

func (q *rendererJournalOrder) hasGap() bool { return len(q.pending) != 0 }

func (q *rendererJournalOrder) push(event rendererJournalEvent, apply func(rendererJournalEvent)) error {
	if event.ID == 0 || event.ID >= rendererInputEventBit || (event.Type != "output" && event.Type != "resize") {
		return errors.New("invalid journal event identity or type")
	}
	first := event.FirstID
	if first == 0 {
		first = event.ID
	}
	if first > event.ID || (event.Type != "resize" && first != event.ID) {
		return errors.New("invalid journal event span")
	}
	if event.ID <= q.applied {
		return nil
	}
	if event.Type == "resize" && (!rendererValidGeometry(event.Cols, event.Rows) || !rendererValidPixels(event.CellWidth, event.CellHeight)) {
		return errors.New("invalid journal resize geometry")
	}
	if !q.hasGap() && first <= q.applied+1 {
		apply(event)
		q.applied = event.ID
		return nil
	}
	if _, exists := q.pending[event.ID]; exists {
		return nil
	}
	cost := rendererJournalEventBytes(event)
	if len(q.pending) >= rendererJournalMaxPendingEvents || cost > defaultRendererJournalBytes-q.bytes {
		return errors.New("renderer journal gap buffer is full")
	}
	if q.pending == nil {
		q.pending = map[uint64]rendererJournalEvent{}
	}
	q.pending[event.ID] = event
	q.bytes += cost
	for {
		var next rendererJournalEvent
		for _, candidate := range q.pending {
			start := candidate.FirstID
			if start == 0 {
				start = candidate.ID
			}
			if start <= q.applied+1 && candidate.ID > q.applied && (next.ID == 0 || candidate.ID < next.ID) {
				next = candidate
			}
		}
		if next.ID == 0 {
			break
		}
		apply(next)
		q.applied = next.ID
		for id, covered := range q.pending {
			if id <= q.applied {
				q.bytes -= rendererJournalEventBytes(covered)
				delete(q.pending, id)
			}
		}
	}
	if q.hasGap() && q.gapSince.IsZero() {
		q.gapSince = time.Now()
	} else if !q.hasGap() {
		q.gapSince = time.Time{}
	}
	return nil
}

func (q *rendererJournalOrder) gapError(now time.Time, timeout time.Duration) error {
	if q.hasGap() && !q.gapSince.IsZero() && now.Sub(q.gapSince) >= timeout {
		return fmt.Errorf("renderer journal event %d did not arrive within %s", q.applied+1, timeout)
	}
	return nil
}
