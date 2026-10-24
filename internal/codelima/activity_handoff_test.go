package codelima

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

// A successor can accept Activity writes after it acquires the server lock but
// before the predecessor receives the committed acknowledgement. If that
// acknowledgement fails, rollback must discard the predecessor's cached cut.
func TestActivityJournalReloadsAfterFailedSuccessorCommit(t *testing.T) {
	harness := newUpdateHandoffHarness(t)
	old, err := harness.host.activityJournal()
	if err != nil {
		t.Fatal(err)
	}
	first := activityMessageRequest{ID: newID(), Source: "tui", Level: slog.LevelInfo, Text: "before update"}
	if _, err := old.Append(first); err != nil {
		t.Fatal(err)
	}
	done := harness.startUpdate()
	peer, token := harness.dialSuccessor(t)
	harness.receiveHandoff(t, peer, token)
	if err := peer.WriteJSON(daemon.HandoffMessage{Type: "ready"}, nil); err != nil {
		t.Fatal(err)
	}
	message, fds, err := peer.ReadMessage()
	daemon.CloseHandoffFDs(fds)
	if err != nil || message.Type != "commit" {
		t.Fatalf("commit = %#v, %v", message, err)
	}
	// Simulate the journal opened by the now-serving successor. The real
	// handoff protocol above keeps the predecessor's update barrier held.
	successor, err := newActivityJournal(harness.service)
	if err != nil {
		t.Fatal(err)
	}
	second := activityMessageRequest{ID: newID(), Source: "tui", Level: slog.LevelInfo, Text: "successor accepted"}
	accepted, err := successor.Append(second)
	if err != nil {
		t.Fatal(err)
	}
	if err := peer.WriteJSON(daemon.HandoffMessage{Type: "failed", Error: "commit acknowledgement failed"}, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "commit acknowledgement failed") {
			t.Fatal("update did not roll back", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("update rollback did not complete")
	}
	resumed, err := harness.host.activityJournal()
	if err != nil {
		t.Fatal(err)
	}
	if resumed.epoch == old.epoch {
		t.Fatal("rollback reused the predecessor's stale journal epoch")
	}
	duplicate, err := resumed.Append(second)
	if err != nil || duplicate != accepted {
		t.Fatal("rollback lost the successor's acknowledged record", duplicate, err)
	}
	third, err := resumed.Append(activityMessageRequest{ID: newID(), Source: "tui", Level: slog.LevelInfo, Text: "after rollback"})
	if err != nil || third.Sequence != accepted.Sequence+1 {
		t.Fatal("rollback reused an allocated journal sequence", third, err)
	}
	reopened, err := newActivityJournal(harness.service)
	if err != nil {
		t.Fatal("rollback left an unreadable journal", err)
	}
	records, through := reopened.Snapshot()
	if len(records) != 3 || through != third.Sequence {
		t.Fatal("rollback did not preserve all committed messages", records, through)
	}
}
