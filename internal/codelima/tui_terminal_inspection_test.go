package codelima

import (
	"context"
	"testing"
	"time"

	"go.rockorager.dev/vaxis"
)

type inspectionTestTerminal struct {
	*fakeTUITerminal
	requests chan TerminalInteractionRequest
}

func (t *inspectionTestTerminal) Interact(request TerminalInteractionRequest) (TerminalInteractionResult, error) {
	t.requests <- request
	result := TerminalInteractionResult{Search: TerminalSearchStatus{Matches: 2, CaughtUp: true}}
	if request.Action == "release" {
		result.Text = "native selected text"
	}
	return result, nil
}

func newInspectionTestApp(t *testing.T) (*vaxisTUIApp, *inspectionTestTerminal) {
	t.Helper()
	service, _ := newTestService(t)
	sessions := newTUISessionStore(context.Background(), service, func(vaxis.Event) {})
	state, err := newTUIState(testTUINodes(t), newSharedFakeTUISessionManager(sessions))
	if err != nil {
		t.Fatal(err)
	}
	state.treePaneMode = tuiTreePaneModeTerminal
	terminal := &inspectionTestTerminal{fakeTUITerminal: newFakeTUITerminal(), requests: make(chan TerminalInteractionRequest, 32)}
	app := &vaxisTUIApp{ctx: context.Background(), service: service, sessions: sessions, state: state, terminalBodyRect: tuiRect{col: 10, row: 5, width: 40, height: 10}}
	putTestSession(sessions, nodeTargetKey("node-root"), &tuiSession{node: Node{ID: "node-root", Slug: "root-node", Status: NodeStatusRunning}}, terminal)
	t.Cleanup(func() { app.closeTerminalSearch(); app.inspector.close(); sessions.Close() })
	return app, terminal
}

func nextInspectionResult(t *testing.T, app *vaxisTUIApp) tuiInspectionResult {
	t.Helper()
	select {
	case result := <-app.inspectionResults():
		return result
	case <-time.After(time.Second):
		t.Fatal("missing inspection completion")
		return tuiInspectionResult{}
	}
}

func TestTUIDragUsesNativeSelectionAndCopiesOneOwnedResult(t *testing.T) {
	app, terminal := newInspectionTestApp(t)
	var copied []string
	app.clipboardPush = func(text string) error { copied = append(copied, text); return nil }
	for _, event := range []vaxis.Mouse{
		{Col: 11, Row: 6, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress},
		{Col: 15, Row: 6, Button: vaxis.MouseLeftButton, EventType: vaxis.EventMotion},
		{Col: 15, Row: 6, Button: vaxis.MouseLeftButton, EventType: vaxis.EventRelease},
	} {
		app.handleMouse(event)
	}
	for range 3 {
		app.handleInspectionResult(nextInspectionResult(t, app))
	}
	for _, action := range []string{"press", "drag", "release"} {
		request := <-terminal.requests
		if request.Action != action || request.Row != 1 {
			t.Fatalf("native selection request = %+v; want %s in viewport row1", request, action)
		}
	}
	if len(copied) != 1 || copied[0] != "native selected text" {
		t.Fatalf("clipboard = %q", copied)
	}
}

func TestTUISearchUsesNativeQueriesAndDiscardsOldQueryCompletions(t *testing.T) {
	app, terminal := newInspectionTestApp(t)
	if err := app.openTerminalSearch(); err != nil {
		t.Fatal(err)
	}
	app.handleSearchKey(vaxis.Key{Text: "a", Keycode: 'a'})
	first := nextInspectionResult(t, app)
	app.handleSearchKey(vaxis.Key{Text: "b", Keycode: 'b'})
	second := nextInspectionResult(t, app)
	app.handleInspectionResult(first)
	if !app.search.pending {
		t.Fatal("old query completion cleared new query's pending state")
	}
	app.handleInspectionResult(second)
	if app.search.pending || app.search.status.Matches != 2 {
		t.Fatalf("search state = %+v", app.search)
	}
	for _, query := range []string{"a", "ab"} {
		request := <-terminal.requests
		if request.Action != "search" || request.Query != query {
			t.Fatalf("query request = %+v", request)
		}
	}
	app.handleSearchKey(vaxis.Key{Keycode: vaxis.KeyEnter})
	app.handleInspectionResult(nextInspectionResult(t, app))
	if request := <-terminal.requests; request.Action != "next" {
		t.Fatalf("next request=%+v", request)
	}
	app.handleSearchKey(vaxis.Key{Keycode: vaxis.KeyEsc})
	if app.search != nil {
		t.Fatal("escape did not close search")
	}
	app.handleInspectionResult(nextInspectionResult(t, app))
	if request := <-terminal.requests; request.Action != "search" || request.Query != "" {
		t.Fatalf("cancel request=%+v", request)
	}
}

func TestTUIInspectionResultsCannotCopyFromAnOldTarget(t *testing.T) {
	app, _ := newInspectionTestApp(t)
	app.clipboardPush = func(string) error { t.Fatal("stale result changed host clipboard"); return nil }
	app.handleInspectionResult(tuiInspectionResult{request: tuiInspectionRequest{target: "closed-tab", copy: true}, result: TerminalInteractionResult{Text: "stale"}})
}
