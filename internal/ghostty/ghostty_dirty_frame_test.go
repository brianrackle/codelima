//go:build cgo && (darwin || linux)

package ghostty

import "testing"

func TestGhosttyDirtyFrameReusesPrivateRowsWithoutCallerAliases(t *testing.T) {
	terminal, err := New("dirty-row-cache", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(terminal.Close)
	terminal.Resize(20, 4)
	terminal.Output(1, []byte("first\r\n\x1b]8;;https://example.com\x1b\\linked\x1b]8;;\x1b\\\r\nthird"))
	first := terminal.Snapshot()
	if first.Err != nil {
		t.Fatal(first.Err)
	}
	terminal.mu.Lock()
	row := &terminal.cachedFrameRows[1][0]
	terminal.mu.Unlock()
	first.Snapshot.Cells[20].Grapheme = "caller mutation"
	unchanged := terminal.Snapshot()
	if unchanged.Err != nil {
		t.Fatal(unchanged.Err)
	}
	terminal.mu.Lock()
	reused := &terminal.cachedFrameRows[1][0] == row
	terminal.mu.Unlock()
	if !reused || unchanged.Snapshot.Cells[20].Grapheme != "l" || unchanged.Snapshot.Cells[20].Hyperlink != "https://example.com" {
		t.Fatalf("clean row was regenerated or externally mutated: reused=%v cell=%+v", reused, unchanged.Snapshot.Cells[20])
	}
	terminal.Output(2, []byte("\x1b[1;1Hchanged"))
	partial := terminal.Snapshot()
	if partial.Err != nil {
		t.Fatal(partial.Err)
	}
	terminal.mu.Lock()
	reused = &terminal.cachedFrameRows[1][0] == row
	terminal.mu.Unlock()
	if !reused || partial.Snapshot.Cells[0].Grapheme != "c" || unchanged.Snapshot.Cells[0].Grapheme != "f" {
		t.Fatalf("partial frame did not preserve immutable rows: reused=%v", reused)
	}
	foreground := uint32(0x123456)
	if _, err := terminal.Interact(TerminalInteractionRequest{Action: "colors", Colors: &TerminalColors{Foreground: &foreground}}); err != nil {
		t.Fatal(err)
	}
	colors := terminal.Snapshot()
	if colors.Err != nil || colors.Snapshot.Cells[20].FG != 0x123456 {
		t.Fatalf("color change failed to invalidate rows: err=%v cell=%+v", colors.Err, colors.Snapshot.Cells[20])
	}
}
