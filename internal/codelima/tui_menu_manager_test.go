package codelima

import (
	"fmt"
	"strings"
	"testing"

	"go.rockorager.dev/vaxis"
)

func TestTUIManagerMenuScrollsAndSelectsUnnumberedSessions(t *testing.T) {
	vx := newRenderTestVaxis(t, 76, 14)
	t.Cleanup(vx.Close)
	selected := -1
	menu := &tuiMenu{Title: "Shells"}
	for i := range 24 {
		menu.Entries = append(menu.Entries, tuiMenuEntry{Label: fmt.Sprintf("session-%02d", i), Action: func() error { selected = i; return nil }})
	}
	for i := range 24 {
		vx.Window().Clear()
		menu.Draw(vx.Window(), vaxis.Style{}, tuiMutedStyle(), vaxis.Style{})
		screen := renderedScreenText(t, vx, 76, 14)
		if !strings.Contains(screen, fmt.Sprintf("session-%02d", i)) || !strings.Contains(screen, "Enter choose") {
			t.Fatalf("unreachable session %d:\n%s", i, screen)
		}
		if i < 23 {
			_, _ = menu.Update(vaxis.Key{Keycode: vaxis.KeyDown})
		}
	}
	if done, err := menu.Update(vaxis.Key{Keycode: vaxis.KeyEnter}); !done || err != nil || selected != 23 {
		t.Fatalf("selected=%d done=%v err=%v", selected, done, err)
	}
}

func TestTUIManagerMenuShiftChoiceRunsOnlyOnce(t *testing.T) {
	normal, shifted := 0, 0
	menu := &tuiMenu{Entries: []tuiMenuEntry{{Key: 'w', Action: func() error { normal++; return nil }, ShiftAction: func() error { shifted++; return nil }}}}
	for _, kind := range []vaxis.EventType{vaxis.EventPress, vaxis.EventRepeat, vaxis.EventRelease} {
		_, _ = menu.Update(vaxis.Key{Keycode: 'W', Text: "W", EventType: kind})
	}
	if normal != 0 || shifted != 1 {
		t.Fatalf("normal=%d shifted=%d", normal, shifted)
	}
}
