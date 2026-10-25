package codelima

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"go.rockorager.dev/vaxis"
)

func keyBindingTestActions() []keyActionDefinition {
	return []keyActionDefinition{
		{ID: "node.delete", Description: "Review deletion", Defaults: []string{"d"}, Contexts: []string{"nodes", "detail"}},
		{ID: "node.clone", Description: "Review clone", Defaults: []string{"c"}, Contexts: []string{"nodes", "detail"}},
		{ID: "navigation.up", Defaults: []string{"up", "k"}, Contexts: []string{"nodes", "detail"}},
		{ID: "session.guest", Defaults: []string{"t"}, Contexts: []string{"detail"}},
		{ID: "session.host", Defaults: []string{"T"}, Contexts: []string{"detail"}},
		{ID: "dialog.cancel", Defaults: []string{"esc"}, Contexts: []string{"dialog"}, Required: true, TextInput: true},
		{ID: "dialog.submit", Defaults: []string{"ctrl+s"}, Contexts: []string{"dialog"}, TextInput: true},
		{ID: "log.delete_filter", Defaults: []string{"d"}, Contexts: []string{"logs"}},
	}
}

func TestKeyBindingsResolveOverridesAliasesAndContexts(t *testing.T) {
	m, err := compileKeyBindings(KeyBindingConfig{"node.delete": {"CTRL+d", "alt+Backspace"}, "node.clone": {}}, keyBindingTestActions())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		context string
		key     vaxis.Key
		action  string
	}{
		{"nodes", vaxis.Key{Keycode: 'd', Text: "d"}, ""},
		{"nodes", vaxis.Key{Keycode: 'd', Modifiers: vaxis.ModCtrl}, "node.delete"},
		{"nodes", vaxis.Key{Keycode: vaxis.KeyBackspace, Modifiers: vaxis.ModAlt}, "node.delete"},
		{"nodes", vaxis.Key{Text: "c"}, ""},
		{"logs", vaxis.Key{Text: "d"}, "log.delete_filter"},
		{"logs", vaxis.Key{Keycode: 'd', Modifiers: vaxis.ModCtrl}, ""},
		{"nodes", vaxis.Key{Keycode: vaxis.KeyUp, EventType: vaxis.EventRepeat}, "navigation.up"},
		{"nodes", vaxis.Key{Keycode: 'd', Modifiers: vaxis.ModCtrl, EventType: vaxis.EventPaste}, ""},
		{"dialog", vaxis.Key{Keycode: '[', Modifiers: vaxis.ModCtrl}, "dialog.cancel"},
	} {
		if got := m.Action(tc.context, tc.key); got != tc.action {
			t.Errorf("Action(%q, %+v) = %q, want %q", tc.context, tc.key, got, tc.action)
		}
	}
	if got := m.Hint("node.delete"); got != "Ctrl+d / Alt+Backspace" {
		t.Fatalf("effective hint = %q", got)
	}
	if got := m.Hint("node.clone"); got != "unbound" {
		t.Fatalf("disabled hint = %q", got)
	}
}

func TestKeyBindingsPreservePrintableCaseAndPhysicalEvents(t *testing.T) {
	m, err := compileKeyBindings(nil, keyBindingTestActions())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key    vaxis.Key
		action string
	}{
		{vaxis.Key{Text: "t"}, "session.guest"},
		{vaxis.Key{Text: "T"}, "session.host"},
		{vaxis.Key{Keycode: 't', ShiftedCode: 'T', Text: "T", Modifiers: vaxis.ModShift}, "session.host"},
		{vaxis.Key{Keycode: 't', Text: "T", Modifiers: vaxis.ModCapsLock}, "session.host"},
		{vaxis.Key{Keycode: 't', Modifiers: vaxis.ModShift}, "session.host"},
		{vaxis.Key{Keycode: 'T'}, "session.host"},
		{vaxis.Key{Text: "T", EventType: vaxis.EventPaste}, ""},
	} {
		before := tc.key
		if got := m.Action("detail", tc.key); got != tc.action {
			t.Errorf("Action(%+v) = %q, want %q", tc.key, got, tc.action)
		}
		if tc.key != before {
			t.Fatal("resolver rewrote the physical input event")
		}
	}
}

func TestKeyBindingsRejectInvalidAndOverlappingOverrides(t *testing.T) {
	for _, tc := range []struct {
		name      string
		overrides KeyBindingConfig
		contains  []string
	}{
		{"unknown action", KeyBindingConfig{"node.remove": {"d"}}, []string{"keys.node.remove", "unknown"}},
		{"unknown key", KeyBindingConfig{"node.delete": {"delete-everything"}}, []string{"keys.node.delete", "delete-everything"}},
		{"unknown modifier", KeyBindingConfig{"node.delete": {"command+d"}}, []string{"keys.node.delete", "command"}},
		{"empty key", KeyBindingConfig{"node.delete": {"ctrl+"}}, []string{"keys.node.delete", "unknown key"}},
		{"empty modifier", KeyBindingConfig{"node.delete": {"+d"}}, []string{"keys.node.delete", "modifier"}},
		{"modified legacy alias", KeyBindingConfig{"node.delete": {"alt+ctrl+h", "alt+backspace"}}, []string{"keys.node.delete", "duplicate"}},
		{"shifted text", KeyBindingConfig{"dialog.cancel": {"shift+1"}}, []string{"keys.dialog.cancel", "text"}},
		{"duplicate", KeyBindingConfig{"node.delete": {"up", "Up"}}, []string{"keys.node.delete", "duplicate"}},
		{"shift alias", KeyBindingConfig{"node.delete": {"T", "shift+t"}}, []string{"keys.node.delete", "duplicate"}},
		{"legacy alias", KeyBindingConfig{"dialog.cancel": {"esc", "ctrl+["}}, []string{"keys.dialog.cancel", "duplicate"}},
		{"overlap", KeyBindingConfig{"node.delete": {"c"}}, []string{"node.delete", "node.clone", "detail"}},
		{"required", KeyBindingConfig{"dialog.cancel": {}}, []string{"keys.dialog.cancel", "required"}},
		{"text theft", KeyBindingConfig{"dialog.cancel": {"q"}}, []string{"keys.dialog.cancel", "text"}},
		{"control character", KeyBindingConfig{"node.delete": {"\x1b"}}, []string{"keys.node.delete"}},
		{"too many", KeyBindingConfig{"node.delete": {"a", "b", "c", "d", "e", "f", "g", "h", "i"}}, []string{"keys.node.delete", "8"}},
		{"too long", KeyBindingConfig{"node.delete": {strings.Repeat("a", 65)}}, []string{"keys.node.delete", "64"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := compileKeyBindings(tc.overrides, keyBindingTestActions())
			var appErr *AppError
			if !errors.As(err, &appErr) || appErr.Category != CategoryInvalidArgument {
				t.Fatalf("error = %v, want InvalidArgument", err)
			}
			for _, want := range tc.contains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%q missing %q", err, want)
				}
			}
		})
	}
}

func TestKeyBindingsEmptyTextAndShiftedDigit(t *testing.T) {
	definitions := []keyActionDefinition{{ID: "replacement", Defaults: []string{"�"}, Contexts: []string{"view"}}, {ID: "number", Defaults: []string{"1"}, Contexts: []string{"view"}}, {ID: "shifted", Defaults: []string{"shift+1"}, Contexts: []string{"view"}}}
	m, err := compileKeyBindings(nil, definitions)
	if err != nil {
		t.Fatal(err)
	}
	if action := m.Action("view", vaxis.Key{Keycode: vaxis.KeyUp}); action != "" {
		t.Fatal("empty Text matched a replacement character", action)
	}
	if action := m.Action("view", vaxis.Key{Keycode: '1', Modifiers: vaxis.ModShift}); action != "shifted" {
		t.Fatal("shifted digit collapsed into unmodified digit", action)
	}
}

func TestKeyBindingsSnapshotsOwnTheirData(t *testing.T) {
	definitions := keyBindingTestActions()
	overrides := KeyBindingConfig{"node.delete": {"ctrl+d"}}
	m, err := compileKeyBindings(overrides, definitions)
	if err != nil {
		t.Fatal(err)
	}
	overrides["node.delete"][0] = "c"
	definitions[0].Defaults[0] = "q"
	definitions[0].Contexts[0] = "dialog"
	info := m.Describe()
	for i := range info {
		if info[i].Action == "node.delete" {
			info[i].Effective[0] = "c"
			info[i].Defaults[0] = "q"
			info[i].Contexts[0] = "dialog"
		}
	}
	if got := m.Action("nodes", vaxis.Key{Keycode: 'd', Modifiers: vaxis.ModCtrl}); got != "node.delete" {
		t.Fatal("snapshot followed caller mutation", got)
	}
	for _, entry := range m.Describe() {
		if entry.Action == "node.delete" && (!slices.Equal(entry.Effective, []string{"ctrl+d"}) || !slices.Equal(entry.Defaults, []string{"d"}) || !slices.Contains(entry.Contexts, "nodes")) {
			t.Fatalf("description exposes shared data: %+v", entry)
		}
	}
}

func TestKeyBindingsStrictRoundTripAndLogicalLayoutPreference(t *testing.T) {
	definitions := []keyActionDefinition{
		{ID: "action.logical", Defaults: []string{"é", "shift+tab", "ctrl+plus", "F63"}, Contexts: []string{"view"}},
		{ID: "action.base", Defaults: []string{"a"}, Contexts: []string{"view"}},
	}
	m, err := compileKeyBindings(nil, definitions)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Action("view", vaxis.Key{Text: "é", Keycode: 'é', BaseLayoutCode: 'a'}); got != "action.logical" {
		t.Fatal("physical fallback beat logical text", got)
	}
	if got := m.Action("view", vaxis.Key{Keycode: 'ж', BaseLayoutCode: 'a'}); got != "action.base" {
		t.Fatal("base-layout fallback lost", got)
	}
	if got := m.Action("view", vaxis.Key{Keycode: '=', ShiftedCode: '+', Modifiers: vaxis.ModShift | vaxis.ModCtrl}); got != "action.logical" {
		t.Fatal("shifted punctuation did not match", got)
	}
	for _, entry := range m.Describe() {
		copyMap, err := compileKeyBindings(KeyBindingConfig{entry.Action: entry.Effective}, definitions)
		if err != nil || copyMap.Hint(entry.Action) != m.Hint(entry.Action) {
			t.Fatal("normalized bindings did not round trip", entry, err)
		}
	}
}

func TestKeyBindingsUnshiftedAlternateCodeDoesNotInvokeShiftedAction(t *testing.T) {
	m, err := compileKeyBindings(nil, []keyActionDefinition{
		{ID: "plain", Defaults: []string{"alt+t"}, Contexts: []string{"view"}},
		{ID: "shifted", Defaults: []string{"alt+T"}, Contexts: []string{"view"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Action("view", vaxis.Key{Keycode: 't', ShiftedCode: 'T', Modifiers: vaxis.ModAlt}); got != "plain" {
		t.Fatalf("unshifted key invoked %q", got)
	}
	if got := m.Action("view", vaxis.Key{Keycode: 't', ShiftedCode: 'T', Modifiers: vaxis.ModAlt | vaxis.ModShift}); got != "shifted" {
		t.Fatalf("shifted key invoked %q", got)
	}
}
