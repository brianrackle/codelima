package codelima

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
	"go.rockorager.dev/vaxis"
)

func TestTUINodeConfirmationDeleteExactNameAndIdentity(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	node := a.state.nodes[0]
	a.openDeleteNodeDialog(node)
	d := a.overlay.(*tuiNodeConfirmation)
	var deleted []string
	d.run = func(n Node) error { deleted = append(deleted, n.ID); return nil }
	for _, value := range []string{"", "root", node.Slug + " ", " " + node.Slug, strings.ToUpper(node.Slug)} {
		d.dialog.Fields[0].Input.SetContent(value)
		if done, err := d.Update(vaxis.Key{Keycode: vaxis.KeyEnter}); done || err != nil || len(deleted) != 0 {
			t.Fatalf("accepted %q: done=%v err=%v deleted=%v", value, done, err, deleted)
		}
	}
	d.dialog.Fields[0].Input.SetContent(node.Slug)
	a.fleet.selectID(a.state.nodes[1].ID)
	a.selectFleetNode()
	a.fleet.marks[a.state.nodes[1].ID] = true
	if done, err := d.Update(vaxis.Key{Keycode: vaxis.KeyEnter}); !done || err != nil || !slices.Equal(deleted, []string{node.ID}) {
		t.Fatalf("wrong deletion: %v %v %v", done, err, deleted)
	}
	if !a.fleet.marks[a.state.nodes[1].ID] {
		t.Fatal("confirmation changed unrelated marks")
	}
}

func TestTUINodeConfirmationRejectsChangedTargetAndKeepsErrors(t *testing.T) {
	for _, change := range []string{"removed", "renamed", "in flight", "disconnected", "operation error"} {
		t.Run(change, func(t *testing.T) {
			a, _ := newTestManager(t, 80, 24)
			node := a.state.nodes[0]
			a.openDeleteNodeDialog(node)
			d := a.overlay.(*tuiNodeConfirmation)
			d.dialog.Fields[0].Input.SetContent(node.Slug)
			called := false
			d.run = func(Node) error { called = true; return errors.New("operation failed") }
			switch change {
			case "removed":
				a.state.nodes = a.state.nodes[1:]
			case "renamed":
				a.state.nodes[0].Slug = "renamed"
			case "in flight":
				a.state.nodes[0].Status = NodeStatusProvisioning
			case "disconnected":
				a.daemonDisconnected = true
			}
			if done, err := d.Update(vaxis.Key{Keycode: vaxis.KeyEnter}); done || err != nil || d.dialog.Error == "" || called != (change == "operation error") {
				t.Fatalf("invalid submit: done=%v err=%v called=%v message=%q", done, err, called, d.dialog.Error)
			}
		})
	}
}

func TestTUINodeConfirmationPasteReleaseRepeatAndImmediateCancel(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	node := a.state.nodes[0]
	a.openDeleteNodeDialog(node)
	d := a.overlay.(*tuiNodeConfirmation)
	called := false
	d.run = func(Node) error { called = true; return nil }
	_, _ = d.Update(vaxis.PasteStartEvent{})
	for _, r := range node.Slug {
		_, _ = d.Update(vaxis.Key{Keycode: r, Text: string(r), EventType: vaxis.EventPaste})
	}
	_, _ = d.Update(vaxis.PasteEndEvent{})
	if got := d.dialog.Fields[0].Input.String(); got != node.Slug {
		t.Fatalf("paste=%q", got)
	}
	for _, kind := range []vaxis.EventType{vaxis.EventRelease, vaxis.EventRepeat, vaxis.EventPaste} {
		_, _ = d.Update(vaxis.Key{Keycode: vaxis.KeyEnter, EventType: kind})
		if called {
			t.Fatalf("event %v submitted deletion", kind)
		}
	}
	if done, _ := d.Update(vaxis.Key{Keycode: vaxis.KeyEsc}); !done {
		t.Fatal("cancel requested confirmation for disposable typed-name input")
	}
}

func TestTUINodeConfirmationStopOnlyGuestActivity(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind terminal.TerminalKind
		meta TerminalMetadata
		want bool
	}{
		{"host working", terminal.NodeHostShell, TerminalMetadata{ProgressState: 1}, false},
		{"unclassified prompt", terminal.TerminalKind(-1), TerminalMetadata{CursorAtPrompt: true}, true},
		{"guest idle", terminal.NodeShell, TerminalMetadata{CursorAtPrompt: true}, false},
		{"guest unknown", terminal.NodeShell, TerminalMetadata{}, true},
		{"guest working overrides prompt", terminal.NodeShell, TerminalMetadata{CursorAtPrompt: true, ProgressState: 1}, true},
		{"guest stale prompt", terminal.NodeShell, TerminalMetadata{CursorAtPrompt: true, ActivityStale: true}, true},
		{"guest paused", terminal.NodeShell, TerminalMetadata{CursorAtPrompt: true, ProgressState: 4}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, _ := newTestManager(t, 80, 24)
			node := a.state.nodes[0]
			putTestSession(a.sessions, terminal.NodeTarget(node.ID).String(), &tuiSession{shellKind: tc.kind}, &metadataTestTerminal{fakeTUITerminal: newFakeTUITerminal(), metadata: tc.meta})
			if got := a.confirmNodeStop(node); got != tc.want {
				t.Fatalf("confirmation=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestTUINodeConfirmationStopPreferenceAndExactAttachment(t *testing.T) {
	a, _ := newTestManager(t, 100, 30)
	a.service = a.sessions.service
	cfg := a.managerSettings()
	cfg.ConfirmStop = "always"
	a.preferences = &cfg
	node := a.state.nodes[0]
	target := terminal.NodeTarget(node.ID).String()
	idleKey := putTestSession(a.sessions, target, &tuiSession{shellKind: terminal.NodeShell}, &metadataTestTerminal{fakeTUITerminal: newFakeTUITerminal(), metadata: TerminalMetadata{CursorAtPrompt: true}})
	busyKey := putTestSession(a.sessions, target, &tuiSession{shellKind: terminal.NodeShell}, &metadataTestTerminal{fakeTUITerminal: newFakeTUITerminal(), metadata: TerminalMetadata{ProgressState: 1}})
	busy, _ := a.sessions.Session(busyKey)
	a.openStopNodeDialog(node)
	d := a.overlay.(*tuiNodeConfirmation)
	attached, stopped := "", ""
	d.attach = func(id string) error { attached = id; return nil }
	d.run = func(n Node) error { stopped = n.ID; return nil }
	a.fleet.selectID(a.state.nodes[1].ID)
	a.selectFleetNode()
	if done, err := d.Update(vaxis.Key{Keycode: 'x', Text: "x"}); done || err != nil || attached != string(busy.terminalID) {
		t.Fatalf("attach=%q done=%v err=%v", attached, done, err)
	}
	a.sessions.CloseSession(busyKey)
	attached = ""
	_, _ = d.Update(vaxis.Key{Keycode: 'x', Text: "x"})
	if attached != "" || d.dialog.Error == "" {
		t.Fatal("ended busy session silently fell back to idle session", idleKey)
	}
	_, _ = d.Update(vaxis.Key{Keycode: ' ', Text: " "})
	if done, err := d.Update(vaxis.Key{Keycode: 's', Text: "s"}); !done || err != nil || stopped != node.ID {
		t.Fatalf("stop=%q done=%v err=%v", stopped, done, err)
	}
	loaded, err := LoadConfig(a.service.cfg.MetadataRoot)
	if err != nil || loaded.ConfirmStop != "busy" {
		t.Fatalf("preference not persisted: %q %v", loaded.ConfirmStop, err)
	}
}

func TestTUINodeConfirmationResponsiveImpactAndScrolling(t *testing.T) {
	for _, width := range []int{120, 80, 60} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a, _ := newTestManager(t, width, 24)
			node := &a.state.nodes[0]
			node.VCPUs, node.MemoryMiB, node.DiskMiB = 4, 8192, 102400
			node.WorkspaceMode, node.Ports = "copy", []string{"15432:5432"}
			node.LastRuntimeObservation = &RuntimeObservation{Exists: true, Status: ObservationRunning}
			a.openDeleteNodeDialog(*node)
			a.draw()
			text := renderedScreenText(t, a.vx, width, 24)
			for _, want := range []string{"Delete root-node?", "100 GiB", "copy", "15432:5432", "Type root-node", "Esc"} {
				if !strings.Contains(text, want) {
					t.Fatalf("missing %q:\n%s", want, text)
				}
			}
			for i := range 15 {
				putTestSession(a.sessions, terminal.NodeTarget(node.ID).String(), &tuiSession{shellKind: terminal.NodeShell, createdAt: time.Now().Add(-time.Hour)}, &metadataTestTerminal{fakeTUITerminal: newFakeTUITerminal(), metadata: TerminalMetadata{Title: fmt.Sprintf("session-%02d", i), ProgressState: 1}})
			}
			putTestSession(a.sessions, terminal.NodeTarget(node.ID).String(), &tuiSession{shellKind: terminal.NodeHostShell}, newFakeTUITerminal())
			a.openStopNodeDialog(*node)
			d := a.overlay.(*tuiNodeConfirmation)
			a.draw()
			text = renderedScreenText(t, a.vx, width, 24)
			if !strings.Contains(text, "15 guest sessions") || !strings.Contains(text, "session-00") || !strings.Contains(text, "PgUp/PgDn") {
				t.Fatalf("missing session review:\n%s", text)
			}
			_, _ = d.Update(vaxis.Key{Keycode: vaxis.KeyEnd})
			a.draw()
			text = renderedScreenText(t, a.vx, width, 24)
			for _, want := range []string{"4 vCPU", "8 GiB", "1 host session keeps running", "Esc"} {
				if !strings.Contains(text, want) {
					t.Fatalf("missing scrolled impact %q:\n%s", want, text)
				}
			}
		})
	}
}

func TestTUINodeConfirmationStopCompletionRetainsHostTab(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	target := terminal.NodeTarget(a.state.nodes[0].ID).String()
	guest := putTestSession(a.sessions, target, &tuiSession{shellKind: terminal.NodeShell}, newFakeTUITerminal())
	host := putTestSession(a.sessions, target, &tuiSession{shellKind: terminal.NodeHostShell}, newFakeTUITerminal())
	if err := a.applyOperationResult("", tuiOperationResult{CloseGuestNodeID: a.state.nodes[0].ID}); err != nil {
		t.Fatal(err)
	}
	if keys := a.sessions.TargetSessionKeys(target); !slices.Equal(keys, []string{host}) {
		t.Fatalf("stop retained %v; guest=%s host=%s", keys, guest, host)
	}
}

func TestTUINodeConfirmationMouseAndSmallViewport(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	node := a.state.nodes[0]
	a.openDeleteNodeDialog(node)
	d := a.overlay.(*tuiNodeConfirmation)
	called := false
	d.run = func(Node) error { called = true; return nil }
	a.draw()
	button := d.controls[0].rect
	click := vaxis.Mouse{Button: vaxis.MouseLeftButton, Col: button.col, Row: button.row}
	if done, _ := d.Update(click); done || called {
		t.Fatal("mouse bypassed typed confirmation")
	}
	d.dialog.Fields[0].Input.SetContent(node.Slug)
	click.EventType = vaxis.EventRelease
	_, _ = d.Update(click)
	if called {
		t.Fatal("mouse release submitted")
	}
	d.Draw(a.vx.Window().New(0, 0, 40, 8), vaxis.Style{}, vaxis.Style{}, vaxis.Style{})
	_, _ = d.Update(vaxis.Key{Keycode: vaxis.KeyEnter})
	if called || len(d.controls) != 0 {
		t.Fatal("unreadable review still allowed submission")
	}
	a.draw()
	click.EventType = vaxis.EventPress
	if done, _ := d.Update(click); !done || !called {
		t.Fatal("mouse did not submit after size was restored")
	}
}

func TestTUINodeConfirmationStopPasteAndCancelDoNotSavePreference(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	a.service = a.sessions.service
	a.openStopNodeDialog(a.state.nodes[0])
	d := a.overlay.(*tuiNodeConfirmation)
	called := false
	d.run = func(Node) error { called = true; return nil }
	d.attach = func(string) error { called = true; return nil }
	initial := d.skipIdle
	for _, kind := range []vaxis.EventType{vaxis.EventRelease, vaxis.EventRepeat, vaxis.EventPaste} {
		for _, r := range "sx " {
			_, _ = d.Update(vaxis.Key{Keycode: r, Text: string(r), EventType: kind})
		}
	}
	if called || d.skipIdle != initial {
		t.Fatal("paste/release/repeat activated a stop control")
	}
	_, _ = d.Update(vaxis.Key{Keycode: ' ', Text: " "})
	if done, _ := d.Update(vaxis.Key{Keycode: vaxis.KeyEsc}); !done || a.managerSettings().ConfirmStop != "busy" {
		t.Fatal("cancel saved the edited preference")
	}
}

func TestTUINodeConfirmationRevalidatesAfterAsyncSettingsSave(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	a.service = a.sessions.service
	events := make(chan vaxis.Event, 20)
	a.postEvent = func(event vaxis.Event) { events <- event }
	a.openStopNodeDialog(a.state.nodes[0])
	d := a.overlay.(*tuiNodeConfirmation)
	called := false
	d.run = func(Node) error { called = true; return nil }
	_, _ = d.Update(vaxis.Key{Keycode: ' ', Text: " "})
	if done, err := d.Update(vaxis.Key{Keycode: 's', Text: "s"}); !done || err != nil {
		t.Fatalf("submit=%v %v", done, err)
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-events:
			if completion, ok := event.(tuiOperationCompleteEvent); ok {
				a.state.nodes = a.state.nodes[1:]
				a.finishOperation(completion)
				if called || a.managerSettings().ConfirmStop != "always" {
					t.Fatal("late preference save stopped a missing node or did not save")
				}
				return
			}
		case <-timer.C:
			t.Fatal("settings save did not complete")
		}
	}
}

func TestTUINodeConfirmationRetainsOnlyMatchingStopEvidence(t *testing.T) {
	a, _ := newTestManager(t, 100, 30)
	node := &a.state.nodes[0]
	node.Status, node.WorkspaceMode = NodeStatusStopped, "mounted"
	node.UpdatedAt = time.Now().Add(-time.Hour)
	stopped := node.UpdatedAt
	a.fleetEvidence = tuiFleetEvidenceState{loaded: true, receivedAt: time.Now(), snapshot: fleetEvidenceSnapshot{Nodes: map[string]fleetNodeEvidence{node.ID: {NodeUpdatedAt: node.UpdatedAt, StoppedAt: &stopped}}}}
	a.openDeleteNodeDialog(*node)
	d := a.overlay.(*tuiNodeConfirmation)
	joined := func() string {
		var lines []string
		for _, line := range d.lines(a.managerTheme()) {
			lines = append(lines, line.text)
		}
		return strings.Join(lines, "\n")
	}
	if text := joined(); !strings.Contains(text, "Last confirmed stop") || !strings.Contains(text, "host directory stays") {
		t.Fatal(text)
	}
	node.UpdatedAt = time.Now()
	if text := joined(); strings.Contains(text, "Last confirmed stop") || !strings.Contains(text, "Last stop: unavailable") {
		t.Fatal(text)
	}
}

func TestTUINodeConfirmationGlobalMinimumSizeCannotSubmitHiddenReview(t *testing.T) {
	for _, action := range []tuiActionID{tuiActionNodeDelete, tuiActionNodeStop} {
		t.Run(string(action), func(t *testing.T) {
			a, _ := newTestManager(t, 80, 24)
			node := a.state.nodes[0]
			called := false
			d := a.newNodeConfirmation(node, action, func(Node) error { called = true; return nil })
			a.overlay = d
			if action == tuiActionNodeDelete {
				d.dialog.Fields[0].Input.SetContent(node.Slug)
			}
			a.draw()
			for _, size := range []vaxis.Resize{{Cols: 40, Rows: 10}, {Cols: 59, Rows: 24}, {Cols: 80, Rows: 13}, {Cols: 60, Rows: 14}} {
				_, _ = a.handleEvent(size)
				// Input can arrive between resize and the next draw, or after the
				// global minimum screen skipped drawing the overlay altogether.
				_, _ = d.Update(vaxis.Key{Keycode: vaxis.KeyEnter})
				a.draw()
				if done, err := d.Update(vaxis.Key{Keycode: vaxis.KeyEnter}); done || err != nil || called {
					t.Fatalf("hidden review submitted at %+v: %v %v %v", size, done, err, called)
				}
			}
			_, _ = a.handleEvent(vaxis.Resize{Cols: 80, Rows: 24})
			a.draw()
			if done, err := d.Update(vaxis.Key{Keycode: vaxis.KeyEnter}); !done || err != nil || !called {
				t.Fatalf("restored review could not submit: %v %v", done, err)
			}
		})
	}
}
