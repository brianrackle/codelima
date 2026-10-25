package codelima

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/term"
)

func TestTUIActivityTypedTargetsOpenRelevantViews(t *testing.T) {
	for _, tc := range []struct {
		action activityAction
		view   tuiView
		panel  int
	}{
		{activityOpenHost, tuiViewSystem, tuiSystemHost},
		{activityOpenDaemon, tuiViewSystem, tuiSystemDaemon},
		{activityOpenPorts, tuiViewPorts, 0},
	} {
		a := testActivityApp(t, 80, 24)
		a.activity.page.Running = nil
		a.activity.page.History = []ActivityRecord{{ID: "typed", Action: tc.action, Text: "arbitrary text"}}
		a.activity.selected = "typed"
		a.handleKey(vaxis.Key{Keycode: vaxis.KeyEnter})
		if a.view != tc.view || (a.view == tuiViewSystem && a.systemPanel != tc.panel) {
			t.Fatal("typed action did not navigate", tc, a.view, a.systemPanel)
		}
	}
}

func TestTUIActivityLegacyNotificationUsesHistory(t *testing.T) {
	a := testActivityApp(t, 80, 24)
	_, err := a.handleEvent(term.EventNotify{Title: "Task\x1b", Body: "Done\nnow"})
	entries := a.messages.Entries()
	if err != nil || len(entries) != 1 || !strings.Contains(entries[0].Text, "Task: Donenow") {
		t.Fatal("legacy notification did not enter bounded history", entries, err)
	}
}

type activitySessionCaller struct {
	sessions []daemon.TerminalState
	calls    int
}

func (c *activitySessionCaller) Call(_ context.Context, method string, _ any, result any) error {
	c.calls++
	if method != "terminal.list" {
		return errors.New("unexpected mutation: " + method)
	}
	*result.(*[]daemon.TerminalState) = c.sessions
	return nil
}

func TestTUIActivitySessionResolutionNeverSubstitutesOrCreates(t *testing.T) {
	id := newID()
	want := daemon.TerminalState{TerminalID: "term_chosen", Target: "node:" + id, Kind: "node-host-shell"}
	client := &activitySessionCaller{sessions: []daemon.TerminalState{{TerminalID: "term_first", Target: want.Target, Kind: want.Kind}, want}}
	got, err := resolveActivityTerminal(context.Background(), client, id, want.TerminalID)
	if err != nil || got.TerminalID != want.TerminalID || client.calls != 1 {
		t.Fatal(got, err, client.calls)
	}
	for _, target := range []struct{ node, session string }{{newID(), want.TerminalID}, {id, "closed"}} {
		if _, err := resolveActivityTerminal(context.Background(), client, target.node, target.session); err == nil {
			t.Fatal("wrong or ended session silently substituted", target)
		}
	}
}

func TestTUIActivityAttachmentCancellationFencesLateReply(t *testing.T) {
	a := testActivityApp(t, 80, 24)
	ctx, cancel := context.WithCancel(context.Background())
	a.activity.attachCancel, a.activity.attachSerial, a.rawShellPending = cancel, 1, true
	a.activity.selected = "chosen"
	a.switchView(tuiViewNodes)
	if ctx.Err() == nil || a.rawShellPending {
		t.Fatal("navigation did not cancel pending attachment")
	}
	a.status = "unchanged"
	a.finishActivityTerminal(tuiActivityTerminalEvent{serial: 1, rowID: "chosen", err: errors.New("late")})
	if a.status != "unchanged" {
		t.Fatal("late reply acted after navigation", a.status)
	}
}
