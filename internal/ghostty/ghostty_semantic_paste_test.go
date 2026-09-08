//go:build cgo && (darwin || linux)

package ghostty

import (
	"strings"
	"sync"
	"testing"
)

func TestGhosttySemanticPasteIsOneAtomicBracketedOperation(t *testing.T) {
	terminal := newCaptureTestTerminal(t, "semantic-paste")
	defer terminal.Close()
	terminal.ingestPTY([]byte("\x1b[?2004h"))
	var mu sync.Mutex
	var output strings.Builder
	terminal.mu.Lock()
	terminal.rendererOutput = func(_ uint64, _ uint32, data []byte) {
		mu.Lock()
		defer mu.Unlock()
		output.Write(data)
	}
	terminal.mu.Unlock()
	var group sync.WaitGroup
	for _, value := range []string{"one\ntwo", "three\nfour"} {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := terminal.Paste(value); err != nil {
				t.Errorf("paste: %v", err)
			}
		}()
	}
	group.Wait()
	first, second := "\x1b[200~one\ntwo\x1b[201~", "\x1b[200~three\nfour\x1b[201~"
	if got := output.String(); got != first+second && got != second+first {
		t.Fatalf("pastes interleaved or encoded incorrectly: %q", got)
	}
}

func TestGhosttySemanticPasteRejectsUnsafeAndOversizedTextWithoutWriting(t *testing.T) {
	terminal := newCaptureTestTerminal(t, "unsafe-paste")
	defer terminal.Close()
	var output strings.Builder
	terminal.mu.Lock()
	terminal.rendererOutput = func(_ uint64, _ uint32, data []byte) { output.Write(data) }
	terminal.mu.Unlock()
	for _, value := range []string{"command\nnext", "\x1b[201~injection", strings.Repeat("x", terminalMaxPasteBytes+1)} {
		if err := terminal.Paste(value); err == nil {
			t.Fatalf("unsafe/oversized paste unexpectedly accepted (%d bytes)", len(value))
		}
		if output.Len() != 0 {
			t.Fatalf("rejected paste wrote %q", output.String())
		}
	}
}
