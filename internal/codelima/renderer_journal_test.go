package codelima

import (
	"testing"
)

// TestRendererJournalCoalescesResizeStorms covers finding 2e item 2: a
// window-drag storm used to append an unbounded, never-trimmed run of resize
// events that every future replay had to walk through inside the init deadline.
func TestRendererJournalCoalescesResizeStorms(t *testing.T) {
	t.Parallel()

	journal := newRendererJournal(defaultRendererJournalBytes)
	journal.AppendOutput([]byte("hello"))
	var last rendererJournalEvent
	for cols := 80; cols <= 280; cols++ {
		last = journal.AppendResize(cols, 24)
	}

	snapshot := journal.Snapshot()
	if len(snapshot.Events) != 2 {
		t.Fatalf("resize storm retained %d events, want the output plus one coalesced resize", len(snapshot.Events))
	}
	resize := snapshot.Events[1]
	if resize.Type != "resize" || resize.ID != last.ID || resize.Cols != 280 || resize.Rows != 24 {
		t.Fatalf("coalesced resize = %#v, want the newest geometry with ID %d", resize, last.ID)
	}
	if want := len("hello") + rendererResizeEventBytes; snapshot.Bytes != want {
		t.Fatalf("journal bytes = %d, want %d (output plus one charged resize)", snapshot.Bytes, want)
	}
	if snapshot.Cols != 280 || snapshot.Rows != 24 {
		t.Fatalf("journal geometry = %dx%d, want 280x24", snapshot.Cols, snapshot.Rows)
	}
	if snapshot.Partial {
		t.Fatal("coalescing a resize storm reported lost history")
	}
}

// TestRendererJournalChargesAndTrimsResizeEvents proves resize entries take part
// in byte accounting, so retained resizes are bounded like retained output.
func TestRendererJournalChargesAndTrimsResizeEvents(t *testing.T) {
	t.Parallel()

	journal := newRendererJournal(2 * rendererResizeEventBytes)
	journal.AppendResize(80, 24)
	if got := journal.Stats().Bytes; got != rendererResizeEventBytes {
		t.Fatalf("resize bytes = %d, want %d", got, rendererResizeEventBytes)
	}
	journal.AppendOutput(make([]byte, rendererResizeEventBytes))
	journal.AppendResize(100, 30)

	stats := journal.Stats()
	if stats.Bytes > 2*rendererResizeEventBytes {
		t.Fatalf("journal bytes = %d, want at most %d", stats.Bytes, 2*rendererResizeEventBytes)
	}
	if stats.Events != 2 || !stats.Partial {
		t.Fatalf("journal stats = %#v, want the oldest resize trimmed", stats)
	}
	if stats.Cols != 100 || stats.Rows != 30 {
		t.Fatalf("journal geometry = %dx%d, want 100x30 to survive trimming", stats.Cols, stats.Rows)
	}
}

// TestRendererJournalResetKeepsGeometryAndReportsPartial covers the
// poison-journal escape's data contract: history is dropped, the replacement
// renderer still knows its size, and the terminal is honestly marked partial.
func TestRendererJournalResetKeepsGeometryAndReportsPartial(t *testing.T) {
	t.Parallel()

	journal := newRendererJournal(defaultRendererJournalBytes)
	journal.AppendResize(120, 40)
	journal.AppendOutput([]byte("poisonous replay"))
	journal.Reset()

	stats := journal.Stats()
	if stats.Events != 0 || stats.Bytes != 0 {
		t.Fatalf("reset journal stats = %#v, want an empty journal", stats)
	}
	if !stats.Partial {
		t.Fatal("reset journal did not report partial recovery")
	}
	if stats.Cols != 120 || stats.Rows != 40 {
		t.Fatalf("reset journal geometry = %dx%d, want 120x40", stats.Cols, stats.Rows)
	}
	next := journal.AppendOutput([]byte("after"))
	if next.ID <= 2 {
		t.Fatalf("reset journal reused event ID %d", next.ID)
	}
}
