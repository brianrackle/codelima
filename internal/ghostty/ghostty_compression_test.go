//go:build cgo && (darwin || linux)

package ghostty

import (
	"strings"
	"testing"
	"time"
)

func TestGhosttyCompressionIdlePassStopsWakingAndPreservesHistory(t *testing.T) {
	terminal, err := New("compression", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(terminal.Close)
	if _, err := terminal.SetIdleCompression(true); err != nil {
		t.Fatal(err)
	}
	terminal.Resize(100, 20)
	terminal.Output(1, []byte(strings.Repeat(strings.Repeat("a", 99)+"\r\n", 1000)))
	want := terminal.ReadRecent(ReadText)
	if want.Err != nil {
		t.Fatal(want.Err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var completed CompressionStats
	for {
		status, err := terminal.CompressionStatus()
		if err != nil {
			t.Fatal(err)
		}
		if status.LastError != "" {
			t.Fatal(status.LastError)
		}
		if status.Steps > 0 && !status.Pending {
			completed = status
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("idle compression did not complete: %+v", status)
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(3 * ghosttyCompressionStep)
	stable, err := terminal.CompressionStatus()
	if err != nil || stable.Steps != completed.Steps || stable.Wakeups != completed.Wakeups {
		t.Fatalf("completed compression kept waking: before=%+v after=%+v err=%v", completed, stable, err)
	}
	if got := terminal.ReadRecent(ReadText); got.Err != nil || got.Text != want.Text {
		t.Fatalf("compression changed history: err=%v equal=%v", got.Err, got.Text == want.Text)
	}
}

func TestGhosttyCompressionCanBeDisabled(t *testing.T) {
	terminal, err := New("compression-disabled", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(terminal.Close)
	if _, err := terminal.SetIdleCompression(false); err != nil {
		t.Fatal(err)
	}
	terminal.Output(1, []byte(strings.Repeat("history\r\n", 1000)))
	status, err := terminal.CompressionStatus()
	if err != nil || status.Enabled || status.Pending || status.Steps != 0 {
		t.Fatalf("disabled compression ran: %+v, %v", status, err)
	}
}
