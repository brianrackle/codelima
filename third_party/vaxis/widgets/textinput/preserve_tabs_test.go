package textinput

import (
	"testing"

	"go.rockorager.dev/vaxis"
)

func TestPreserveTabsAcrossLoadPasteAndEditing(t *testing.T) {
	m := New()
	m.PreserveTabs = true
	m.SetContent("界\ttext")
	if m.String() != "界\ttext" || m.CursorPosition() != 6 {
		t.Fatal("loading changed a literal tab", m.String(), m.CursorPosition())
	}
	m.Update(vaxis.Key{EventType: vaxis.EventPaste, Text: "\n\tmore"})
	m.Update(vaxis.PasteEndEvent{})
	if m.String() != "界\ttext\n\tmore" {
		t.Fatal("paste changed a literal tab", m.String())
	}
	m.Update(vaxis.Key{Keycode: vaxis.KeyHome})
	m.Update(vaxis.Key{Keycode: vaxis.KeyRight})
	m.Update(vaxis.Key{Keycode: vaxis.KeyRight})
	m.Update(vaxis.Key{Keycode: vaxis.KeyBackspace})
	if m.String() != "界text\n\tmore" {
		t.Fatal("tab was not one editable character", m.String())
	}
	if got := New().SetContent("a\tb").String(); got != "a        b" {
		t.Fatal("default tab expansion changed", got)
	}
}
