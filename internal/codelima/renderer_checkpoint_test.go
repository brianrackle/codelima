package codelima

import (
	"bytes"
	"strings"
	"testing"
)

func TestRendererCheckpointBindsBuildTerminalWatermarkAndPolicy(t *testing.T) {
	state := terminalCheckpointState{Data: []byte("complete native state"), Cols: 80, Rows: 24, Generation: 7, ViewportOffset: 12, Focused: true}
	checkpoint, err := newRendererCheckpoint("build-a", "term-a", 3, 99, false, state)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkpoint.validate("build-a", "term-a"); err != nil {
		t.Fatal(err)
	}
	if err := checkpoint.validate("build-b", "term-a"); err == nil {
		t.Fatal("checkpoint from another native build was accepted")
	}
	if err := checkpoint.validate("build-a", "term-b"); err == nil {
		t.Fatal("checkpoint from another terminal was accepted")
	}
	state.Data[0] = 'x'
	if string(checkpoint.State.Data) != "complete native state" {
		t.Fatal("checkpoint retained caller-owned mutable bytes")
	}
	for _, mutate := range []func(*rendererCheckpoint){
		func(c *rendererCheckpoint) { c.AppliedEventID++ },
		func(c *rendererCheckpoint) { c.RendererGeneration++ },
		func(c *rendererCheckpoint) { c.State.Focused = false },
		func(c *rendererCheckpoint) { c.State.ViewportOffset++ },
		func(c *rendererCheckpoint) { c.State.Metadata.Title = "changed" },
		func(c *rendererCheckpoint) { c.State.Data = []byte("corrupt") },
		func(c *rendererCheckpoint) { c.Policy.WidthPolicy = "another-width-policy" },
	} {
		changed := *checkpoint
		mutate(&changed)
		if err := changed.validate("build-a", "term-a"); err == nil {
			t.Fatalf("mutated checkpoint was accepted: %#v", changed)
		}
	}
}

func TestRendererCheckpointRejectsUnboundedState(t *testing.T) {
	for _, state := range []terminalCheckpointState{
		{Data: bytes.Repeat([]byte{'x'}, rendererCheckpointMaxBytes+1), Cols: 80, Rows: 24},
		{Data: []byte("data"), Cols: 1 << 30, Rows: 1 << 30},
		{Data: []byte("data"), Cols: 80, Rows: 24, ViewportOffset: -1},
		{Data: []byte("data"), Cols: 80, Rows: 24, Metadata: TerminalMetadata{Title: strings.Repeat("x", 1<<17)}},
	} {
		if _, err := newRendererCheckpoint("build-a", "term-a", 1, 0, false, state); err == nil {
			t.Fatal("unbounded checkpoint state was accepted")
		}
	}
}

func TestRendererCheckpointTailDistinguishesCoalescedResizesFromLostOutput(t *testing.T) {
	journal := newRendererJournal(128)
	covered := journal.AppendOutput([]byte("before checkpoint"))
	journal.AppendResize(100, 30)
	last := journal.AppendResize(120, 40)
	tail, complete := journal.tailAfter(covered.ID)
	if !complete || len(tail.Events) != 1 || tail.Events[0].ID != last.ID || tail.Events[0].Cols != 120 {
		t.Fatalf("coalesced geometry tail = %#v, complete=%v", tail, complete)
	}
	journal.AppendOutput(bytes.Repeat([]byte{'x'}, 128))
	if _, complete := journal.tailAfter(covered.ID); complete {
		t.Fatal("tail with evicted post-checkpoint events was called complete")
	}
	latest := journal.Snapshot()
	tail, complete = journal.tailAfter(latest.LastID)
	if !complete || len(tail.Events) != 0 {
		t.Fatalf("empty current tail = %#v, complete=%v", tail, complete)
	}
}
