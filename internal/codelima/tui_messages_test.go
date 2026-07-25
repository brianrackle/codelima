package codelima

import (
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"git.sr.ht/~rockorager/vaxis"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
)

func TestMessageLogAppendPreservesOrderAndLatest(t *testing.T) {
	t.Parallel()

	log := newTUIMessageLog(10)
	log.Append(slog.LevelInfo, "first")
	log.Append(slog.LevelWarn, "second")
	log.Append(slog.LevelError, "third")

	if got := log.Len(); got != 3 {
		t.Fatalf("Len() = %d, want 3", got)
	}
	entries := log.Entries()
	wantText := []string{"first", "second", "third"}
	for index, want := range wantText {
		if entries[index].Text != want {
			t.Fatalf("entries[%d].Text = %q, want %q", index, entries[index].Text, want)
		}
	}
	if entries[1].Level != slog.LevelWarn {
		t.Fatalf("entries[1].Level = %v, want warn", entries[1].Level)
	}
	latest, ok := log.Latest()
	if !ok || latest.Text != "third" {
		t.Fatalf("Latest() = (%+v, %v), want third", latest, ok)
	}
}

func TestDaemonInputRevokedEventIsSilentDuringFocusHandoff(t *testing.T) {
	t.Parallel()

	client := &daemonclient.Client{Hello: daemon.HelloResult{ClientID: "owner-client"}}
	var events []vaxis.Event
	store := &tuiSessionStore{
		service:   &Service{daemonClient: client},
		postEvent: func(event vaxis.Event) { events = append(events, event) },
	}

	store.handleDaemonEvent(daemon.Event{Event: "input.revoked", Data: map[string]any{"client_id": "owner-client"}})
	if len(events) != 0 {
		t.Fatalf("input revocation posted %d UI events, want none", len(events))
	}
}

func TestMessageLogOverflowEvictsOldest(t *testing.T) {
	t.Parallel()

	log := newTUIMessageLog(3)
	for i := 0; i < 6; i++ {
		log.Append(slog.LevelInfo, fmt.Sprintf("msg-%d", i))
	}

	if got := log.Len(); got != 3 {
		t.Fatalf("Len() after overflow = %d, want cap 3", got)
	}
	entries := log.Entries()
	want := []string{"msg-3", "msg-4", "msg-5"}
	for index, expected := range want {
		if entries[index].Text != expected {
			t.Fatalf("entries[%d].Text = %q, want %q (oldest should be evicted, order preserved)", index, entries[index].Text, expected)
		}
	}
}

func TestMessageLogIgnoresEmptyMessages(t *testing.T) {
	t.Parallel()

	log := newTUIMessageLog(10)
	log.Append(slog.LevelInfo, "")
	log.Append(slog.LevelInfo, "   \n")
	log.Append(slog.LevelInfo, "kept")

	if got := log.Len(); got != 1 {
		t.Fatalf("Len() = %d, want 1 (empty/whitespace messages dropped)", got)
	}
	if latest, _ := log.Latest(); latest.Text != "kept" {
		t.Fatalf("Latest().Text = %q, want kept", latest.Text)
	}
}

func TestSetStatusMirrorsStatusIntoRingAtLevel(t *testing.T) {
	t.Parallel()

	app := &vaxisTUIApp{messages: newTUIMessageLog(10)}

	// setStatus records into the ring at the given level and sets the footer.
	app.setStatus(slog.LevelInfo, "opened https://example")
	if app.status != "opened https://example" || app.statusLevel != slog.LevelInfo {
		t.Fatalf("setStatus did not set footer status/level, got %q/%v", app.status, app.statusLevel)
	}
	if got := app.messages.Len(); got != 1 {
		t.Fatalf("setStatus recorded %d ring entries, want 1", got)
	}

	// An error status records at error level.
	app.setStatus(slog.LevelError, "something failed")
	if app.statusLevel != slog.LevelError {
		t.Fatalf("setStatus(error) level = %v, want error", app.statusLevel)
	}
	entries := app.messages.Entries()
	if len(entries) != 2 || entries[1].Level != slog.LevelError {
		t.Fatalf("expected the error status recorded at error level, got %+v", entries)
	}

	// clearStatus clears the footer and records nothing.
	app.clearStatus()
	if app.status != "" {
		t.Fatalf("clearStatus did not clear the footer, got %q", app.status)
	}
	if got := app.messages.Len(); got != 2 {
		t.Fatalf("clearStatus recorded a ring entry, Len() = %d, want 2", got)
	}
}

func TestMessagesViewOpensAndClosesViaKeys(t *testing.T) {
	t.Parallel()

	state, err := newTUIState(nil, newFakeTUISessionManager())
	if err != nil {
		t.Fatalf("newTUIState() error = %v", err)
	}
	app := &vaxisTUIApp{state: state, messages: newTUIMessageLog(200)}
	app.messages.Append(slog.LevelInfo, "hello world")

	app.handleKey(vaxis.Key{Text: "m", Keycode: 'm'})
	if app.activeMessagesView() == nil {
		t.Fatal("expected 'm' to open the messages view")
	}
	if !app.rightPaneOverrideActive() {
		t.Fatal("messages view should register as an active right-pane override")
	}

	if _, err := app.handleEvent(vaxis.Key{Keycode: vaxis.KeyEsc}); err != nil {
		t.Fatalf("handleEvent(Esc) error = %v", err)
	}
	if app.activeMessagesView() != nil {
		t.Fatal("expected Esc to close the messages view")
	}
}

func TestMessagesViewRendersNewestAndScrolls(t *testing.T) {
	messages := make([]tuiMessage, 0, 12)
	for i := 0; i < 12; i++ {
		messages = append(messages, tuiMessage{Level: slog.LevelInfo, Text: fmt.Sprintf("msg-%02d", i)})
	}
	view := newTUIMessagesView(messages)

	vx := newRenderTestVaxis(t, 40, 10)
	defer vx.Close()

	render := func() string {
		win := vx.Window()
		win.Clear()
		view.Draw(win, vaxis.Style{}, tuiMutedStyle(), vaxis.Style{})
		return renderedScreenText(t, vx, 40, 10)
	}

	// Opens pinned to the bottom: the newest entry shows, the oldest does not.
	screen := render()
	if !strings.Contains(screen, "msg-11") {
		t.Fatalf("expected newest message visible on open, got:\n%s", screen)
	}
	if strings.Contains(screen, "msg-00") {
		t.Fatalf("did not expect the oldest message visible while pinned to bottom, got:\n%s", screen)
	}

	// Home jumps to the top: oldest shows, newest scrolls off.
	_, _ = view.Update(vaxis.Key{Text: "g", Keycode: 'g'})
	screen = render()
	if !strings.Contains(screen, "msg-00") {
		t.Fatalf("expected oldest message visible after Home, got:\n%s", screen)
	}
	if strings.Contains(screen, "msg-11") {
		t.Fatalf("did not expect the newest message visible after scrolling to top, got:\n%s", screen)
	}

	// Down/Up move the scroll offset by one line.
	if view.scroll != 0 {
		t.Fatalf("scroll after Home = %d, want 0", view.scroll)
	}
	_, _ = view.Update(vaxis.Key{Keycode: vaxis.KeyDown})
	if view.scroll != 1 {
		t.Fatalf("scroll after Down = %d, want 1", view.scroll)
	}
	_, _ = view.Update(vaxis.Key{Keycode: vaxis.KeyUp})
	if view.scroll != 0 {
		t.Fatalf("scroll after Up = %d, want 0", view.scroll)
	}
}

// TestRefreshErrorRecordedInMessagesAtWarnLevel pins the architect-review gap:
// a failed background refresh must land in the message ring at warn level (not
// only in the slog seam), and a persistently failing auto-refresh must not
// flood the ring with identical consecutive entries.
func TestRefreshErrorRecordedInMessagesAtWarnLevel(t *testing.T) {
	t.Parallel()

	app := &vaxisTUIApp{messages: newTUIMessageLog(10)}

	app.finishDataRefresh(tuiRefreshCompleteEvent{Err: fmt.Errorf("sandbox unreachable")})

	entries := app.messages.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected exactly one ring entry after a failed refresh, got %d (%+v)", len(entries), entries)
	}
	if entries[0].Level != slog.LevelWarn {
		t.Fatalf("refresh failure recorded at level %v, want warn", entries[0].Level)
	}
	if !strings.Contains(entries[0].Text, "refresh failed") || !strings.Contains(entries[0].Text, "sandbox unreachable") {
		t.Fatalf("refresh failure text = %q, want it to name the failure", entries[0].Text)
	}

	// The same error on the next tick must not append a duplicate...
	app.finishDataRefresh(tuiRefreshCompleteEvent{Err: fmt.Errorf("sandbox unreachable")})
	if got := app.messages.Len(); got != 1 {
		t.Fatalf("identical consecutive refresh failure duplicated in ring, Len() = %d, want 1", got)
	}

	// ...but a different error must be recorded.
	app.finishDataRefresh(tuiRefreshCompleteEvent{Err: fmt.Errorf("metadata corrupt")})
	if got := app.messages.Len(); got != 2 {
		t.Fatalf("distinct refresh failure not recorded, Len() = %d, want 2", got)
	}
}

func TestFinishOperationRetainsFailedOutputInRing(t *testing.T) {
	t.Parallel()

	app := &vaxisTUIApp{
		messages:       newTUIMessageLog(200),
		operations:     map[string]*tuiOperationState{},
		operationOrder: []string{},
	}
	op := &tuiOperationState{
		ID:    "op-1",
		Title: "Start Node root",
		Lines: []string{"waiting for command output...", "bootstrap: step 1", "bootstrap: boom"},
	}
	app.operations[op.ID] = op
	app.operationOrder = append(app.operationOrder, op.ID)

	app.finishOperation(tuiOperationCompleteEvent{OperationID: op.ID, Err: fmt.Errorf("bootstrap failed")})

	entries := app.messages.Entries()
	if len(entries) == 0 {
		t.Fatal("expected finished operation output to be retained in the ring")
	}
	var sawSummary, sawOutput, sawPlaceholder bool
	for _, entry := range entries {
		if strings.Contains(entry.Text, "Start Node root failed: bootstrap failed") {
			sawSummary = true
			if entry.Level != slog.LevelError {
				t.Fatalf("failure summary level = %v, want error", entry.Level)
			}
		}
		if strings.Contains(entry.Text, "bootstrap: boom") {
			sawOutput = true
		}
		if strings.Contains(entry.Text, "waiting for command output") {
			sawPlaceholder = true
		}
	}
	if !sawSummary {
		t.Fatalf("expected a failure summary entry, got %+v", entries)
	}
	if !sawOutput {
		t.Fatalf("expected retained operation output in the ring, got %+v", entries)
	}
	if sawPlaceholder {
		t.Fatalf("placeholder output line should be filtered out, got %+v", entries)
	}
}
