//go:build cgo && (darwin || linux)

package ghostty

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// A publication is a single observation even when output arrives between
// successive requests. The generation and actual visible characters must both
// agree, not just the independently sampled sequence numbers.
func TestGhosttyPublicationCapturesCellsAndTextTogether(t *testing.T) {
	base, err := newGhosttyTUITerminal("publication", nil)
	if err != nil {
		t.Fatal(err)
	}
	terminal := base.(*ghosttyTUITerminal)
	t.Cleanup(terminal.Close)
	terminal.Resize(24, 3)
	var writers sync.WaitGroup
	writers.Add(1)
	go func() {
		defer writers.Done()
		for index := range 100 {
			terminal.sendSync(cmdRendererOutput{Data: []byte(fmt.Sprintf("\x1b[2J\x1b[Hframe-%03d", index))})
		}
	}()
	for range 100 {
		snapshot, visible := terminal.Publish()
		if snapshot.Err != nil || visible.Err != nil {
			t.Fatalf("publish errors: snapshot=%v text=%v", snapshot.Err, visible.Err)
		}
		if snapshot.Snapshot.Generation != visible.Generation {
			t.Fatalf("mixed publication: snapshot=%d text=%d", snapshot.Snapshot.Generation, visible.Generation)
		}
		var row strings.Builder
		for _, cell := range snapshot.Snapshot.Cells[:snapshot.Snapshot.Cols] {
			row.WriteString(cell.Grapheme)
		}
		if got := strings.TrimRight(row.String(), " "); got != visible.Text {
			t.Fatalf("mixed content: row=%q text=%q", got, visible.Text)
		}
	}
	writers.Wait()
	terminal.Close()
	snapshot, visible := terminal.Publish()
	if snapshot.Err == nil || visible.Err == nil {
		t.Fatalf("closed publication must fail: snapshot=%v text=%v", snapshot.Err, visible.Err)
	}
}
