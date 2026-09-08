//go:build cgo && (darwin || linux)

package ghostty

import (
	"testing"

	"github.com/brianrackle/codelima/internal/terminalstate"
)

func TestGhosttyCheckpointRestoresOwnedDefaultColorPolicyWithoutReports(t *testing.T) {
	original, err := New("checkpoint-colors", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(original.Close)
	fg, bg := uint32(0x123456), uint32(0x654321)
	palette := make([]uint32, 256)
	palette[0] = 0x102030
	_, err = original.Interact(TerminalInteractionRequest{Action: "colors", Colors: &TerminalColors{Foreground: &fg, Background: &bg, Palette: palette, Theme: 1}})
	if err != nil {
		t.Fatal(err)
	}
	fg, bg, palette[0] = 0xffffff, 0xffffff, 0xffffff
	original.Output(1, []byte("\x1b[?2031hx"))
	state, err := original.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	if state.Colors == nil || *state.Colors.Foreground != 0x123456 || *state.Colors.Background != 0x654321 || state.Colors.Palette[0] != 0x102030 {
		t.Fatalf("checkpoint borrowed original color buffers: %+v", state.Colors)
	}
	restored, err := New("restored-colors", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restored.Close)
	if err := restored.RestoreCheckpoint(state); err != nil {
		t.Fatal(err)
	}
	if report := restored.readPendingResponses(); report != "" {
		t.Fatalf("restore emitted unsolicited report: %q", report)
	}
	*state.Colors.Foreground = 0xffffff
	state.Colors.Palette[0] = 0xffffff
	restored.Output(2, []byte("\x1b]110\a\x1b]111\a\x1b]104\a"))
	snapshot := restored.Snapshot()
	if snapshot.Err != nil {
		t.Fatal(snapshot.Err)
	}
	if snapshot.Snapshot.Cells[0].FG != 0x123456 || snapshot.Snapshot.Cells[0].BG != 0x654321 {
		t.Fatalf("native default colors lost on restore: %+v", snapshot.Snapshot.Cells[0])
	}
	second, err := restored.Checkpoint()
	if err != nil || second.Colors == nil || second.Colors.Palette[0] != 0x102030 {
		t.Fatalf("restored policy retained envelope borrows: %+v err=%v", second.Colors, err)
	}
	bad := terminalstate.CloneColors(second.Colors)
	bad.Theme = 3
	second.Colors = bad
	if err := restored.RestoreCheckpoint(second); err == nil {
		t.Fatal("invalid checkpoint colors accepted")
	}
}
