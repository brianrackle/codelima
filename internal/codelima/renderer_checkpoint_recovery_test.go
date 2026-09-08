//go:build cgo && (darwin || linux)

package codelima

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRendererCheckpointRecoveryKeepsHistoryBeyondRawJournal(t *testing.T) {
	journal := newRendererJournal(128)
	s := newRendererSupervisor("checkpoint-recovery", journal, rendererGhosttyWorkerOptions(t), nil, nil, nil, nil)
	t.Cleanup(s.Close)
	if err := s.Start(context.Background(), 80, 24); err != nil {
		t.Fatal(err)
	}
	if err := s.SendOutput(journal.AppendOutput([]byte("checkpoint-prefix\r\n"))); err != nil {
		t.Fatal(err)
	}
	if err := s.captureCheckpoint(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.SendOutput(journal.AppendOutput([]byte(strings.Repeat("tail\r\n", 20)))); err != nil {
		t.Fatal(err)
	}
	if !journal.Stats().Partial {
		t.Fatal("fixture did not trim raw history")
	}
	before := s.Status().Generation
	s.Restart()
	waitForCondition(t, 5*time.Second, func() bool { st := s.Status(); return st.Generation > before && st.State == rendererStateReady }, "checkpoint replacement")
	read, err := s.Read(ReadRecent, ReadText)
	if err != nil || !strings.Contains(read.Text, "checkpoint-prefix") {
		t.Fatalf("checkpoint history missing: read=%q err=%v", read.Text, err)
	}
	if s.Status().PartialRecovery {
		t.Fatal("complete checkpoint recovery marked partial")
	}
}

func TestRendererCheckpointRejectsMissingAppliedJournalEvents(t *testing.T) {
	link, err := startRendererLink(rendererGhosttyWorkerOptions(t), 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { link.Fail(errTerminalClosed) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := link.Call(ctx, "init", rendererInitParams{TerminalID: "checkpoint-gap", Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	if err := link.Call(ctx, "output", rendererOutputParams{EventID: 2, Data: []byte("second")}); err != nil {
		t.Fatal(err)
	}
	if _, err := link.CallResult(ctx, "checkpoint", rendererCheckpointParams{Through: 2}); err == nil {
		t.Fatal("checkpoint accepted a missing preceding output event")
	}
	if err := link.Call(ctx, "output", rendererOutputParams{EventID: 1, Data: []byte("first-")}); err != nil {
		t.Fatal(err)
	}
	raw, err := link.CallResult(ctx, "read", rendererReadRequest(ReadVisible, ReadText))
	if err != nil {
		t.Fatal(err)
	}
	var read ReadResultDTO
	if err := json.Unmarshal(raw, &read); err != nil {
		t.Fatal(err)
	}
	if read.Text != "first-second" {
		t.Fatalf("output applied out of order: %q", read.Text)
	}
	if _, err := link.CallResult(ctx, "checkpoint", rendererCheckpointParams{Through: 2}); err != nil {
		t.Fatal(err)
	}
}

func TestRendererCheckpointNativeDecodeFailureUsesRawReplay(t *testing.T) {
	journal := newRendererJournal(96)
	s := newRendererSupervisor("checkpoint-invalid-native", journal, rendererGhosttyWorkerOptions(t), nil, nil, nil, nil)
	t.Cleanup(s.Close)
	if err := s.Start(context.Background(), 80, 24); err != nil {
		t.Fatal(err)
	}
	if err := s.SendOutput(journal.AppendOutput([]byte("old-prefix\r\n"))); err != nil {
		t.Fatal(err)
	}
	if err := s.captureCheckpoint(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.SendOutput(journal.AppendOutput([]byte(strings.Repeat("tail\r\n", 16)))); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	changed := *s.checkpoint
	changed.State.Data = []byte("valid envelope with an invalid native FINISH stream")
	changed.SHA256, _ = changed.digest()
	s.checkpoint = &changed
	s.mu.Unlock()
	before := s.Status().Generation
	s.Restart()
	waitForCondition(t, 5*time.Second, func() bool { st := s.Status(); return st.Generation > before && st.State == rendererStateReady }, "native decoder failure fallback")
	read, err := s.Read(ReadRecent, ReadText)
	if err != nil || strings.Contains(read.Text, "old-prefix") || !strings.Contains(read.Text, "tail") || !s.Status().PartialRecovery {
		t.Fatalf("invalid native fallback: %+v status=%+v err=%v", read, s.Status(), err)
	}
}

func TestRendererHandoffRecoveryKeepsCheckpointAndOrderedTail(t *testing.T) {
	journal := newRendererJournal(192)
	journal.AppendResize(80, 24)
	old := newRendererSupervisor("handoff-checkpoint", journal, rendererGhosttyWorkerOptions(t), nil, nil, nil, nil)
	t.Cleanup(old.Close)
	if err := old.Start(context.Background(), 80, 24); err != nil {
		t.Fatal(err)
	}
	if err := old.SendOutput(journal.AppendOutput([]byte("before-handoff\r\n"))); err != nil {
		t.Fatal(err)
	}
	if err := old.captureCheckpoint(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := old.TryResize(journal.AppendResize(100, 30)); err != nil {
		t.Fatal(err)
	}
	if err := old.SendOutput(journal.AppendOutput([]byte(strings.Repeat("tail\r\n", 20)))); err != nil {
		t.Fatal(err)
	}
	encoded := old.handoffRecovery(journal.Snapshot())
	old.Close()
	bundle, err := decodeRendererRecovery("handoff-checkpoint", encoded)
	if err != nil {
		t.Fatal(err)
	}
	adoptedJournal := journalFromRendererRecovery(bundle.Journal)
	replacement := newRendererSupervisor("handoff-checkpoint", adoptedJournal, rendererGhosttyWorkerOptions(t), nil, nil, nil, nil)
	t.Cleanup(replacement.Close)
	if !replacement.restoreCheckpoint(bundle.Checkpoint) {
		t.Fatal("handoff checkpoint quota admission failed")
	}
	if err := replacement.Start(context.Background(), 100, 30); err != nil {
		t.Fatal(err)
	}
	next := adoptedJournal.AppendOutput([]byte("after-handoff"))
	if next.ID != bundle.Journal.LastID+1 {
		t.Fatal("handoff reset journal event identity")
	}
	if err := replacement.SendOutput(next); err != nil {
		t.Fatal(err)
	}
	// A checkpoint call shares the output lane and acts as an applied-watermark
	// barrier, so the following read includes the first post-handoff event.
	if err := replacement.captureCheckpoint(context.Background()); err != nil {
		t.Fatal(err)
	}
	read, err := replacement.Read(ReadRecent, ReadText)
	if err != nil || !strings.Contains(read.Text, "before-handoff") || !strings.Contains(read.Text, "after-handoff") {
		t.Fatalf("handoff recovery lost state: %+v err=%v", read, err)
	}
}

func TestRendererCheckpointRecoveryFallsBackAcrossBuilds(t *testing.T) {
	journal := newRendererJournal(96)
	s := newRendererSupervisor("checkpoint-build-fallback", journal, rendererGhosttyWorkerOptions(t), nil, nil, nil, nil)
	t.Cleanup(s.Close)
	if err := s.Start(context.Background(), 80, 24); err != nil {
		t.Fatal(err)
	}
	if err := s.SendOutput(journal.AppendOutput([]byte("old-prefix\r\n"))); err != nil {
		t.Fatal(err)
	}
	if err := s.captureCheckpoint(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.SendOutput(journal.AppendOutput([]byte(strings.Repeat("tail\r\n", 16)))); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	changed := *s.checkpoint
	changed.BuildID = "different-native-build"
	s.checkpoint = &changed
	s.mu.Unlock()
	before := s.Status().Generation
	s.Restart()
	waitForCondition(t, 5*time.Second, func() bool { st := s.Status(); return st.Generation > before && st.State == rendererStateReady }, "raw fallback replacement")
	read, err := s.Read(ReadRecent, ReadText)
	if err != nil || strings.Contains(read.Text, "old-prefix") || !strings.Contains(read.Text, "tail") {
		t.Fatalf("unexpected fallback: read=%q err=%v", read.Text, err)
	}
	if !s.Status().PartialRecovery {
		t.Fatal("cross-build truncated raw recovery not marked partial")
	}
}
