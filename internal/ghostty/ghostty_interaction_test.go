//go:build cgo && (darwin || linux)

package ghostty

import "testing"

func TestGhosttyClipboardReleaseWithoutSelectionIsNoOp(t *testing.T) {
	terminal, err := New("empty-selection", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(terminal.Close)
	terminal.Resize(20, 4)
	terminal.Output(1, []byte("alpha beta"))
	for _, priorSelection := range []bool{false, true} {
		if priorSelection {
			if _, err := terminal.Interact(TerminalInteractionRequest{Action: "press", Kind: "word", Col: 1}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := terminal.Interact(TerminalInteractionRequest{Action: "press", Col: 1}); err != nil {
			t.Fatal(err)
		}
		result, err := terminal.Interact(TerminalInteractionRequest{Action: "release", Col: 1})
		if err != nil || result.Text != "" {
			t.Fatalf("click without drag (prior selection=%v): text=%q err=%v", priorSelection, result.Text, err)
		}
	}
	if _, err := terminal.Interact(TerminalInteractionRequest{Action: "press", Kind: "word", Col: 1}); err != nil {
		t.Fatal(err)
	}
	result, err := terminal.Interact(TerminalInteractionRequest{Action: "release", Col: 1})
	if err != nil || result.Text != "alpha" {
		t.Fatalf("word selection release: text=%q err=%v", result.Text, err)
	}
}

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
