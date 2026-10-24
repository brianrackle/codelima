package codelima

import (
	"fmt"
	"strings"
	"testing"

	"go.rockorager.dev/vaxis"
)

func TestTUIManagerDialogKeepsFocusedFieldVisible(t *testing.T) {
	vx := newRenderTestVaxis(t, 76, 16)
	t.Cleanup(vx.Close)
	fields := make([]tuiDialogField, 24)
	for i := range fields {
		fields[i] = newTUIInputField(fmt.Sprint(i), fmt.Sprintf("Field %02d", i), fmt.Sprintf("value-%02d", i), true)
	}
	dialog := newTUIDialog("Many fields", "Save", []string{"Every field must remain reachable."}, fields, nil)
	for i := range fields {
		vx.Window().Clear()
		dialog.Draw(vx.Window(), vaxis.Style{}, tuiMutedStyle(), vaxis.Style{})
		screen := renderedScreenText(t, vx, 76, 16)
		if !strings.Contains(screen, fmt.Sprintf("value-%02d", i)) || !strings.Contains(screen, "Ctrl+s") {
			t.Fatalf("field %d or submit help is clipped:\n%s", i, screen)
		}
		_, _ = dialog.Update(vaxis.Key{Keycode: vaxis.KeyTab})
	}
}

func TestTUIManagerDialogConfirmsDiscardAndKeepsEdits(t *testing.T) {
	dialog := newTUIDialog("Edit", "Save", nil, []tuiDialogField{newTUIInputField("name", "Name", "original", true)}, nil)
	_, _ = dialog.Update(vaxis.Key{Keycode: 'x', Text: "x"})
	if done, _ := dialog.Update(vaxis.Key{Keycode: vaxis.KeyEsc}); done {
		t.Fatal("Esc discarded an edited form without confirmation")
	}
	if done, _ := dialog.Update(vaxis.Key{Keycode: 'n', Text: "n"}); done {
		t.Fatal("keep editing dismissed the form")
	}
	if got := dialog.Fields[0].rawValue(); got != "originalx" {
		t.Fatalf("discard confirmation typed into field: %q", got)
	}
	_, _ = dialog.Update(vaxis.Key{Keycode: vaxis.KeyEsc})
	if done, _ := dialog.Update(vaxis.Key{Keycode: 'y', Text: "y"}); !done {
		t.Fatal("explicit discard did not close form")
	}
	pristine := newTUIDialog("New", "Create", nil, []tuiDialogField{newTUIDefaultInputField("name", "Name", "default", true)}, nil)
	if done, _ := pristine.Update(vaxis.Key{Keycode: vaxis.KeyEsc}); !done {
		t.Fatal("unchanged defaults unnecessarily requested discard confirmation")
	}
}
