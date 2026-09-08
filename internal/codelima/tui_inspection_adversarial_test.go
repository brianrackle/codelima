package codelima

import (
	"errors"
	"testing"
	"time"
)

func TestInspectionCompletionCannotAffectReplacementWithSameSessionKey(t *testing.T) {
	app, _ := newInspectionTestApp(t)
	oldReceiver := &inspectionTestTerminal{fakeTUITerminal: newFakeTUITerminal()}
	key := app.state.activeSessionKey()
	app.clipboardPush = func(string) error { t.Error("old terminal receiver changed the host clipboard"); return nil }
	if err := app.openTerminalSearch(); err != nil {
		t.Fatal(err)
	}
	app.search.pending = true
	app.handleInspectionResult(tuiInspectionResult{
		request: tuiInspectionRequest{target: key, receiver: oldReceiver, copy: true, version: app.search.version, request: TerminalInteractionRequest{Action: "release"}},
		result:  TerminalInteractionResult{Text: "stale selection", Search: TerminalSearchStatus{Matches: 123, CaughtUp: true}},
	})
	if !app.search.pending || app.search.status.Matches != 0 {
		t.Fatal("old terminal receiver changed replacement search state")
	}
}

func TestInspectionOldEpochCannotCopyEvenWithTheSameLiveReceiver(t *testing.T) {
	app, terminal := newInspectionTestApp(t)
	app.graphicsEpoch = 2
	app.clipboardPush = func(string) error { t.Error("old daemon epoch changed clipboard"); return nil }
	app.handleInspectionResult(tuiInspectionResult{request: tuiInspectionRequest{target: app.state.activeSessionKey(), receiver: terminal, epoch: 1, copy: true}, result: TerminalInteractionResult{Text: "old epoch"}})
	if app.status != "" {
		t.Fatal("old epoch completion changed UI status")
	}
}

func TestSearchCloseRetainsAndRetriesNativeClearWhenInspectionQueueIsFull(t *testing.T) {
	app, _ := newInspectionTestApp(t)
	if err := app.openTerminalSearch(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	close(done)
	app.inspector = &tuiTerminalInspector{requests: make(chan tuiInspectionRequest, 1), stop: make(chan struct{}), done: done}
	app.inspector.requests <- tuiInspectionRequest{}
	app.closeTerminalSearch()
	if app.search == nil || !app.search.closing || app.search.tick == nil {
		t.Fatal("queue-full close silently abandoned native search clear")
	}
	<-app.inspector.requests
	app.searchTick()
	if app.search != nil {
		t.Fatal("search remained open after native clear admission")
	}
	request := <-app.inspector.requests
	if request.request.Action != "search" || request.request.Query != "" {
		t.Fatalf("retry did not clear native query: %+v", request)
	}
}

func TestHostColorAdmissionFailureSchedulesAnIdleRetry(t *testing.T) {
	app, terminal := newInspectionTestApp(t)
	target := app.state.activeSessionKey()
	app.hostColors = &TerminalColors{Theme: 1}
	app.hostColorsSent = map[string]bool{target: true}
	request := tuiInspectionRequest{target: target, receiver: terminal, epoch: app.graphicsEpoch, request: TerminalInteractionRequest{Action: "colors", Colors: app.hostColors}}
	app.handleInspectionResult(tuiInspectionResult{request: request, err: errors.New("renderer temporarily unavailable")})
	if app.hostColorsSent[target] || app.hostColorTicks() == nil || !time.Now().Before(app.hostColorsRetry) {
		t.Fatal("transient color failure has no bounded idle wake")
	}
	t.Cleanup(func() { app.hostColorsTick.Stop() })
	app.propagateHostColors()
	if len(terminal.requests) != 0 {
		t.Fatal("color retry ignored its backoff")
	}
	app.hostColorsRetry = time.Now().Add(-time.Millisecond)
	app.propagateHostColors()
	app.handleInspectionResult(nextInspectionResult(t, app))
	if request := <-terminal.requests; request.Action != "colors" || !app.hostColorsSent[target] {
		t.Fatal("color policy did not retry after backoff")
	}
}
