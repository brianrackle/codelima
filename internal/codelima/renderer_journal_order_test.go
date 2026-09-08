package codelima

import (
	"bytes"
	"slices"
	"testing"
	"time"
)

func TestRendererJournalOrderWaitsForGapsAndAppliesAContiguousPrefix(t *testing.T) {
	order := rendererJournalOrder{}
	var applied []uint64
	apply := func(event rendererJournalEvent) { applied = append(applied, event.ID) }
	if err := order.push(rendererJournalEvent{ID: 2, Type: "output", Data: []byte("second")}, apply); err != nil {
		t.Fatal(err)
	}
	if order.applied != 0 || len(applied) != 0 || !order.hasGap() {
		t.Fatalf("uncovered event was applied: %#v, %v", order, applied)
	}
	if err := order.push(rendererJournalEvent{ID: 1, Type: "output", Data: []byte("first")}, apply); err != nil {
		t.Fatal(err)
	}
	if order.applied != 2 || order.hasGap() || !slices.Equal(applied, []uint64{1, 2}) {
		t.Fatalf("ordered prefix = %v, watermark=%d, gap=%v", applied, order.applied, order.hasGap())
	}
	if err := order.push(rendererJournalEvent{ID: 1, Type: "output", Data: []byte("duplicate")}, apply); err != nil || len(applied) != 2 {
		t.Fatalf("covered event was applied twice: %v, %v", applied, err)
	}
}

func TestRendererJournalOrderBoundsMissingEventsAndAcceptsResizeSpans(t *testing.T) {
	order := rendererJournalOrder{}
	if err := order.push(rendererJournalEvent{ID: 3, FirstID: 1, Type: "resize", Cols: 120, Rows: 40}, func(rendererJournalEvent) {}); err != nil {
		t.Fatal(err)
	}
	if order.applied != 3 || order.hasGap() {
		t.Fatalf("coalesced resize prefix = %#v", order)
	}
	if err := order.push(rendererJournalEvent{ID: 5, Type: "output", Data: []byte("gap")}, func(rendererJournalEvent) {}); err != nil {
		t.Fatal(err)
	}
	if err := order.gapError(time.Now().Add(time.Minute), time.Second); err == nil {
		t.Fatal("an unresolved gap had no bounded failure")
	}
	if err := order.push(rendererJournalEvent{ID: 6, Type: "output", Data: bytes.Repeat([]byte{'x'}, defaultRendererJournalBytes+1)}, func(rendererJournalEvent) {}); err == nil {
		t.Fatal("unbounded pending output was accepted")
	}
}
