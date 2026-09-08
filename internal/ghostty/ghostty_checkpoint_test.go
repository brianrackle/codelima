//go:build cgo && (darwin || linux)

package ghostty

import (
	"bytes"
	"reflect"
	"testing"
)

func TestGhosttyCheckpointRetainsSplitVTContinuationAndCanCheckpointAgain(t *testing.T) {
	for _, test := range []struct{ name, prefix, suffix string }{
		{"utf8", "text \xe7", "\x95\x8c end"},
		{"csi", "text\x1b[38;2;", "120;30;90mcolored"},
		{"osc", "\x1b]2;split", " title\x07after"},
		{"osc_link", "\x1b]8;;https://", "example.test\x1b\\link\x1b]8;;\x1b\\"},
		{"dcs", "\x1bP$q", "m\x1b\\after"},
		{"apc", "\x1b_ignored", "\x1b\\after"},
	} {
		t.Run(test.name, func(t *testing.T) {
			original := newCaptureTestTerminal(t, "checkpoint-original")
			defer original.Close()
			original.Resize(40, 4)
			original.ingestPTY([]byte(test.prefix))
			state, err := original.Checkpoint()
			if err != nil {
				t.Fatal(err)
			}
			restored := newCaptureTestTerminal(t, "checkpoint-restored")
			defer restored.Close()
			if err := restored.RestoreCheckpoint(state); err != nil {
				t.Fatal(err)
			}
			second, err := restored.Checkpoint()
			if err != nil {
				t.Fatalf("immediate second checkpoint: %v", err)
			}
			again := newCaptureTestTerminal(t, "checkpoint-again")
			defer again.Close()
			if err := again.RestoreCheckpoint(second); err != nil {
				t.Fatal(err)
			}
			for _, terminal := range []*ghosttyTUITerminal{original, restored, again} {
				terminal.ingestPTY([]byte(test.suffix))
			}
			want := original.Snapshot()
			if want.Err != nil {
				t.Fatal(want.Err)
			}
			for _, terminal := range []*ghosttyTUITerminal{restored, again} {
				got := terminal.Snapshot()
				if got.Err != nil {
					t.Fatal(got.Err)
				}
				if !reflect.DeepEqual(got.Snapshot.Cells, want.Snapshot.Cells) || got.Snapshot.CursorX != want.Snapshot.CursorX || got.Snapshot.CursorY != want.Snapshot.CursorY {
					t.Fatalf("restored continuation differs: got=%q want=%q", terminal.ReadVisible(ReadANSI).Text, original.ReadVisible(ReadANSI).Text)
				}
			}
		})
	}
}

func TestGhosttyCheckpointRejectsCorruptionWithoutReplacingLiveState(t *testing.T) {
	terminal := newCaptureTestTerminal(t, "checkpoint-corruption")
	defer terminal.Close()
	terminal.ingestPTY([]byte("retained screen"))
	state, err := terminal.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	for _, corrupt := range [][]byte{state.Data[:len(state.Data)/2], append(bytes.Clone(state.Data), 1), []byte("invalid")} {
		bad := state
		bad.Data = corrupt
		if err := terminal.RestoreCheckpoint(bad); err == nil {
			t.Fatal("accepted corrupt checkpoint")
		}
		if got := terminal.ReadVisible(ReadText); got.Err != nil || got.Text != "retained screen" {
			t.Fatalf("failed restore damaged current terminal: %+v", got)
		}
	}
}

func TestGhosttyCheckpointRestoresNonBottomViewport(t *testing.T) {
	terminal := newCaptureTestTerminal(t, "checkpoint-viewport")
	defer terminal.Close()
	if err := terminal.ResizePixels(30, 4, 11, 23); err != nil {
		t.Fatal(err)
	}
	terminal.ingestPTY([]byte("one\r\ntwo\r\nthree\r\nfour\r\nfive\r\nsix\r\nseven"))
	terminal.Scroll(-2)
	want := terminal.ReadVisible(ReadText)
	state, err := terminal.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	restored := newCaptureTestTerminal(t, "checkpoint-viewport-restored")
	defer restored.Close()
	if err := restored.RestoreCheckpoint(state); err != nil {
		t.Fatal(err)
	}
	if got := restored.ReadVisible(ReadText); got.Err != nil || got.Text != want.Text {
		t.Fatalf("viewport restore got=%+v want=%+v", got, want)
	}
	again, err := restored.Checkpoint()
	if err != nil || again.CellWidth != 11 || again.CellHeight != 23 || again.ViewportOffset != state.ViewportOffset {
		t.Fatalf("pixel geometry/viewport policy lost: %+v err=%v", again, err)
	}
}
