package codelima

import (
	"context"
	"math"
	"strings"
	"testing"

	"go.rockorager.dev/vaxis"
)

func newTestManager(t *testing.T, width, height int) (*vaxisTUIApp, *sharedFakeTUISessionManager) {
	t.Helper()
	service, _ := newTestService(t)
	sessions := newTUISessionStore(context.Background(), service, func(vaxis.Event) {})
	t.Cleanup(sessions.Close)
	manager := newSharedFakeTUISessionManager(sessions)
	nodes := testTUINodes(t)
	for i := range nodes {
		nodes[i].DirectoryPath = "/work/project"
		nodes[i].ConfigurationSlug = "small"
	}
	state, err := newTUIState(nodes, manager)
	if err != nil {
		t.Fatal(err)
	}
	vx := newRenderTestVaxis(t, width, height)
	t.Cleanup(vx.Close)
	app := &vaxisTUIApp{ctx: context.Background(), state: state, sessions: sessions, vx: vx, messages: newTUIMessageLog(200)}
	app.refreshFleet()
	t.Cleanup(app.stopSpinners)
	return app, manager
}

func TestTUIManagerBrowsingDoesNotCreateShells(t *testing.T) {
	app, sessions := newTestManager(t, 80, 24)
	app.draw()
	for _, key := range []vaxis.Key{{Keycode: vaxis.KeyDown}, {Keycode: vaxis.KeyUp}, {Keycode: vaxis.KeyEnter}, {Keycode: vaxis.KeyEsc}} {
		app.handleKey(key)
		app.draw()
	}
	selected := app.state.selectedEntry().node.ID
	if err := app.applyReloadedNodes(app.state.nodes, ""); err != nil {
		t.Fatal(err)
	}
	if len(sessions.opened) != 0 || app.state.treePaneMode != tuiTreePaneModeInfo {
		t.Fatalf("browsing opened a shell: %v, pane=%s", sessions.opened, app.state.treePaneMode)
	}
	if app.state.selectedEntry().node.ID != selected {
		t.Fatal("refresh changed selected identity")
	}
}

func TestTUIManagerFilterOwnsNumbersAndRestoresPreviousFilter(t *testing.T) {
	app, _ := newTestManager(t, 80, 24)
	app.handleKey(vaxis.Key{Text: "/", Keycode: '/'})
	for _, r := range "port:3000" {
		app.handleKey(vaxis.Key{Text: string(r), Keycode: r})
	}
	if app.view != tuiViewNodes || app.fleet.filter != "port:3000" {
		t.Fatalf("filter typing navigated: view=%d filter=%q", app.view, app.fleet.filter)
	}
	app.handleKey(vaxis.Key{Keycode: vaxis.KeyEsc})
	if app.filterInput != nil || app.fleet.filter != "" {
		t.Fatal("Esc did not cancel live filter")
	}
	app.handleKey(vaxis.Key{Text: "2", Keycode: '2'})
	if app.view != tuiViewTemplates {
		t.Fatal("global view key did not navigate after leaving filter")
	}
}

func TestTUIManagerResponsiveTableAndPreview(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {120, 30}, {150, 38}} {
		t.Run(string(rune(size[0])), func(t *testing.T) {
			app, _ := newTestManager(t, size[0], size[1])
			app.draw()
			screen := renderedScreenText(t, app.vx, size[0], size[1])
			labels := []string{"1 Nodes", "NAME", "STATUS", "root-node", "child-node"}
			if size[0] >= 100 {
				labels = append(labels, "2 Templates", "3 Ports", "4 Activity", "5 System")
			}
			for _, want := range labels {
				if !strings.Contains(screen, want) {
					t.Errorf("missing %q:\n%s", want, screen)
				}
			}
			if strings.Contains(screen, "preview") != (size[0] >= 140) {
				t.Fatalf("preview at wrong width:\n%s", screen)
			}
			if app.treeContentRect.width < size[0]-5 {
				t.Fatal("fleet retained sidebar geometry")
			}
			if app.terminalBodyRect.height != 0 {
				t.Fatal("fleet exposed an embedded terminal hit target")
			}
		})
	}
}

func TestTUIManagerShellInterceptsOnlyF6(t *testing.T) {
	app, _ := newTestManager(t, 100, 30)
	enableTestEmbeddedShell(app)
	if err := app.openEmbeddedShell(false); err != nil {
		t.Fatal(err)
	}
	term := targetTestTerminal(t, app.sessions, app.state.selectedEntry().key())
	keys := []vaxis.Key{
		{Keycode: vaxis.KeyLeft, Modifiers: vaxis.ModAlt},
		{Keycode: vaxis.KeyRight, Modifiers: vaxis.ModAlt},
		{Keycode: 't', Modifiers: vaxis.ModAlt},
		{Keycode: 'w', Modifiers: vaxis.ModAlt},
		{Keycode: vaxis.KeyF07}, {Keycode: 'q', Text: "q"},
		{Keycode: '1', Text: "1"}, {Keycode: vaxis.KeyEsc},
	}
	for _, key := range keys {
		for _, eventType := range []vaxis.EventType{vaxis.EventPress, vaxis.EventRepeat, vaxis.EventRelease} {
			key.EventType = eventType
			if !isTUITerminalPayloadKey(key) {
				t.Fatalf("shell key classified as manager shortcut: %+v", key)
			}
			app.handleKey(key)
		}
	}
	if len(term.events) != 3*len(keys) {
		t.Fatalf("shell received %d events, want %d", len(term.events), 3*len(keys))
	}
	app.handleKey(vaxis.Key{Keycode: vaxis.KeyF06})
	if app.state.focus != tuiFocusTree || app.detailTab != tuiDetailShell || len(term.events) != 3*len(keys) {
		t.Fatal("F6 failed to return focus without sending shell payload")
	}
}

func TestTUIManagerMouseSkipsGroupHeaders(t *testing.T) {
	app, sessions := newTestManager(t, 80, 24)
	app.draw()
	previous := app.fleet.selectedID
	rect := app.treeContentRect
	app.handleMouse(vaxis.Mouse{Col: rect.col, Row: rect.row, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress})
	if app.fleet.selectedID != previous {
		t.Fatal("directory heading changed selection")
	}
	app.handleMouse(vaxis.Mouse{Col: rect.col, Row: rect.row + 1, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress})
	if app.fleet.selectedID != app.fleetMouseRows[1].NodeID || len(sessions.opened) > 0 {
		t.Fatal("mouse selected wrong node or created a shell")
	}
}

func TestTUIManagerIgnoresStaleViewResponses(t *testing.T) {
	app, _ := newTestManager(t, 80, 24)
	app.view, app.managerGeneration = tuiViewTemplates, 2
	app.managerData.loading = true
	app.applyManagerData(tuiManagerDataEvent{generation: 1, view: tuiViewSystem})
	if !app.managerData.loading {
		t.Fatal("late doctor reply changed templates state")
	}
	app.applyManagerData(tuiManagerDataEvent{generation: 2, view: tuiViewTemplates, data: tuiManagerData{configurations: []Configuration{{Slug: "small"}}}})
	if app.managerData.loading || len(app.managerData.configurations) != 1 {
		t.Fatal("current response was lost")
	}
}

func TestTUIManagerThemeAndGauges(t *testing.T) {
	ansi, rgb := newTUITheme(false), newTUITheme(true)
	if ansi.selected.Attribute&vaxis.AttrReverse == 0 || rgb.selected.Background != vaxis.RGBColor(0x1b, 0x2a, 0x36) {
		t.Fatal("selection does not honor ANSI / truecolor modes")
	}
	if rgb.usage(69) != rgb.mint || rgb.usage(70) != rgb.amber || rgb.usage(90) != rgb.coral {
		t.Fatal("gauge warning thresholds drifted")
	}
	for _, test := range []struct {
		value float64
		want  string
	}{
		{0, "░░░░░░░░░░"}, {25, "██▌░░░░░░░"}, {150, "██████████"}, {math.NaN(), "—         "},
	} {
		if got := tuiGauge(test.value, 10); got != test.want {
			t.Errorf("gauge %v = %q, want %q", test.value, got, test.want)
		}
	}
}

func TestTUIManagerPaletteFuzzySearchAndPasteCannotExecute(t *testing.T) {
	runs := 0
	p := newTUIPalette("Commands", []tuiPaletteCommand{{label: "Create node", run: func() error { runs++; return nil }}, {label: "Go to templates"}})
	for _, r := range "crnd" {
		_, _ = p.Update(vaxis.Key{Keycode: r, Text: string(r)})
	}
	if len(p.matches) != 1 || p.matches[0].label != "Create node" {
		t.Fatalf("fuzzy matches = %+v", p.matches)
	}
	_, _ = p.Update(vaxis.Key{Keycode: vaxis.KeyEnter, EventType: vaxis.EventPaste})
	if runs != 0 {
		t.Fatal("paste executed a palette action")
	}
	p.input.SetContent("crnd")
	p.filter()
	done, err := p.Update(vaxis.Key{Keycode: vaxis.KeyEnter})
	if !done || err != nil || runs != 1 {
		t.Fatalf("Enter action = %t, %v, %d runs", done, err, runs)
	}
}
