package codelima

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"go.rockorager.dev/vaxis"
)

func shortcutLifecycleKey(t *testing.T, sequence string, kind vaxis.EventType) vaxis.Key {
	t.Helper()
	key := decodedVaxisInputKey(t, fmt.Sprintf(sequence, int(kind)+1))
	if key.EventType != kind {
		t.Fatalf("decoded key = %#v, want event type %v", key, kind)
	}
	return key
}

func dispatchShortcutKey(t *testing.T, app *vaxisTUIApp, sequence string, kind vaxis.EventType) {
	t.Helper()
	if quit, err := app.handleEvent(shortcutLifecycleKey(t, sequence, kind)); quit || err != nil {
		t.Fatalf("shortcut dispatch = (%v, %v)", quit, err)
	}
}

func TestTUIShortcutSearchAndInfoLifecycle(t *testing.T) {
	for _, route := range []string{"handleKey", "handleEvent"} {
		t.Run(route, func(t *testing.T) {
			app, terminal := newInspectionTestApp(t)
			app.state.focus = tuiFocusTerminal
			app.state.terminalTarget = nodeTargetKey("node-root")
			dispatch := func(sequence string, kind vaxis.EventType) {
				t.Helper()
				key := shortcutLifecycleKey(t, sequence, kind)
				if route == "handleKey" {
					if app.handleKey(key) {
						t.Fatal("shortcut quit app")
					}
				} else {
					dispatchShortcutKey(t, app, sequence, kind)
				}
			}
			f7 := "\x1b[18;1:%d~"
			for _, closeKey := range []string{f7, "\x1b[27;1:%du"} {
				dispatch(f7, vaxis.EventPress)
				for _, kind := range []vaxis.EventType{vaxis.EventRepeat, vaxis.EventRelease} {
					dispatch(f7, kind)
					if app.search == nil {
						t.Fatal("F7 followup closed search")
					}
				}
				dispatch(closeKey, vaxis.EventPress)
				if app.search != nil {
					t.Fatal("fresh close press did not close search")
				}
				for _, kind := range []vaxis.EventType{vaxis.EventRepeat, vaxis.EventRelease} {
					dispatch(closeKey, kind)
				}
				if app.search != nil || len(terminal.events) != 0 {
					t.Fatalf("close followup reopened search or reached shell: %#v", terminal.events)
				}
			}
			app.state.focusTree()
			for range 2 {
				before := app.state.treePaneMode
				dispatch("\x1b[105;1:%du", vaxis.EventPress)
				if app.state.treePaneMode == before {
					t.Fatal("fresh i press did not toggle pane")
				}
				for _, kind := range []vaxis.EventType{vaxis.EventRepeat, vaxis.EventRelease} {
					dispatch("\x1b[105;1:%du", kind)
					if app.state.treePaneMode == before {
						t.Fatal("i followup toggled pane again")
					}
				}
			}
		})
	}
}

func TestTUIShortcutSelectorFollowupsCannotActOnParent(t *testing.T) {
	for _, sequence := range []string{"\x1b[13;1:%du", "\x1b[27;1:%du", "\x1b[91;5:%du"} {
		t.Run(sequence, func(t *testing.T) {
			app, _ := newInspectionTestApp(t)
			submissions := 0
			parent := newTUIDialog("parent", "save", nil, nil, func(map[string]string) error {
				submissions++
				return nil
			})
			app.overlay = parent
			app.showSelector(newTUISelector("child", nil, []tuiSelectorOption{{Label: "one", Value: "one"}}, nil, false, nil))
			dispatchShortcutKey(t, app, sequence, vaxis.EventPress)
			if app.overlay != parent || submissions != 0 {
				t.Fatal("selector did not restore parent without submitting")
			}
			for _, kind := range []vaxis.EventType{vaxis.EventRepeat, vaxis.EventRepeat, vaxis.EventRelease} {
				dispatchShortcutKey(t, app, sequence, kind)
				if app.overlay != parent || submissions != 0 {
					t.Fatal("selector followup submitted or dismissed parent")
				}
			}
			dispatchShortcutKey(t, app, "\x1b[13;1:%du", vaxis.EventPress)
			if submissions != 1 || app.overlay != nil {
				t.Fatal("fresh Enter did not submit parent exactly once")
			}
		})
	}
}

func TestTUIShortcutOpeningMenuOrFormConsumesFollowups(t *testing.T) {
	for _, code := range []rune{'n', 'a', 'g', 'd', 'c', 'm'} {
		t.Run(string(code), func(t *testing.T) {
			app, _ := newInspectionTestApp(t)
			sequence := fmt.Sprintf("\x1b[%d;1:%%du", code)
			dispatchShortcutKey(t, app, sequence, vaxis.EventPress)
			opened := app.overlay
			if opened == nil {
				t.Fatal("decoded shortcut did not open its overlay")
			}
			for _, kind := range []vaxis.EventType{vaxis.EventRepeat, vaxis.EventRelease} {
				dispatchShortcutKey(t, app, sequence, kind)
				if app.overlay != opened {
					t.Fatal("opening shortcut followup replaced overlay")
				}
			}
			if dialog := app.activeDialog(); dialog != nil {
				for _, field := range dialog.Fields {
					if field.Input != nil && field.Input.String() == string(code) {
						t.Fatal("opening shortcut repeat typed into the form")
					}
				}
			}
		})
	}
	// A menu action can itself open a text field. Its repeats belong to the
	// old menu until a fresh press starts another gesture.
	t.Run("menu replaces itself with form", func(t *testing.T) {
		app, _ := newInspectionTestApp(t)
		dialog := newTUIDialog("form", "save", nil, []tuiDialogField{newTUIInputField("name", "Name", "", false)}, nil)
		app.overlay = &tuiMenu{Entries: []tuiMenuEntry{{Key: 'n', Action: func() error { app.overlay = dialog; return nil }}}}
		dispatchShortcutKey(t, app, "\x1b[110;1:%du", vaxis.EventPress)
		for _, kind := range []vaxis.EventType{vaxis.EventRepeat, vaxis.EventRelease} {
			dispatchShortcutKey(t, app, "\x1b[110;1:%du", kind)
		}
		if app.overlay != dialog || dialog.Fields[0].Input.String() != "" {
			t.Fatal("menu followup changed the new form")
		}
		// A fresh press with the same identity must be accepted immediately.
		dispatchShortcutKey(t, app, "\x1b[110;1:%d;110u", vaxis.EventPress)
		if dialog.Fields[0].Input.String() != "n" {
			t.Fatal("fresh n press was suppressed")
		}
	})
}

func TestTUIShortcutDialogActionsOnlyRunOnPress(t *testing.T) {
	for _, sequence := range []string{"\x1b[13;1:%du", "\x1b[115;5:%du"} {
		t.Run(sequence, func(t *testing.T) {
			calls := 0
			dialog := newTUIDialog("form", "save", nil, nil, func(map[string]string) error {
				calls++
				return errors.New("keep dialog open")
			})
			for _, kind := range []vaxis.EventType{vaxis.EventRelease, vaxis.EventRepeat, vaxis.EventPress, vaxis.EventRepeat, vaxis.EventRelease, vaxis.EventPress} {
				_, _ = dialog.Update(shortcutLifecycleKey(t, sequence, kind))
			}
			if calls != 2 {
				t.Fatalf("submit calls = %d, want 2 presses", calls)
			}
		})
	}
	for _, sequence := range []string{"\x1b[13;1:%du", "\x1b[1;1:%dC"} {
		t.Run("activate/"+sequence, func(t *testing.T) {
			calls := 0
			dialog := newTUIDialog("form", "save", nil, []tuiDialogField{newTUIValueSelectorField("field", "Field", "", nil, func() error { calls++; return nil })}, nil)
			for _, kind := range []vaxis.EventType{vaxis.EventPress, vaxis.EventRepeat, vaxis.EventRelease, vaxis.EventPress} {
				_, _ = dialog.Update(shortcutLifecycleKey(t, sequence, kind))
			}
			if calls != 2 {
				t.Fatalf("activate calls = %d, want 2", calls)
			}
		})
	}
}

func TestTUIShortcutSelectorActionsOnlyRunOnPress(t *testing.T) {
	selector := newTUISelector("multi", nil, []tuiSelectorOption{{Label: "one", Value: "one"}}, nil, true, nil)
	for _, kind := range []vaxis.EventType{vaxis.EventPress, vaxis.EventRepeat, vaxis.EventRelease} {
		_, _ = selector.Update(shortcutLifecycleKey(t, "\x1b[32;1:%d;32u", kind))
		if !selector.Selected["one"] {
			t.Fatal("Space did not select once and stay selected")
		}
	}
	_, _ = selector.Update(shortcutLifecycleKey(t, "\x1b[117;5:%du", vaxis.EventRepeat))
	if !selector.Selected["one"] {
		t.Fatal("Ctrl+u repeat cleared selection")
	}
	_, _ = selector.Update(shortcutLifecycleKey(t, "\x1b[117;5:%du", vaxis.EventPress))
	if len(selector.Selected) != 0 {
		t.Fatal("Ctrl+u press did not clear selection")
	}
	for _, sequence := range []string{"\x1b[13;1:%du", "\x1b[27;1:%du", "\x1b[91;5:%du"} {
		for _, kind := range []vaxis.EventType{vaxis.EventRepeat, vaxis.EventRelease} {
			if done, _ := selector.Update(shortcutLifecycleKey(t, sequence, kind)); done {
				t.Fatal("selector followup confirmed/cancelled")
			}
		}
	}
}

func TestTUIShortcutNavigationRepeatsWithoutReleaseMovement(t *testing.T) {
	for _, sequence := range []string{"\x1b[9;1:%du", "\x1b[1;1:%dB", "\x1b[1;1:%dA"} {
		t.Run("form and selector/"+sequence, func(t *testing.T) {
			dialog := newTUIDialog("fields", "save", nil, []tuiDialogField{{}, {}, {}, {}}, nil)
			selector := newTUISelector("choices", nil, []tuiSelectorOption{{}, {}, {}, {}}, nil, false, nil)
			for _, overlay := range []tuiOverlay{dialog, selector} {
				index := func() int {
					if overlay == dialog {
						return dialog.FieldIndex
					}
					return selector.Index
				}
				for _, kind := range []vaxis.EventType{vaxis.EventPress, vaxis.EventRepeat} {
					before := index()
					_, _ = overlay.Update(shortcutLifecycleKey(t, sequence, kind))
					if index() == before {
						t.Fatal("navigation did not move on press/repeat")
					}
				}
				before := index()
				_, _ = overlay.Update(shortcutLifecycleKey(t, sequence, vaxis.EventRelease))
				if index() != before {
					t.Fatal("release moved selection")
				}
			}
		})
	}
	t.Run("tab switch and reorder", func(t *testing.T) {
		app, _ := newInspectionTestApp(t)
		for range 3 {
			if err := app.openTerminalTab(); err != nil {
				t.Fatal(err)
			}
		}
		for _, sequence := range []string{"\x1b[1;3:%dC", "\x1b[1;4:%dD"} {
			keys := app.sessions.TargetSessionKeys(nodeTargetKey("node-root"))
			app.state.setActiveTab(nodeTargetKey("node-root"), keys[len(keys)-1])
			for _, kind := range []vaxis.EventType{vaxis.EventPress, vaxis.EventRepeat} {
				beforeKeys, beforeActive := app.sessions.TargetSessionKeys(nodeTargetKey("node-root")), app.state.activeSessionKey()
				dispatchShortcutKey(t, app, sequence, kind)
				if beforeActive == app.state.activeSessionKey() && reflect.DeepEqual(beforeKeys, app.sessions.TargetSessionKeys(nodeTargetKey("node-root"))) {
					t.Fatal("tab navigation did not move on press/repeat")
				}
			}
			beforeKeys, beforeActive := app.sessions.TargetSessionKeys(nodeTargetKey("node-root")), app.state.activeSessionKey()
			dispatchShortcutKey(t, app, sequence, vaxis.EventRelease)
			if beforeActive != app.state.activeSessionKey() || !reflect.DeepEqual(beforeKeys, app.sessions.TargetSessionKeys(nodeTargetKey("node-root"))) {
				t.Fatal("release moved tab")
			}
		}
	})
	t.Run("tree", func(t *testing.T) {
		app, _ := newInspectionTestApp(t)
		for index, kind := range []vaxis.EventType{vaxis.EventPress, vaxis.EventRepeat} {
			before := app.state.selectedEntry().key()
			sequence := []string{"\x1b[1;1:%dB", "\x1b[1;1:%dA"}[index]
			dispatchShortcutKey(t, app, sequence, kind)
			if before == app.state.selectedEntry().key() {
				t.Fatal("tree navigation did not move")
			}
		}
		before := app.state.selectedEntry().key()
		dispatchShortcutKey(t, app, "\x1b[1;1:%dA", vaxis.EventRelease)
		if before != app.state.selectedEntry().key() {
			t.Fatal("release moved tree")
		}
	})
	t.Run("messages", func(t *testing.T) {
		view := newTUIMessagesView(make([]tuiMessage, 30))
		view.lastViewport = 5
		for _, kind := range []vaxis.EventType{vaxis.EventPress, vaxis.EventRepeat} {
			before := view.scroll
			_, _ = view.Update(shortcutLifecycleKey(t, "\x1b[1;1:%dB", kind))
			if view.scroll != before+1 {
				t.Fatal("messages did not scroll")
			}
		}
		before := view.scroll
		_, _ = view.Update(shortcutLifecycleKey(t, "\x1b[1;1:%dB", vaxis.EventRelease))
		if view.scroll != before {
			t.Fatal("release scrolled messages")
		}
	})
}

func TestTUIShortcutNodeActionsAndQuitRequirePress(t *testing.T) {
	app, _ := newInspectionTestApp(t)
	for _, code := range []rune{'n', 'a', 'g', 's', 'd', 'c'} {
		for _, kind := range []vaxis.EventType{vaxis.EventRepeat, vaxis.EventRelease, vaxis.EventPaste} {
			if _, ok := app.matchAction(vaxis.Key{Keycode: code, Text: string(code), EventType: kind}); ok {
				t.Fatalf("non-press matched node action %q", code)
			}
		}
		if _, ok := app.matchAction(vaxis.Key{Keycode: code}); !ok {
			t.Fatalf("keycode-only press did not match %q", code)
		}
	}
	for _, sequence := range []string{"\x1b[113;1:%du", "\x1b[99;5:%du"} {
		for _, kind := range []vaxis.EventType{vaxis.EventRepeat, vaxis.EventRelease} {
			if app.handleKey(shortcutLifecycleKey(t, sequence, kind)) {
				t.Fatal("non-press quit app")
			}
		}
	}
	app.overlay = newTUIDialog("form", "save", nil, nil, nil)
	for _, kind := range []vaxis.EventType{vaxis.EventRepeat, vaxis.EventRelease, vaxis.EventPaste} {
		if quit, _ := app.handleEvent(vaxis.Key{Keycode: 'c', Modifiers: vaxis.ModCtrl, EventType: kind}); quit {
			t.Fatal("non-press quit overlay")
		}
	}
	if quit, _ := app.handleEvent(vaxis.Key{Keycode: 'c', Modifiers: vaxis.ModCtrl}); !quit {
		t.Fatal("Ctrl+c press did not quit overlay")
	}
	app.overlay = nil
	if !app.handleKey(vaxis.Key{Keycode: 'q'}) {
		t.Fatal("q press did not quit tree")
	}
}

func TestTUIShortcutDialogFollowupsStayOutOfShell(t *testing.T) {
	for _, sequence := range []string{"\x1b[13;1:%du", "\x1b[115;5:%du", "\x1b[27;1:%du", "\x1b[91;5:%du"} {
		t.Run(sequence, func(t *testing.T) {
			app, terminal := newInspectionTestApp(t)
			if err := app.state.focusTerminal(); err != nil {
				t.Fatal(err)
			}
			app.overlay = newTUIDialog("form", "save", nil, nil, nil)
			press := shortcutLifecycleKey(t, sequence, vaxis.EventPress)
			dispatchShortcutKey(t, app, sequence, vaxis.EventPress)
			if app.overlay != nil {
				t.Fatal("press did not close dialog")
			}
			// Pasted text with the claimed key's identity remains shell payload.
			paste := press
			paste.EventType, paste.Text = vaxis.EventPaste, "pasted text"
			for _, event := range []vaxis.Event{vaxis.PasteStartEvent{}, paste, vaxis.PasteEndEvent{}} {
				if quit, err := app.handleEvent(event); quit || err != nil {
					t.Fatalf("paste dispatch = (%v, %v)", quit, err)
				}
			}
			if len(terminal.events) != 3 || !reflect.DeepEqual(terminal.events[1], normalizeTUITerminalEvent(paste)) {
				t.Fatalf("paste lost to shortcut claim: %#v", terminal.events)
			}
			terminal.events = nil
			for _, kind := range []vaxis.EventType{vaxis.EventRepeat, vaxis.EventRelease} {
				followup := press
				followup.EventType, followup.Modifiers = kind, 0
				if quit, err := app.handleEvent(followup); quit || err != nil {
					t.Fatalf("followup = (%v, %v)", quit, err)
				}
			}
			if len(terminal.events) != 0 {
				t.Fatalf("dialog followup leaked into shell: %#v", terminal.events)
			}
			// Fresh presses and ordinary repeats/releases keep the terminal's
			// keyboard protocol intact, even for the just-consumed key.
			press.Modifiers = 0
			for _, kind := range []vaxis.EventType{vaxis.EventPress, vaxis.EventRepeat, vaxis.EventRelease} {
				key := press
				key.EventType = kind
				if quit, err := app.handleEvent(key); quit || err != nil {
					t.Fatalf("shell input = (%v, %v)", quit, err)
				}
				if len(terminal.events) != 1 || !reflect.DeepEqual(terminal.events[0], normalizeTUITerminalEvent(key)) {
					t.Fatalf("shell input changed: %#v", terminal.events)
				}
				terminal.events = nil
			}
		})
	}
}

func TestTUIShortcutTabFollowupsAfterModifierRelease(t *testing.T) {
	app, _ := newInspectionTestApp(t)
	if err := app.state.focusTerminal(); err != nil {
		t.Fatal(err)
	}
	for _, sequence := range []string{"\x1b[116;3:%du", "\x1b[116;4:%du", "\x1b[119;3:%du"} {
		dispatchShortcutKey(t, app, sequence, vaxis.EventPress)
		backend, ok := app.sessions.SessionTerminal(app.state.activeSessionKey())
		if !ok {
			t.Fatal("missing active tab")
		}
		term := backend.(*fakeTUITerminal)
		for _, kind := range []vaxis.EventType{vaxis.EventRepeat, vaxis.EventRelease} {
			key := shortcutLifecycleKey(t, sequence, kind)
			key.Modifiers = 0
			if quit, err := app.handleEvent(key); quit || err != nil {
				t.Fatalf("followup = (%v, %v)", quit, err)
			}
		}
		if len(term.events) != 0 {
			t.Fatalf("tab shortcut leaked into shell after modifier release: %#v", term.events)
		}
	}
}

func TestTUIShortcutSearchNavigationAndPaste(t *testing.T) {
	app, terminal := newInspectionTestApp(t)
	if err := app.openTerminalSearch(); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []vaxis.EventType{vaxis.EventPress, vaxis.EventRepeat} {
		dispatchShortcutKey(t, app, "\x1b[13;1:%du", kind)
		app.handleInspectionResult(nextInspectionResult(t, app))
		if request := <-terminal.requests; request.Action != "next" {
			t.Fatal(request)
		}
	}
	dispatchShortcutKey(t, app, "\x1b[13;1:%du", vaxis.EventRelease)
	// The next request acts as an ordered barrier: a stray release request
	// would arrive before this explicitly requested previous match.
	dispatchShortcutKey(t, app, "\x1b[13;2:%du", vaxis.EventPress)
	app.handleInspectionResult(nextInspectionResult(t, app))
	if request := <-terminal.requests; request.Action != "previous" {
		t.Fatalf("release advanced search: %+v", request)
	}

	for _, event := range []vaxis.Event{
		vaxis.PasteStartEvent{},
		vaxis.Key{Keycode: vaxis.KeyEsc, Text: "needle", EventType: vaxis.EventPaste},
		vaxis.PasteEndEvent{},
	} {
		if quit, err := app.handleEvent(event); quit || err != nil {
			t.Fatalf("search paste = (%v, %v)", quit, err)
		}
	}
	if app.search == nil || app.search.input.String() != "needle" || len(terminal.events) != 0 {
		t.Fatal("paste closed search or reached shell")
	}
	app.handleInspectionResult(nextInspectionResult(t, app))
	if request := <-terminal.requests; request.Action != "search" || request.Query != "needle" {
		t.Fatalf("paste request = %+v", request)
	}
}

func TestTUIShortcutDialogTextRepeatsAndPasteRemainInput(t *testing.T) {
	app, _ := newInspectionTestApp(t)
	dialog := newTUIDialog("form", "save", nil, []tuiDialogField{newTUIInputField("name", "Name", "", false)}, nil)
	app.overlay = dialog
	for _, kind := range []vaxis.EventType{vaxis.EventPress, vaxis.EventRepeat, vaxis.EventRelease} {
		dispatchShortcutKey(t, app, "\x1b[120;1:%d;120u", kind)
	}
	if dialog.Fields[0].Input.String() != "xx" {
		t.Fatal("text press/repeat/release changed")
	}
	for _, event := range []vaxis.Event{
		vaxis.PasteStartEvent{},
		vaxis.Key{Keycode: 'c', Modifiers: vaxis.ModCtrl, Text: "control", EventType: vaxis.EventPaste},
		vaxis.Key{Keycode: vaxis.KeyEnter, Text: "enter", EventType: vaxis.EventPaste},
		vaxis.PasteEndEvent{},
	} {
		if quit, err := app.handleEvent(event); quit || err != nil {
			t.Fatalf("form paste = (%v, %v)", quit, err)
		}
	}
	if app.overlay != dialog || dialog.Fields[0].Input.String() != "xxcontrolenter" {
		t.Fatal("paste activated a form action")
	}
	// Selector-only fields have no text widget and safely ignore paste.
	app.overlay = newTUIDialog("selector", "save", nil, []tuiDialogField{{}}, nil)
	for _, event := range []vaxis.Event{vaxis.PasteStartEvent{}, vaxis.Key{EventType: vaxis.EventPaste}, vaxis.PasteEndEvent{}} {
		if quit, err := app.handleEvent(event); quit || err != nil {
			t.Fatalf("selector paste = (%v, %v)", quit, err)
		}
	}
}
