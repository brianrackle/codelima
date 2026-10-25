//go:build cgo && (darwin || linux)

package ghostty

import "testing"

func TestGhosttyNotificationCountersSurviveCheckpoint(t *testing.T) {
	term := newCaptureTestTerminal(t, "notices")
	defer term.Close()
	term.ingestPTY([]byte("\a\a\x1b]777;notify;Task;Done\a\x1b]777;notify;Task;Done\a"))
	snapshot := term.Snapshot()
	if snapshot.Err != nil || snapshot.Snapshot.Metadata.BellCount != 2 || snapshot.Snapshot.Metadata.NotificationCount != 2 || snapshot.Snapshot.Metadata.NotificationBody != "Done" {
		t.Fatal("native metadata lost identical notifications", snapshot)
	}
	state, err := term.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	restored := newCaptureTestTerminal(t, "restored-notices")
	defer restored.Close()
	if err := restored.RestoreCheckpoint(state); err != nil {
		t.Fatal(err)
	}
	if got := restored.Snapshot().Snapshot.Metadata; got != snapshot.Snapshot.Metadata {
		t.Fatal("checkpoint changed counters", got)
	}
	restored.ingestPTY([]byte("\x1b]777;notify;Task;Done\a"))
	if got := restored.Snapshot().Snapshot.Metadata.NotificationCount; got != 3 {
		t.Fatal("identical notice after restore was lost", got)
	}
}
