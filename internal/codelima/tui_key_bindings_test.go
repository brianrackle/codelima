package codelima

import (
	"reflect"
	"strings"
	"testing"

	"go.rockorager.dev/vaxis"
)

func TestTUIKeyBindingsOverlayTextAndMenuActions(t *testing.T) {
	a, _ := newTestManager(t, 100, 30)
	a.applyKeyBindings(KeyBindingConfig{"dialog.submit": {"alt+s"}, "dialog.cancel": {"alt+q"}, "dialog.next": {"alt+n"}, "menu.configuration_create": {"alt+c"}})
	if a.keyBindingError != "" {
		t.Fatal(a.keyBindingError)
	}
	var submitted map[string]string
	dialog := newTUIDialog("Edit", "Save", nil, []tuiDialogField{newTUIInputField("name", "Name", "", true), newTUIInputField("value", "Value", "", false)}, func(values map[string]string) error { submitted = values; return nil })
	a.overlay = dialog
	for _, r := range "1n?" {
		a.handleKey(vaxis.Key{Keycode: r, Text: string(r)})
	}
	a.updateOverlay(vaxis.PasteStartEvent{})
	a.handleKey(vaxis.Key{Keycode: 'd', Text: "d", EventType: vaxis.EventPaste})
	a.updateOverlay(vaxis.PasteEndEvent{})
	a.handleKey(vaxis.Key{Keycode: 's', Modifiers: vaxis.ModCtrl})
	if submitted != nil || dialog.Fields[0].Input.String() != "1n?d" {
		t.Fatal("text was interpreted as a shortcut", submitted, dialog.Fields[0].Input.String())
	}
	a.handleKey(vaxis.Key{Keycode: 'n', Modifiers: vaxis.ModAlt})
	if dialog.FieldIndex != 1 {
		t.Fatal("configured field navigation failed")
	}
	a.handleKey(vaxis.Key{Keycode: 's', Modifiers: vaxis.ModAlt})
	if submitted["name"] != "1n?d" || a.overlay != nil {
		t.Fatal("configured submission failed", submitted)
	}
	called := 0
	a.overlay = &tuiMenu{Entries: []tuiMenuEntry{{Key: 'c', BindingAction: "menu.configuration_create", Action: func() error { called++; return nil }}}}
	a.handleKey(vaxis.Key{Text: "c"})
	if called != 0 {
		t.Fatal("old menu shortcut remained active")
	}
	a.handleKey(vaxis.Key{Keycode: 'c', Modifiers: vaxis.ModAlt})
	if called != 1 {
		t.Fatal("configured menu shortcut failed")
	}
}

func TestTUIKeyBindingsEmbeddedPayloadAndFocus(t *testing.T) {
	a, _ := newTestManager(t, 100, 30)
	enableTestEmbeddedShell(a)
	term := newFakeTUITerminal()
	node := a.state.selectedEntry().node
	putTestSession(a.sessions, nodeTargetKey(node.ID), &tuiSession{}, term)
	a.applyKeyBindings(KeyBindingConfig{"terminal.focus": {"alt+f"}})
	a.handleKey(vaxis.Key{Keycode: 'f', Modifiers: vaxis.ModAlt})
	if a.state.focus != tuiFocusTerminal {
		t.Fatal("configured focus key did not enter shell")
	}
	for _, key := range []vaxis.Key{{Keycode: vaxis.KeyF06}, {Keycode: vaxis.KeyLeft, Modifiers: vaxis.ModAlt}, {Keycode: 'f', Modifiers: vaxis.ModAlt, EventType: vaxis.EventPaste}} {
		if !a.isTerminalPayloadKey(key) {
			t.Fatal("payload was intercepted", key)
		}
		a.handleKey(key)
		if len(term.events) == 0 || !reflect.DeepEqual(term.events[len(term.events)-1], normalizeTUITerminalEvent(key)) {
			t.Fatal("payload changed", key, term.events)
		}
	}
	before := len(term.events)
	a.handleKey(vaxis.Key{Keycode: 'f', Modifiers: vaxis.ModAlt})
	for _, kind := range []vaxis.EventType{vaxis.EventRepeat, vaxis.EventRelease} {
		a.handleKey(vaxis.Key{Keycode: 'f', Text: "f", EventType: kind})
	}
	if a.state.focus != tuiFocusTree || len(term.events) != before {
		t.Fatal("focus lifecycle leaked to shell")
	}
}

func TestTUIKeyBindingsEmbeddedTabReordering(t *testing.T) {
	for _, remapped := range []bool{false, true} {
		t.Run(map[bool]string{false: "defaults", true: "remapped"}[remapped], func(t *testing.T) {
			a, _ := newTestManager(t, 100, 30)
			enableTestEmbeddedShell(a)
			target := nodeTargetKey(a.state.selectedEntry().node.ID)
			var term *fakeTUITerminal
			var keys []string
			for i := range 3 {
				candidate := newFakeTUITerminal()
				if i == 1 {
					term = candidate
				}
				keys = append(keys, putTestSession(a.sessions, target, &tuiSession{}, candidate))
			}
			active := keys[1]
			a.state.setActiveTab(target, active)
			a.openNodeDetail(tuiDetailShell)
			left := vaxis.Key{Keycode: vaxis.KeyLeft, Modifiers: vaxis.ModAlt | vaxis.ModShift}
			right := vaxis.Key{Keycode: vaxis.KeyRight, Modifiers: vaxis.ModAlt | vaxis.ModShift}
			if remapped {
				a.applyKeyBindings(KeyBindingConfig{"terminal.move_tab_left": {"alt+h"}, "terminal.move_tab_right": {"alt+l"}})
				if a.keyBindingError != "" {
					t.Fatal(a.keyBindingError)
				}
				a.handleKey(left)
				if got := a.sessions.TargetSessionKeys(target); !reflect.DeepEqual(got, keys) {
					t.Fatalf("disabled default reordered tabs: %v", got)
				}
				left = vaxis.Key{Keycode: 'h', Modifiers: vaxis.ModAlt}
				right = vaxis.Key{Keycode: 'l', Modifiers: vaxis.ModAlt}
			}
			a.handleKey(left)
			if got := a.sessions.TargetSessionKeys(target); !reflect.DeepEqual(got, []string{keys[1], keys[0], keys[2]}) {
				t.Fatalf("left shortcut did not reorder active tab: %v", got)
			}
			a.handleKey(left)
			if got := a.sessions.TargetSessionKeys(target); !reflect.DeepEqual(got, []string{keys[1], keys[0], keys[2]}) {
				t.Fatalf("boundary move wrapped tabs: %v", got)
			}
			a.handleKey(right)
			if got := a.sessions.TargetSessionKeys(target); !reflect.DeepEqual(got, keys) {
				t.Fatalf("right shortcut did not restore order: %v", got)
			}
			repeat := right
			repeat.EventType = vaxis.EventRepeat
			a.handleKey(repeat)
			movedRight := []string{keys[0], keys[2], keys[1]}
			if got := a.sessions.TargetSessionKeys(target); !reflect.DeepEqual(got, movedRight) {
				t.Fatalf("held move shortcut did not repeat: %v", got)
			}
			repeat.EventType = vaxis.EventRelease
			a.handleKey(repeat)
			if got := a.sessions.TargetSessionKeys(target); !reflect.DeepEqual(got, movedRight) {
				t.Fatalf("release changed tab order: %v", got)
			}
			a.handleKey(left)
			a.openNodeDetail(tuiDetailOverview)
			a.handleKey(left)
			if got := a.sessions.TargetSessionKeys(target); !reflect.DeepEqual(got, keys) {
				t.Fatalf("reorder shortcut acted outside Shell detail: %v", got)
			}
			a.openNodeDetail(tuiDetailShell)
			if a.state.activeSessionKey() != active {
				t.Fatal("reordering changed active terminal")
			}
			a.handleKey(vaxis.Key{Keycode: vaxis.KeyF06})
			before := len(term.events)
			a.handleKey(left)
			if a.state.focus != tuiFocusTerminal || len(term.events) != before+1 || !reflect.DeepEqual(term.events[before], normalizeTUITerminalEvent(left)) {
				t.Fatal("focused shell did not receive reorder chord unchanged")
			}
			if got := a.sessions.TargetSessionKeys(target); !reflect.DeepEqual(got, keys) {
				t.Fatalf("focused-shell payload reordered tabs: %v", got)
			}
		})
	}
}

func TestTUIKeyBindingsRemappedDeleteRetainsCapturedReviewAndPhysicalClaim(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	a.applyKeyBindings(KeyBindingConfig{"node.delete": {"ctrl+d"}})
	if a.keyBindingError != "" {
		t.Fatal(a.keyBindingError)
	}
	a.handleKey(vaxis.Key{Keycode: 'd', Text: "d"})
	if a.overlay != nil {
		t.Fatal("old delete key still opens a review")
	}
	node := a.state.selectedEntry().node
	a.handleKey(vaxis.Key{Keycode: 'd', Modifiers: vaxis.ModCtrl})
	dialog, ok := a.overlay.(*tuiNodeConfirmation)
	if !ok {
		t.Fatalf("remapped deletion did not open captured review: %T", a.overlay)
	}
	for _, event := range []vaxis.EventType{vaxis.EventRepeat, vaxis.EventRelease} {
		a.handleKey(vaxis.Key{Keycode: 'd', Text: "d", EventType: event})
	}
	if got := dialog.dialog.Fields[0].Input.String(); got != "" {
		t.Fatal("trigger leaked into name input", got)
	}
	var deleted string
	dialog.run = func(n Node) error { deleted = n.ID; return nil }
	a.fleet.selectID(a.state.nodes[1].ID)
	a.selectFleetNode()
	dialog.dialog.Fields[0].Input.SetContent(node.Slug)
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyEnter})
	if deleted != node.ID {
		t.Fatalf("review changed target: %q, want %q", deleted, node.ID)
	}
}

func TestTUIKeyBindingsNavigationUsesActionDirectionAndDisablesOldKeys(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	a.applyKeyBindings(KeyBindingConfig{"view.ports": {"ctrl+p"}, "navigation.previous_tab": {"alt+b"}, "metrics.window_less": {"alt+m"}})
	if a.keyBindingError != "" {
		t.Fatal(a.keyBindingError)
	}
	a.handleKey(vaxis.Key{Text: "3"})
	if a.view != tuiViewNodes {
		t.Fatal("old view key still active")
	}
	a.handleKey(vaxis.Key{Keycode: 'p', Modifiers: vaxis.ModCtrl})
	if a.view != tuiViewPorts {
		t.Fatal("configured view key inactive")
	}
	a.switchView(tuiViewNodes)
	a.openNodeDetail(tuiDetailLogs)
	a.handleKey(vaxis.Key{Keycode: 'b', Modifiers: vaxis.ModAlt})
	if a.detailTab != tuiDetailMetrics {
		t.Fatal("previous-tab binding used physical modifiers to determine direction", a.detailTab)
	}
	a.metrics.window = 2
	a.handleKey(vaxis.Key{Keycode: 'm', Modifiers: vaxis.ModAlt})
	if a.metrics.window != 1 {
		t.Fatal("remapped decrement did not decrease metrics window", a.metrics.window)
	}
}

func TestTUIKeyBindingsRecoveryRetainsEffectiveMapAndHeldKeys(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	a.applyKeyBindings(KeyBindingConfig{"node.delete": {"ctrl+d"}})
	a.claimShortcutKey(vaxis.Key{Keycode: 'd', Modifiers: vaxis.ModCtrl})
	a.applyKeyBindings(KeyBindingConfig{"node.delete": {"c"}})
	if a.keyBindingError == "" || a.keyHint("node.delete") != "Ctrl+d" {
		t.Fatal("invalid reload replaced effective bindings")
	}
	if _, ok := a.shortcutKeys['d']; !ok {
		t.Fatal("reload dropped held physical key claim")
	}
	first := a.keyBindingError
	messageCount := len(a.messages.Entries())
	a.applyKeyBindings(KeyBindingConfig{"node.delete": {"c"}})
	if a.keyBindingError != first || len(a.messages.Entries()) != messageCount {
		t.Fatal("diagnostic is unstable")
	}
	a.applyKeyBindings(nil)
	if a.keyBindingError != "" || a.keyHint("node.delete") != "d" {
		t.Fatal("valid recovery failed")
	}
	b, _ := newTestManager(t, 80, 24)
	b.applyKeyBindings(KeyBindingConfig{"node.delete": {"c"}})
	if b.keyHint("node.delete") != "d" || !strings.Contains(b.keyBindingError, "conflicts") {
		t.Fatal("startup fallback failed")
	}
}

func TestTUIKeyBindingsEffectiveHelpHintsAndMouse(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	a.applyKeyBindings(KeyBindingConfig{"node.delete": {"alt+d", "ctrl+d"}, "node.create": {"alt+n"}, "node.shell": {}, "dialog.cancel": {"alt+q"}})
	if a.keyBindingError != "" {
		t.Fatal(a.keyBindingError)
	}
	if footer := a.managerFooter(); !strings.Contains(footer, "Alt+n new") || !strings.Contains(footer, "unbound shell") {
		t.Fatal("footer does not describe effective keys", footer)
	}
	a.openKeyHelp()
	h := a.overlay.(*tuiKeyHelp)
	h.searching = true
	h.input.SetContent("node.delete")
	lines, count := h.searchLines()
	if count != 1 || !strings.Contains(lines[len(lines)-1].key, "Alt+d / Ctrl+d") {
		t.Fatal("help does not expose configured aliases/action ID", lines)
	}
	a.applyKeyBindings(KeyBindingConfig{"node.delete": {"alt+x"}, "dialog.cancel": {"alt+q"}})
	a.draw()
	lines, _ = h.searchLines()
	if !strings.Contains(lines[len(lines)-1].key, "Alt+x") {
		t.Fatal("open help retained an old map", lines)
	}
	a.overlay = nil
	a.openDeleteNodeDialog(a.state.selectedEntry().node)
	a.draw()
	d := a.overlay.(*tuiNodeConfirmation)
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyEsc})
	if a.overlay != d {
		t.Fatal("old overlay cancel key remained active")
	}
	found := false
	for _, control := range d.controls {
		if control.id == "cancel" {
			found = true
			a.handleMouse(vaxis.Mouse{Col: control.rect.col, Row: control.rect.row, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress})
		}
	}
	if !found || a.overlay != nil {
		t.Fatal("remapping broke pointer cancellation")
	}
	a.applyKeyBindings(KeyBindingConfig{"node.delete": {"%"}})
	if hint := a.keyHintsFormat("{node.delete} review %d", 3); hint != "% review 3" {
		t.Fatal("key spelling was interpreted as a format string", hint)
	}
}

func TestTUIKeyBindingsRenderedControlsUseEffectiveMap(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {150, 38}} {
		a, _ := newTestManager(t, size[0], size[1])
		a.applyKeyBindings(KeyBindingConfig{"system.refresh": {"alt+c"}, "system.open": {"alt+o"}, "dialog.cancel": {"alt+q"}, "dialog.accept": {"alt+a"}})
		if a.keyBindingError != "" {
			t.Fatal(a.keyBindingError)
		}
		a.switchView(tuiViewSystem)
		a.systemPanel = tuiSystemDaemon
		a.draw()
		screen := renderedScreenText(t, a.vx, size[0], size[1])
		for _, want := range []string{"Alt+c", "Alt+o details"} {
			if !strings.Contains(screen, want) {
				t.Fatalf("missing %s:\n%s", want, screen)
			}
		}
		selector := newTUISelector("Choose", []string{"Literal {dialog.cancel} path"}, nil, nil, false, nil)
		a.overlay = selector
		a.draw()
		screen = renderedScreenText(t, a.vx, size[0], size[1])
		for _, want := range []string{"Literal {dialog.cancel} path", "Alt+a confirm", "Alt+q cancel"} {
			if !strings.Contains(screen, want) {
				t.Fatalf("missing %s:\n%s", want, screen)
			}
		}
	}
}

func TestTUIKeyBindingsEmbeddedHintsFitControls(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {150, 38}} {
		a, _ := newTestManager(t, size[0], size[1])
		enableTestEmbeddedShell(a)
		a.applyKeyBindings(KeyBindingConfig{"session.guest": {"alt+g"}, "session.host": {"alt+h"}, "view.ports": {"alt+p"}})
		if a.keyBindingError != "" {
			t.Fatal(a.keyBindingError)
		}
		a.openNodeDetail(tuiDetailShell)
		a.draw()
		screen := renderedScreenText(t, a.vx, size[0], size[1])
		for _, want := range []string{"Alt+g guest", "Alt+h host", "Alt+p Ports"} {
			if !strings.Contains(screen, want) {
				t.Fatalf("missing %s:\n%s", want, screen)
			}
		}
		if a.embedded.guest.col+a.embedded.guest.width >= a.embedded.host.col || a.embedded.host.col+a.embedded.host.width > size[0] {
			t.Fatal("configured shell controls overlap or leave the viewport", a.embedded)
		}
	}
}

func TestTUIKeyBindingsHelpHintUsesFocusedContext(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	a.applyKeyBindings(KeyBindingConfig{"reader.up": {"alt+u"}, "reader.down": {"alt+d"}, "dialog.up": {"ctrl+alt+u"}})
	if a.keyBindingError != "" {
		t.Fatal(a.keyBindingError)
	}
	a.openKeyHelp()
	a.draw()
	h := a.overlay.(*tuiKeyHelp)
	if hint := h.FooterHint(); !strings.Contains(hint, "Alt+u/Alt+d") || strings.Contains(hint, "Ctrl+Alt+u") {
		t.Fatal(hint)
	}
	h.searching = true
	if hint := h.FooterHint(); !strings.Contains(hint, "Ctrl+Alt+u") || strings.Contains(hint, "Alt+u/Alt+d") {
		t.Fatal(hint)
	}
}
