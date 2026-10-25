package codelima

import (
	"slices"
	"strings"
	"testing"

	"go.rockorager.dev/vaxis"
)

func TestTUICommandEditorPreservesMultilineBlocksPasteAndOrder(t *testing.T) {
	a, parent := testTemplateEditor(t)
	var saved []string
	e := newTUICommandEditor(a, parent, []string{"echo first"}, func(commands []string) { saved = commands })
	a.overlay = e
	a.updateOverlay(vaxis.Key{Keycode: 'n', Modifiers: vaxis.ModCtrl})
	a.updateOverlay(vaxis.PasteStartEvent{})
	a.updateOverlay(vaxis.Key{EventType: vaxis.EventPaste, Text: "printf '界👩‍💻'\n# Ctrl+s is pasted text\nprintf done"})
	if saved != nil {
		t.Fatal("paste submitted commands")
	}
	a.updateOverlay(vaxis.PasteEndEvent{})
	a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyUp, Modifiers: vaxis.ModAlt})
	a.updateOverlay(vaxis.Key{Keycode: 's', Modifiers: vaxis.ModCtrl, EventType: vaxis.EventRelease})
	if saved != nil {
		t.Fatal("release submitted commands")
	}
	a.updateOverlay(vaxis.Key{Keycode: 's', Modifiers: vaxis.ModCtrl})
	if a.overlay != parent || len(saved) != 2 || saved[1] != "echo first" || !strings.Contains(saved[0], "\n# Ctrl+s") {
		t.Fatal("command boundaries/order changed", saved)
	}
}

func TestTUICommandEditorPreservesDecodedPasteControls(t *testing.T) {
	a, parent := testTemplateEditor(t)
	var saved []string
	e := newTUICommandEditor(a, parent, nil, func(commands []string) { saved = commands })
	a.overlay = e
	for _, event := range []vaxis.Event{
		vaxis.PasteStartEvent{},
		vaxis.Key{EventType: vaxis.EventPaste, Text: "printf '界'"},
		vaxis.Key{EventType: vaxis.EventPaste, Keycode: 'j', Modifiers: vaxis.ModCtrl},
		vaxis.Key{EventType: vaxis.EventPaste, Keycode: vaxis.KeyTab},
		vaxis.Key{EventType: vaxis.EventPaste, Text: "printf second"},
		vaxis.Key{EventType: vaxis.EventPaste, Keycode: 'm', Modifiers: vaxis.ModCtrl},
		vaxis.Key{EventType: vaxis.EventPaste, Keycode: 'j', Modifiers: vaxis.ModCtrl},
		vaxis.Key{EventType: vaxis.EventPaste, Text: "printf last"},
		vaxis.PasteEndEvent{},
	} {
		_, _ = a.handleEvent(event)
	}
	if saved != nil || a.overlay != e {
		t.Fatal("paste executed an editor action")
	}
	a.updateOverlay(vaxis.Key{Keycode: 's', Modifiers: vaxis.ModCtrl})
	want := []string{"printf '界'\n\tprintf second\nprintf last"}
	if !slices.Equal(saved, want) {
		t.Fatalf("decoded paste changed command bytes: got %q, want %q", saved, want)
	}
	if line := templateCommandLine(saved[0]); !strings.Contains(line, "↵") {
		t.Fatalf("command preview concealed newlines: %q", line)
	}
}

func TestTUICommandEditorUnicodeSplitJoinDiscardAndEmpty(t *testing.T) {
	a, parent := testTemplateEditor(t)
	e := newTUICommandEditor(a, parent, []string{"界👩‍💻ab"}, func(commands []string) { t.Fatal("discard saved", commands) })
	a.overlay = e
	setCommandCursor(e.buffers[0], 2)
	a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyEnter})
	if e.buffers[0].String() != "界👩‍💻\nab" {
		t.Fatal("newline split a grapheme", e.buffers[0].String())
	}
	a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyBackspace})
	if e.buffers[0].String() != "界👩‍💻ab" {
		t.Fatal("backspace did not rejoin lines")
	}
	a.updateOverlay(vaxis.Key{Keycode: 'z', Text: "z"})
	a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyEsc})
	if !e.confirmDiscard {
		t.Fatal("edited script discarded without confirmation")
	}
	a.updateOverlay(vaxis.Key{Keycode: 'n', Text: "n"})
	if e.confirmDiscard || a.overlay != e {
		t.Fatal("keep editing failed")
	}
	a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyEsc})
	a.updateOverlay(vaxis.Key{Keycode: 'y', Text: "y"})
	if a.overlay != parent {
		t.Fatal("discard did not return to parent")
	}
	var saved []string
	e = newTUICommandEditor(a, parent, []string{"one"}, func(commands []string) { saved = commands })
	a.overlay = e
	a.updateOverlay(vaxis.Key{Keycode: 'x', Modifiers: vaxis.ModCtrl})
	a.updateOverlay(vaxis.Key{Keycode: 's', Modifiers: vaxis.ModCtrl})
	if saved == nil || !slices.Equal(saved, []string{}) {
		t.Fatal("empty command list must be explicit")
	}
}

func TestTUICommandEditorWrapAndCursorRemainVisible(t *testing.T) {
	a, parent := testTemplateEditor(t)
	a.vx.Resize(vaxis.Resize{Cols: 80, Rows: 24})
	e := newTUICommandEditor(a, parent, []string{strings.Repeat("界é", 100) + "\nLAST-LINE"}, func([]string) {})
	a.overlay = e
	a.draw()
	screen := renderedScreenText(t, a.vx, 80, 24)
	if !strings.Contains(screen, "LAST-LINE") {
		t.Fatalf("active line was clipped\n%s", screen)
	}
	a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyUp})
	a.updateOverlay(vaxis.Key{Keycode: vaxis.KeyHome})
	a.updateOverlay(vaxis.Key{Keycode: 'Z', Text: "Z"})
	if !strings.Contains(e.buffers[0].String(), "Z") {
		t.Fatal("wrapped cursor was not editable")
	}
}
