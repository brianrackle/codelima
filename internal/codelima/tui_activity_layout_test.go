package codelima

import (
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.rockorager.dev/vaxis"
)

func activityTextPosition(t *testing.T, a *vaxisTUIApp, text string) (int, int) {
	t.Helper()
	width, height := a.vx.Window().Size()
	for row := range height {
		for col := range width {
			var tail strings.Builder
			for x := col; x < width && tail.Len() < len(text); x++ {
				tail.WriteString(renderedCellGrapheme(t, a.vx, x, row))
			}
			if strings.HasPrefix(tail.String(), text) {
				return col, row
			}
		}
	}
	t.Fatalf("missing %q:\n%s", text, renderedScreenText(t, a.vx, width, height))
	return 0, 0
}

func TestTUIActivityHistoryColumnsStylesAndSelection(t *testing.T) {
	for _, rgb := range []bool{false, true} {
		t.Run(fmt.Sprint(rgb), func(t *testing.T) {
			a := testActivityApp(t, 150, 38)
			now := time.Now()
			now = time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())
			a.activity.page.History = []ActivityRecord{
				{ID: "info", NodeSlug: "短", Text: "informational message", Time: now},
				{ID: "request:requested", OperationID: "request", NodeSlug: "a-very-long-node-slug", Text: "start requested", Time: now.Add(-time.Second)},
				{ID: "error", NodeID: "node", NodeSlug: "failure", Text: "failure message", Level: slog.LevelError, Time: now.Add(-2 * time.Second)},
				{ID: "warning", Source: "host", Text: "warning message", Action: activityOpenHost, Level: slog.LevelWarn, Time: now.Add(-3 * time.Second)},
				{ID: "success", NodeSlug: "ready", Text: "successful message", Success: true, Time: now.Add(-4 * time.Second)},
			}
			a.activity.selected = "error"
			theme := newTUITheme(rgb)
			tuiFill(a.vx.Window(), theme.text)
			a.drawActivity(a.vx.Window(), theme)
			messageCol := -1
			for _, want := range []struct {
				text, glyph string
				color       vaxis.Color
			}{
				{"informational message", "i", theme.blue.Foreground},
				{"start requested", "◐", theme.amber.Foreground},
				{"failure message", "✕", theme.coral.Foreground},
				{"warning message", "⚠", theme.amber.Foreground},
				{"successful message", "✓", theme.mint.Foreground},
			} {
				col, row := activityTextPosition(t, a, want.text)
				if messageCol >= 0 && col != messageCol {
					t.Errorf("message columns differ: %q at %d, wanted %d", want.text, col, messageCol)
				}
				messageCol = col
				found := false
				for x := 2; x < col; x++ {
					if renderedCellGrapheme(t, a.vx, x, row) == want.glyph && renderedCellStyle(t, a.vx, x, row).Foreground == want.color {
						found = true
					}
				}
				if !found {
					t.Errorf("%q lacks its distinct %s glyph/color", want.text, want.glyph)
				}
				if want.text == "failure message" {
					for x := 2; x < 148; x++ {
						style := renderedCellStyle(t, a.vx, x, row)
						if rgb && style.Background != theme.selected.Background || !rgb && style.Attribute&vaxis.AttrReverse == 0 {
							t.Fatalf("selected row loses highlight at cell %d", x)
						}
					}
				}
				if want.text == "successful message" && renderedCellStyle(t, a.vx, col, row).Foreground != theme.text.Foreground {
					t.Error("success color should belong to the glyph, preserving readable message text")
				}
			}
			activityTextPosition(t, a, "⏎ details")
			activityTextPosition(t, a, "5 system")
			_, row := activityTextPosition(t, a, "History today")
			if renderedCellGrapheme(t, a.vx, 1, row) != "╭" || renderedCellStyle(t, a.vx, 1, row).Foreground != theme.mint.Foreground {
				t.Fatal("history title must share the focused panel border")
			}
		})
	}
}

func TestTUIActivityRowActionsAndHeaderBadge(t *testing.T) {
	a := testActivityApp(t, 150, 38)
	a.activity.page.History = []ActivityRecord{{ID: "host", Source: "host", Text: "capacity warning", Level: slog.LevelWarn, Action: activityOpenHost, Time: time.Now()}}
	a.draw()
	col, row := activityTextPosition(t, a, "⠋1")
	if renderedCellStyle(t, a.vx, col, row).Foreground != newTUITheme(false).amber.Foreground {
		t.Fatal("running count must remain amber in the active view")
	}
	col, row = activityTextPosition(t, a, "5 system")
	a.handleMouse(vaxis.Mouse{Col: col, Row: row, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress})
	if a.view != tuiViewSystem || a.systemPanel != tuiSystemHost {
		t.Fatal("row action did not open the typed Host target")
	}
	a.activity.page.Running = nil
	a.draw()
	if strings.Contains(renderedScreenText(t, a.vx, 150, 38), "⠋1") {
		t.Fatal("completed operations left a stale header count")
	}
	region := a.viewRegions[tuiViewActivity]
	a.handleMouse(vaxis.Mouse{Col: region.col, Row: region.row, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress})
	if a.view != tuiViewActivity {
		t.Fatal("header click did not return to Activity")
	}
}

func TestTUIActivityControlsUseRenderedTargets(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {150, 38}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			a := testActivityApp(t, size[0], size[1])
			a.activity.page.History[0].NodeID = a.activity.page.Running[0].NodeID
			click := func(text string) {
				t.Helper()
				a.draw()
				x, y := activityTextPosition(t, a, text)
				a.handleMouse(vaxis.Mouse{Col: x, Row: y, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress})
			}
			click("errors")
			_, history := a.activityRows()
			if a.activity.level != 2 || len(history) != 1 || history[0].id != "failed" {
				t.Fatal("severity chip did not select errors", a.activity.level, history)
			}
			click("f level")
			if a.activity.level != 0 {
				t.Fatal("footer severity control did not cycle")
			}
			click("/ filter")
			if a.activity.input == nil {
				t.Fatal("footer did not enter search")
			}
			a.handleKey(vaxis.Key{Text: "exit", EventType: vaxis.EventPaste})
			_, _ = a.handleEvent(vaxis.PasteEndEvent{})
			click("Esc cancel")
			if a.activity.input != nil || a.activity.query != "" {
				t.Fatal("filter footer did not restore previous query")
			}
			var copied string
			a.clipboardPush = func(text string) error { copied = text; return nil }
			a.activity.selected = "failed"
			click("y copy")
			if !strings.Contains(copied, "bootstrap exit 23") {
				t.Fatal("footer copy lost selected evidence", copied)
			}
			click("⏎ open")
			if a.view != tuiViewNodes || !a.nodeDetail {
				t.Fatal("footer open did not use the existing navigation action")
			}
		})
	}
}

func TestTUIActivityHistoryShowsDateAndVisibleRange(t *testing.T) {
	a := testActivityApp(t, 80, 24)
	a.activity.page.History = nil
	yesterday := time.Now().AddDate(0, 0, -1)
	yesterday = time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 12, 0, 0, 0, yesterday.Location())
	for i := range 40 {
		a.activity.page.History = append(a.activity.page.History, ActivityRecord{ID: fmt.Sprint(i), Source: "daemon", Text: fmt.Sprintf("record %d", i), Time: yesterday.Add(-time.Duration(i) * time.Second)})
	}
	a.activity.selected = "39"
	a.draw()
	screen := renderedScreenText(t, a.vx, 80, 24)
	for _, text := range []string{yesterday.Local().Format("Jan 02"), "of 40", "page 1", "g top", "kept up to 7 days", "record 39"} {
		if !strings.Contains(screen, text) {
			t.Errorf("missing %q:\n%s", text, screen)
		}
	}
	if a.activity.historyScroll == 0 {
		t.Fatal("history did not scroll to the selected last row")
	}
}
