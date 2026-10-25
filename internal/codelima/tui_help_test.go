package codelima

import (
	"slices"
	"strings"
	"testing"

	"go.rockorager.dev/vaxis"
)

func TestTUIManagerHelpWrapsDescriptionsAtWordsAndPreservesGraphemes(t *testing.T) {
	if got := helpDescriptionRows("PgUp PgDn", 8); !slices.Equal(got, []string{"PgUp", "PgDn"}) {
		t.Fatalf("help split a key name: %q", got)
	}
	if got := helpDescriptionRows("restart; unmarked failed node retries failed step", 24); !slices.Equal(got, []string{"restart; unmarked failed", "node retries failed step"}) {
		t.Fatalf("help split a word: %q", got)
	}
	text := strings.Repeat("界e\u0301", 9)
	rows := helpDescriptionRows(text, 9)
	if strings.Join(rows, "") != text {
		t.Fatal("wrapping changed a grapheme")
	}
	for _, row := range rows {
		width := 0
		for _, ch := range vaxis.Characters(row) {
			width += ch.Width
		}
		if width > 9 {
			t.Fatalf("wrapped width = %d", width)
		}
	}
}

func TestTUIManagerHelpLeadsWithOpeningContext(t *testing.T) {
	for _, tt := range []struct {
		view   tuiView
		detail bool
		want   string
	}{{tuiViewNodes, false, "NODES"}, {tuiViewNodes, true, "NODE DETAIL"}, {tuiViewTemplates, false, "TEMPLATES & FORMS"}, {tuiViewPorts, false, "PORTS"}, {tuiViewActivity, false, "ACTIVITY"}, {tuiViewSystem, false, "SYSTEM"}} {
		a, _ := newTestManager(t, 150, 38)
		a.view, a.nodeDetail = tt.view, tt.detail
		a.openKeyHelp()
		h := a.overlay.(*tuiKeyHelp)
		if h.groups[0].title != tt.want {
			t.Fatalf("opening %v/%v leads with %q", tt.view, tt.detail, h.groups[0].title)
		}
		a.draw()
		if len(h.columns) != 4 {
			t.Fatalf("wide help has %d columns", len(h.columns))
		}
		if !strings.Contains(renderedScreenText(t, a.vx, 150, 38), "opened from "+tt.want) {
			t.Fatal("missing opening context")
		}
	}
}

func TestTUIManagerHelpOwnsKeysAndSearchesEveryContext(t *testing.T) {
	a, sessions := newTestManager(t, 80, 24)
	selected := a.fleet.selectedID
	a.handleKey(vaxis.Key{Keycode: '?', Text: "?"})
	h := a.overlay.(*tuiKeyHelp)
	for _, kind := range []vaxis.EventType{vaxis.EventRepeat, vaxis.EventRelease, vaxis.EventPaste} {
		a.handleKey(vaxis.Key{Keycode: '?', Text: "?", EventType: kind})
	}
	for _, key := range []rune{'2', 'n', 's', 'r', 'K', 't', 'x'} {
		a.handleKey(vaxis.Key{Keycode: key, Text: string(key)})
	}
	if a.overlay != h || a.view != tuiViewNodes || a.fleet.selectedID != selected || len(sessions.opened) != 0 {
		t.Fatal("help allowed a shortcut to change the manager")
	}
	a.handleKey(vaxis.Key{Keycode: '/', Text: "/"})
	for _, r := range "keep sessions" {
		a.handleKey(vaxis.Key{Keycode: r, Text: string(r), EventType: vaxis.EventPaste})
	}
	if _, err := a.handleEvent(vaxis.PasteEndEvent{}); err != nil {
		t.Fatal(err)
	}
	a.draw()
	screen := renderedScreenText(t, a.vx, 80, 24)
	if !strings.Contains(screen, "SYSTEM") || !strings.Contains(screen, "update daemon, keep sessions") {
		t.Fatalf("search did not find another context:\n%s", screen)
	}
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyEnter})
	if a.overlay != h {
		t.Fatal("search result executed or closed help")
	}
	h.input.SetContent("no-help-binding-matches-this")
	a.draw()
	if !strings.Contains(renderedScreenText(t, a.vx, 80, 24), "No matching keys") {
		t.Fatal("empty result state missing")
	}
	a.handleKey(vaxis.Key{Keycode: '?', Text: "?"})
	if a.overlay != h || !strings.HasSuffix(h.input.String(), "?") {
		t.Fatal("help search interpreted printable text as a close command")
	}
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyEsc})
	if a.overlay != nil || a.view != tuiViewNodes || a.fleet.selectedID != selected {
		t.Fatal("closing help did not restore its origin")
	}
}

func TestTUIManagerHelpCompactScrollAndMouse(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	a.openNodeDetail(tuiDetailOverview)
	a.openKeyHelp()
	h := a.overlay.(*tuiKeyHelp)
	a.draw()
	if len(h.columns) != 1 {
		t.Fatal("compact help must keep descriptions readable")
	}
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyEnd})
	a.draw()
	screen := renderedScreenText(t, a.vx, 80, 24)
	if !strings.Contains(screen, "SPEC") || !strings.Contains(screen, "copy YAML path") {
		t.Fatalf("compact end lost last bindings:\n%s", screen)
	}
	before := h.scroll[0]
	col := h.columns[0]
	a.handleMouse(vaxis.Mouse{Col: col.rect.col, Row: col.rect.row + 2, Button: vaxis.MouseWheelUp, EventType: vaxis.EventPress})
	if h.scroll[0] >= before {
		t.Fatal("wheel did not scroll focused help column")
	}
	for range len(h.groups) - 1 {
		a.handleKey(vaxis.Key{Keycode: vaxis.KeyTab})
		a.draw()
	}
	if h.focus != len(h.groups)-1 || !strings.Contains(renderedScreenText(t, a.vx, 80, 24), "SYSTEM") {
		t.Fatal("Tab did not expose offscreen groups")
	}
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyTab, Modifiers: vaxis.ModShift})
	if h.focus != len(h.groups)-2 {
		t.Fatal("Shift+Tab did not move back")
	}
}

func TestTUIManagerHelpResizeRetainsContextAndVisibleMouseTargets(t *testing.T) {
	a, _ := newTestManager(t, 150, 38)
	a.openKeyHelp()
	h := a.overlay.(*tuiKeyHelp)
	a.draw()
	column := h.columns[2]
	a.handleMouse(vaxis.Mouse{Col: column.rect.col, Row: column.rect.row, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress})
	if h.focus != 2 {
		t.Fatal("click did not focus Templates")
	}
	for _, size := range []vaxis.Resize{{Cols: 80, Rows: 24}, {Cols: 110, Rows: 30}, {Cols: 60, Rows: 14}, {Cols: 40, Rows: 10}, {Cols: 150, Rows: 38}} {
		if _, err := a.handleEvent(size); err != nil {
			t.Fatal(err)
		}
		a.draw()
		if h.focus != 2 || a.overlay != h {
			t.Fatal("resize replaced the help context")
		}
		if size.Cols < 60 {
			continue
		}
		for _, c := range h.columns {
			if c.rect.col < 0 || c.rect.col+c.rect.width > size.Cols || c.rect.row+c.rect.height > size.Rows {
				t.Fatalf("mouse target outside resized screen: %+v", c)
			}
		}
		if !strings.Contains(renderedScreenText(t, a.vx, size.Cols, size.Rows), "TEMPLATES & FORMS") {
			t.Fatal("focused group disappeared on resize")
		}
	}
	a.handleMouse(vaxis.Mouse{Col: h.findRect.col, Row: h.findRect.row, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress})
	if !h.searching {
		t.Fatal("search link did not activate")
	}
	a.draw()
	a.handleMouse(vaxis.Mouse{Col: h.closeRect.col, Row: h.closeRect.row, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress})
	if a.overlay != nil {
		t.Fatal("close link did not dismiss help")
	}
}
