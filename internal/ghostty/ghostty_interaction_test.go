//go:build cgo && (darwin || linux)

package ghostty

import "testing"

func TestGhosttyInteractionRejectsNarrowingCoordinatesWithoutMutation(t *testing.T) {
	terminal, err := New("interaction-bounds", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(terminal.Close)
	terminal.Resize(20, 4)
	terminal.Output(1, []byte("alpha beta"))
	if _, err := terminal.Interact(TerminalInteractionRequest{Action: "press", Kind: "word", Col: 1}); err != nil {
		t.Fatal(err)
	}
	before, err := terminal.Interact(TerminalInteractionRequest{Action: "copy"})
	if err != nil || before.Text != "alpha" {
		t.Fatalf("initial selection=%q err=%v", before.Text, err)
	}
	maxInt := int(^uint(0) >> 1)
	for _, request := range []TerminalInteractionRequest{
		{Action: "press", Col: -1}, {Action: "press", Row: maxInt},
		{Action: "drag", Col: maxInt}, {Action: "release", Row: -maxInt - 1},
		{Action: "cancel", Col: 65536},
	} {
		if _, err := terminal.Interact(request); err == nil {
			t.Errorf("accepted invalid request: %+v", request)
		}
	}
	after, err := terminal.Interact(TerminalInteractionRequest{Action: "copy"})
	if err != nil || after.Text != before.Text {
		t.Fatalf("invalid request mutated selection: after=%q err=%v", after.Text, err)
	}
}
