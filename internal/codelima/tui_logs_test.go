package codelima

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.rockorager.dev/vaxis"
)

func testLogsApp(t *testing.T, width, height int) *vaxisTUIApp {
	t.Helper()
	app, _ := newTestManager(t, width, height)
	app.nodeDetail, app.detailTab = true, tuiDetailLogs
	app.logs = tuiLogsState{nodeID: app.state.selectedEntry().node.ID, follow: true, loaded: true}
	app.logs.snapshot = nodeLogSnapshot{NodeID: app.logs.nodeID, Source: nodeLogEvents, Path: "/logs/events.jsonl"}
	for i := range 40 {
		level := "info"
		if i%5 == 0 {
			level = "error"
		}
		app.logs.snapshot.Lines = append(app.logs.snapshot.Lines, nodeLogLine{Offset: int64(i * 100), Level: level, Kind: "forwarding.route.added", Text: fmt.Sprintf("port:3000 line-%02d %s", i, strings.Repeat("界", 90))})
	}
	app.logs.selectLast()
	return app
}

func TestTUILogsSearchOwnsTextAndPasteBeforeShortcuts(t *testing.T) {
	app := testLogsApp(t, 80, 24)
	app.handleKey(vaxis.Key{Keycode: '/', Text: "/"})
	for _, r := range "3000" {
		app.handleKey(vaxis.Key{Keycode: r, Text: string(r)})
	}
	app.handleKey(vaxis.Key{Keycode: 'q', Text: "q", EventType: vaxis.EventPaste})
	if _, err := app.handleEvent(vaxis.PasteEndEvent{}); err != nil {
		t.Fatal(err)
	}
	if app.view != tuiViewNodes || !app.nodeDetail || app.logs.query != "3000q" || app.logs.follow {
		t.Fatalf("search escaped input context: view=%d detail=%v query=%q follow=%v", app.view, app.nodeDetail, app.logs.query, app.logs.follow)
	}
	app.handleKey(vaxis.Key{Keycode: vaxis.KeyEsc})
	if !app.nodeDetail || app.logs.input != nil || app.logs.query != "" {
		t.Fatal("Esc must cancel search without leaving Logs")
	}
	app.handleKey(vaxis.Key{Keycode: '/', Text: "/"})
	for _, r := range "line-0" {
		app.handleKey(vaxis.Key{Keycode: r, Text: string(r)})
	}
	app.handleKey(vaxis.Key{Keycode: vaxis.KeyEnter})
	app.handleKey(vaxis.Key{Keycode: 'n', Text: "n"})
	if app.overlay != nil || app.logs.selected != 1 {
		t.Fatalf("next match selected %d or opened create", app.logs.selected)
	}
	app.handleKey(vaxis.Key{Keycode: 'N', Text: "N"})
	if app.logs.selected != 0 {
		t.Fatal("previous match failed")
	}
}

func TestTUILogsFollowFilterWrapAndMouseAtBothSizes(t *testing.T) {
	for _, size := range [][2]int{{80, 24}, {150, 38}} {
		app := testLogsApp(t, size[0], size[1])
		app.draw()
		screen := renderedScreenText(t, app.vx, size[0], size[1])
		for _, want := range []string{"events", "bootstrap", "lima", "serial", "following", "line-39"} {
			if !strings.Contains(screen, want) {
				t.Fatalf("missing %q: %s", want, screen)
			}
		}
		app.handleKey(vaxis.Key{Keycode: vaxis.KeyUp})
		if app.logs.follow || app.logs.selected != 38 {
			t.Fatal("scroll did not pause follow")
		}
		app.handleKey(vaxis.Key{Keycode: 'g', Text: "g"})
		if app.logs.selected != 0 {
			t.Fatal("top failed")
		}
		app.handleKey(vaxis.Key{Keycode: 'w', Text: "w"})
		app.draw()
		if !app.logs.wrap || len(app.logs.mouseRows) < 2 || app.logs.mouseRows[0] != app.logs.mouseRows[1] {
			t.Fatal("wide Unicode log line did not wrap")
		}
		rect := app.logs.bodyRect
		app.handleMouse(vaxis.Mouse{Col: rect.col, Row: rect.row + 1, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress})
		if app.logs.selected != app.logs.mouseRows[1] {
			t.Fatal("wrapped row selected wrong record")
		}
		for range 3 {
			app.handleKey(vaxis.Key{Keycode: 'L', Text: "L"})
		}
		for _, index := range app.logs.visibleLines() {
			if app.logs.snapshot.Lines[index].Level != "error" {
				t.Fatal("severity filter leaked info")
			}
		}
		app.handleKey(vaxis.Key{Keycode: 'G', Text: "G"})
		if !app.logs.follow || app.logs.selected != 35 {
			t.Fatal("bottom did not resume filtered follow")
		}
	}
}

func TestTUILogsRejectStaleResponsesAfterPauseSourceAndNavigation(t *testing.T) {
	app := testLogsApp(t, 80, 24)
	app.logs.serial, app.managerGeneration = 10, 3
	event := tuiLogsDataEvent{serial: 10, generation: 3, data: app.logs.snapshot}
	event.data.Lines = []nodeLogLine{{Text: "late"}}
	ctx, cancel := context.WithCancel(context.Background())
	app.logs.cancel = cancel
	app.handleKey(vaxis.Key{Keycode: 'f', Text: "f"})
	app.applyLogsData(event)
	if ctx.Err() == nil || len(app.logs.snapshot.Lines) != 40 {
		t.Fatal("pause accepted pending response")
	}
	app.handleKey(vaxis.Key{Keycode: ']', Text: "]"})
	app.applyLogsData(event)
	if app.logs.source != 1 || len(app.logs.snapshot.Lines) != 0 {
		t.Fatal("old source reply replaced new source")
	}
	app.switchView(tuiViewTemplates)
	event.serial = app.logs.serial
	app.applyLogsData(event)
	if len(app.logs.snapshot.Lines) != 0 {
		t.Fatal("hidden view accepted response")
	}
}

func TestTUILogsPagerArgumentsStayLiteral(t *testing.T) {
	args, err := nodeLogPagerArgs(`less -S`, "/tmp/a $(touch BAD).log")
	if err != nil || len(args) != 3 || args[2] != "/tmp/a $(touch BAD).log" {
		t.Fatalf("pager args=%q, %v", args, err)
	}
	if _, err := nodeLogPagerArgs("less | sh", "/logs/events"); err == nil {
		t.Fatal("shell pipeline accepted")
	}
}

func TestTUILogsScrollsWithinOversizeWrappedRecord(t *testing.T) {
	app := testLogsApp(t, 80, 24)
	app.logs.snapshot.Lines = []nodeLogLine{{Text: strings.Repeat("wide 界 ", 500) + "THE END", Level: "info"}}
	app.logs.selected, app.logs.follow, app.logs.wrap = 0, false, true
	app.draw()
	for range 120 {
		app.handleKey(vaxis.Key{Keycode: vaxis.KeyDown})
		app.draw()
	}
	if !strings.Contains(renderedScreenText(t, app.vx, 80, 24), "THE END") {
		t.Fatal("cannot reach end of one wrapped record")
	}
	app.handleKey(vaxis.Key{Keycode: 'g', Text: "g"})
	app.draw()
	if app.logs.offset != 0 {
		t.Fatal("top did not reset wrapped scroll")
	}
}

func TestTUILogsFollowsGrowthAndCopiesSelectedRecord(t *testing.T) {
	app := testLogsApp(t, 80, 24)
	app.logs.serial, app.managerGeneration = 2, 3
	data := app.logs.snapshot
	data.Lines = append(slices.Clone(data.Lines), nodeLogLine{Offset: 4000, Text: "latest output"})
	app.applyLogsData(tuiLogsDataEvent{serial: 2, generation: 3, data: data})
	if app.logs.selected != 40 {
		t.Fatal("follow did not select appended record")
	}
	var copied string
	app.clipboardPush = func(text string) error { copied = text; return nil }
	app.handleKey(vaxis.Key{Keycode: 'y', Text: "y"})
	if copied != "latest output" {
		t.Fatalf("copied %q", copied)
	}
}

func TestTUILogsHighlightsAcrossWrapWithoutSplittingGraphemes(t *testing.T) {
	style, highlight := vaxis.Style{}, vaxis.Style{Attribute: vaxis.AttrReverse}
	text := "xx👩🏽‍💻match"
	matches := regexp.MustCompile("👩🏽‍💻match").FindAllStringIndex(text, -1)
	rows := []tuiLogRow{{text: "xx👩🏽‍💻ma", start: 0}, {text: "tch", start: len("xx👩🏽‍💻ma")}}
	for _, row := range rows {
		segments := logHighlightSegments(row, matches, style, highlight)
		if segments[len(segments)-1].Style != highlight {
			t.Fatal("wrapped match lost highlight")
		}
	}
	segments := logHighlightSegments(tuiLogRow{text: text}, regexp.MustCompile("👩").FindAllStringIndex(text, -1), style, highlight)
	if len(segments) != 3 || segments[1].Text != "👩🏽‍💻" {
		t.Fatalf("split emoji: %+v", segments)
	}
}
