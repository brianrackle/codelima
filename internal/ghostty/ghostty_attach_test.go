//go:build cgo && (darwin || linux)

package ghostty

import (
	"reflect"
	"testing"

	"github.com/brianrackle/codelima/internal/terminalstate"
)

func TestGhosttyAttachExportRestoresScreenCursorAndInputModes(t *testing.T) {
	for _, screen := range []string{"", "\x1b[?1049h"} {
		t.Run(screen, func(t *testing.T) {
			original := newCaptureTestTerminal(t, "attach-original")
			defer original.Close()
			original.Resize(40, 8)
			original.ingestPTY([]byte(screen + "\x1b[31mhello 界\r\nsecond\x1b[0m\x1b[?2004h\x1b[?1h\x1b[?1000h\x1b[?1006h\x1b[?25l\x1b[3;7H"))
			state := original.ReadVisible(terminalstate.ReadAttachANSI)
			if state.Err != nil {
				t.Fatal(state.Err)
			}
			restored := newCaptureTestTerminal(t, "attach-restored")
			defer restored.Close()
			restored.Resize(40, 8)
			restored.ingestPTY([]byte(state.Text))
			want, got := original.Snapshot(), restored.Snapshot()
			if want.Err != nil || got.Err != nil || !reflect.DeepEqual(want.Snapshot.Cells, got.Snapshot.Cells) || want.Snapshot.CursorX != got.Snapshot.CursorX || want.Snapshot.CursorY != got.Snapshot.CursorY || want.Snapshot.CursorVisible != got.Snapshot.CursorVisible {
				t.Fatalf("export differs: want=%+v got=%+v export=%q", want, got, state.Text)
			}
			for _, mode := range []int{1, 2004, 1000, 1006} {
				if !restored.getModeLocked(mode, false) {
					t.Fatalf("lost input mode %d", mode)
				}
			}
		})
	}
}

func TestGhosttyAttachRefusesIncompleteEscapeEvenAfterCheckpointRestore(t *testing.T) {
	terminal := newCaptureTestTerminal(t, "attach-boundary")
	defer terminal.Close()
	terminal.ingestPTY([]byte("before\x1b]2;partial"))
	if result := terminal.ReadVisible(terminalstate.ReadAttachANSI); result.Err == nil {
		t.Fatal("exported in the middle of an OSC")
	}
	state, err := terminal.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	restored := newCaptureTestTerminal(t, "attach-boundary-restored")
	defer restored.Close()
	if err := restored.RestoreCheckpoint(state); err != nil {
		t.Fatal(err)
	}
	if result := restored.ReadVisible(terminalstate.ReadAttachANSI); result.Err == nil {
		t.Fatal("lost incomplete parser boundary on restore")
	}
	restored.ingestPTY([]byte(" title\x07after"))
	if result := restored.ReadVisible(terminalstate.ReadAttachANSI); result.Err != nil {
		t.Fatal(result.Err)
	}
}
