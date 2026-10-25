package codelima

import (
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.rockorager.dev/vaxis"
)

func testActivityApp(t *testing.T, width, height int) *vaxisTUIApp {
	t.Helper()
	a, _ := newTestManager(t, width, height)
	a.view = tuiViewActivity
	now := time.Now()
	a.activity.page = ActivityPage{
		Running: []NodeOperation{{ID: "active", NodeID: a.state.selectedEntry().node.ID, NodeSlug: "other-window", Request: NodeOperationRequest{Kind: "start"}, State: "running", CreatedAt: now.Add(-time.Minute)}},
		History: []ActivityRecord{
			{ID: "failed", OperationID: "failed", NodeSlug: "failed-node", Text: "retry · failed · bootstrap exit 23", Level: slog.LevelError, Time: now.Add(-2 * time.Minute)},
			{ID: "canceled", OperationID: "canceled", NodeSlug: "canceled-node", Text: "start · canceled", Level: slog.LevelWarn, Time: now.Add(-3 * time.Minute)},
			{ID: "success", OperationID: "success", NodeSlug: "ready-node", Text: "stop · succeeded", Success: true, Time: now.Add(-4 * time.Minute)},
		},
	}
	a.activity.loaded = true
	return a
}

func TestTUIActivitySharedRecordsRenderAndSelectAcrossRefresh(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {150, 38}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			a := testActivityApp(t, size[0], size[1])
			a.activity.selected = "failed"
			a.draw()
			screen := renderedScreenText(t, a.vx, size[0], size[1])
			for _, want := range []string{"Running", "other-window", "History", "failed-node", "bootstrap exit 23", "K cancel", "⠋1", "g top"} {
				if !strings.Contains(screen, want) {
					t.Fatalf("missing %q:\n%s", want, screen)
				}
			}
			page := a.activity.page
			page.History = append([]ActivityRecord{{ID: "new", Success: true, Time: time.Now()}}, page.History...)
			a.activity.serial = 2
			a.applyActivityData(tuiActivityDataEvent{serial: 1})
			if len(a.activity.page.History) != 3 {
				t.Fatal("stale completion replaced shared state")
			}
			a.applyActivityData(tuiActivityDataEvent{serial: 2, page: page})
			if a.activity.selected != "failed" {
				t.Fatal("new activity moved durable selection")
			}
			a.activity.selected = "active"
			page.Running = nil
			page.History = append([]ActivityRecord{{ID: "active", Success: true, Time: time.Now()}}, page.History...)
			a.applyActivityData(tuiActivityDataEvent{serial: 2, page: page})
			if a.activity.selected != "active" || len(a.activity.page.Running) != 0 {
				t.Fatal("completion lost selection or retained busy badge")
			}
		})
	}
}

func TestTUIActivityFilterPasteSeverityAndCopy(t *testing.T) {
	a := testActivityApp(t, 80, 24)
	a.handleKey(vaxis.Key{Text: "/", Keycode: '/'})
	for _, r := range "23" {
		a.handleKey(vaxis.Key{Text: string(r), Keycode: r})
	}
	a.handleKey(vaxis.Key{Text: "q", Keycode: 'q', EventType: vaxis.EventPaste})
	_, _ = a.handleEvent(vaxis.PasteEndEvent{})
	if a.view != tuiViewActivity || a.activity.query != "23q" {
		t.Fatal("search let numbers or pasted shortcuts escape", a.activity.query)
	}
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyEsc})
	if a.activity.input != nil || a.activity.query != "" || a.view != tuiViewActivity {
		t.Fatal("Esc did not restore Activity search")
	}
	a.messages.Append(slog.LevelWarn, "capacity warning")
	a.handleKey(vaxis.Key{Text: "f", Keycode: 'f'})
	_, history := a.activityRows()
	if len(history) != 3 {
		t.Fatal("warn+ must retain error, canceled and local warning", len(history))
	}
	a.handleKey(vaxis.Key{Text: "f", Keycode: 'f'})
	_, history = a.activityRows()
	if len(history) != 1 || history[0].id != "failed" {
		t.Fatal("errors filter leaked lower severity", history)
	}
	a.activity.selected = "failed"
	var copied string
	a.clipboardPush = func(s string) error { copied = s; return nil }
	a.handleKey(vaxis.Key{Text: "y", Keycode: 'y'})
	if !strings.Contains(copied, "failed-node") || !strings.Contains(copied, "bootstrap exit 23") {
		t.Fatal("copy lost selected operation evidence", copied)
	}
}

func TestTUIActivityOpenSelectsNodeWithoutOpeningShell(t *testing.T) {
	a := testActivityApp(t, 80, 24)
	nodeID := a.activity.page.Running[0].NodeID
	a.activity.selected = "active"
	a.fleet.setFilter("does-not-exist")
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyEnter})
	if a.view != tuiViewNodes || !a.nodeDetail || a.state.selectedEntry().node.ID != nodeID || a.state.focus != tuiFocusTree {
		t.Fatal("Activity did not open the operation's node without shell focus")
	}
}

func TestTUIActivityMouseSelectsSharedOperation(t *testing.T) {
	a := testActivityApp(t, 80, 24)
	a.draw()
	for _, row := range a.activity.mouseRows {
		if row.id == "failed" {
			a.handleMouse(vaxis.Mouse{Col: row.rect.col, Row: row.rect.row, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress})
			if a.activity.selected != "failed" || a.view != tuiViewActivity {
				t.Fatal("Activity click used a stale fleet target")
			}
			return
		}
	}
	t.Fatal("failed operation has no visible mouse target")
}
