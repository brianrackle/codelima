package codelima

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/terminal"
	"go.rockorager.dev/vaxis"
)

func overviewFixture(t *testing.T, width, height int) *vaxisTUIApp {
	t.Helper()
	a, _ := newTestManager(t, width, height)
	node := &a.state.nodes[0]
	node.Status = NodeStatusRunning
	a.refreshFleet()
	a.fleet.selectID(node.ID)
	a.selectFleetNode()
	a.openNodeDetail(tuiDetailOverview)
	for _, label := range []string{"first task", "second task", "third task"} {
		putTestSession(a.sessions, nodeTargetKey(node.ID), &tuiSession{label: label, shellKind: terminal.NodeShell}, newFakeTUITerminal())
	}
	a.ports = tuiPortsState{loaded: true, snapshot: daemon.PortsSnapshot{Enabled: true, Authorized: true, LastPollAt: time.Now(), Nodes: []daemon.PortNode{{ID: node.ID, Ready: true}}, Rows: []daemon.PortRoute{
		{ID: "first", NodeID: node.ID, Kind: "http", Port: 3000, Address: "http://first.localhost:3000", Status: "live"},
		{ID: "second", NodeID: node.ID, Kind: "http", Port: 4000, Address: "http://second.localhost:4000", Status: "live"},
	}}}
	a.draw()
	return a
}

func TestTUIOverviewMouseSelectsRowsAndOpensPanelLinks(t *testing.T) {
	a := overviewFixture(t, 150, 38)
	var selected tuiOverviewRow
	for _, row := range a.overview.rows {
		if row.panel == tuiOverviewPorts && row.id == "second" {
			selected = row
		}
	}
	a.handleMouse(vaxis.Mouse{Col: selected.rect.col, Row: selected.rect.row, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress})
	if a.overview.panel != tuiOverviewPorts || a.overview.route != "second" {
		t.Fatal("mouse selected a different route")
	}
	a.handleMouse(vaxis.Mouse{Col: selected.rect.col, Row: selected.rect.row, Button: vaxis.MouseWheelUp, EventType: vaxis.EventPress})
	if a.overview.route != "first" {
		t.Fatal("wheel failed to move within Ports")
	}
	a.draw()
	for _, control := range a.overview.controls {
		if control.key == "metrics" {
			a.handleMouse(vaxis.Mouse{Col: control.rect.col, Row: control.rect.row, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress})
			if a.detailTab != tuiDetailMetrics {
				t.Fatal("Metrics link did not navigate")
			}
			return
		}
	}
	t.Fatal("missing Metrics link")
}

func TestTUIOverviewSessionAgeComesFromDaemonAcrossAdoption(t *testing.T) {
	service, _ := newTestService(t)
	store := newTUISessionStore(context.Background(), service, nil)
	t.Cleanup(store.Close)
	created := time.Now().Add(-time.Hour)
	state := daemon.TerminalState{TerminalID: "overview-retained", TabID: "node:one#1", Target: "node:one", Kind: terminal.NodeHostShell.String(), CreatedAt: created}
	key, err := store.installDaemonTab(tuiDaemonTerminalOpenedEvent{TargetKey: state.Target, ShellKind: terminal.NodeHostShell, State: state})
	if err != nil {
		t.Fatal(err)
	}
	if !store.sessions[key].createdAt.Equal(created) {
		t.Fatal("adoption reset age")
	}
	state.TabID = "node:one#2"
	if err := store.replaceDaemonSessions([]daemon.TerminalState{state}); err != nil {
		t.Fatal(err)
	}
	if !store.sessions[state.TabID].createdAt.Equal(created) {
		t.Fatal("resync reset age")
	}
	for _, when := range []time.Time{{}, time.Now().Add(time.Hour)} {
		if overviewAge(when, time.Now()) != "—" {
			t.Fatal("invented age", when)
		}
	}
}

func TestTUIOverviewRecentAndResourcesRetainUnknownAndStale(t *testing.T) {
	a := overviewFixture(t, 150, 38)
	node := a.state.selectedEntry().node
	a.fleetEvidence = tuiFleetEvidenceState{loaded: true, receivedAt: time.Now().Add(-time.Minute), snapshot: fleetEvidenceSnapshot{RecentNode: node.ID, Recent: []ActivityRecord{
		{NodeID: node.ID, Time: time.Now(), Text: "selected event"},
		{NodeID: "other", Time: time.Now(), Text: "must not leak"},
	}}}
	a.draw()
	screen := renderedScreenText(t, a.vx, 150, 38)
	for _, want := range []string{"Recent events", "stale", "selected event", "Last observed usage", "CPU    0 vCPU · —"} {
		if !strings.Contains(screen, want) {
			t.Errorf("missing %q:\n%s", want, screen)
		}
	}
	if strings.Contains(screen, "must not leak") {
		t.Fatal("cross-node event leaked")
	}
}

func TestTUIOverviewPanelsAndCompactNavigation(t *testing.T) {
	for _, size := range [][2]int{{150, 38}, {80, 24}} {
		a := overviewFixture(t, size[0], size[1])
		nodeID := a.fleet.selectedID
		seen := map[string]bool{}
		for range tuiOverviewPanelCount {
			a.draw()
			screen := renderedScreenText(t, a.vx, size[0], size[1])
			for _, name := range []string{"Identity", "Resources", "Sessions", "Ports", "Recent events"} {
				seen[name] = seen[name] || strings.Contains(screen, name)
			}
			a.handleKey(vaxis.Key{Keycode: vaxis.KeyDown, Modifiers: vaxis.ModCtrl})
		}
		for name, present := range seen {
			if !present {
				t.Errorf("%dx%d lost %s", size[0], size[1], name)
			}
		}
		if a.overview.panel != tuiOverviewSessions || a.fleet.selectedID != nodeID {
			t.Fatal("panel traversal moved node or failed to wrap")
		}
		a.handleKey(vaxis.Key{Keycode: vaxis.KeyTab})
		if a.detailTab != tuiDetailMetrics {
			t.Fatal("Tab no longer cycles detail tabs")
		}
	}
}

func TestTUIOverviewSelectionSurvivesRefreshAndClosesExactSession(t *testing.T) {
	a := overviewFixture(t, 150, 38)
	nodeID := a.fleet.selectedID
	keys := a.sessions.TargetSessionKeys(nodeTargetKey(nodeID))
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyDown})
	if a.overview.session != keys[1] {
		t.Fatal("Down did not select second session")
	}
	if err := a.sessions.MoveTab(nodeTargetKey(nodeID), keys[1], -1); err != nil {
		t.Fatal(err)
	}
	a.draw()
	if a.overview.session != keys[1] || a.fleet.selectedID != nodeID {
		t.Fatal("reordering retargeted selection")
	}
	a.handleKey(vaxis.Key{Keycode: 'K', Text: "K", EventType: vaxis.EventRelease})
	if a.overlay != nil {
		t.Fatal("release opened end confirmation")
	}
	a.handleKey(vaxis.Key{Keycode: 'K', Text: "K"})
	d, ok := a.overlay.(*tuiDialog)
	if !ok {
		t.Fatal("end session omitted confirmation")
	}
	a.overview.session = keys[2]
	if err := d.OnSubmit(nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.sessions.Session(keys[1]); ok {
		t.Fatal("confirmed session retained")
	}
	if _, ok := a.sessions.Session(keys[2]); !ok {
		t.Fatal("confirmation followed changed selection")
	}
}

func TestTUIOverviewOpensSelectedRouteOnlyWhenCurrent(t *testing.T) {
	a := overviewFixture(t, 150, 38)
	var opened string
	a.openLink = func(url string) error { opened = url; return nil }
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyDown, Modifiers: vaxis.ModCtrl})
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyDown})
	a.handleKey(vaxis.Key{Keycode: 'o', Text: "o"})
	if opened != "http://second.localhost:4000" {
		t.Fatal("opened wrong route", opened)
	}
	var copied string
	a.clipboardPush = func(text string) error { copied = text; return nil }
	a.handleKey(vaxis.Key{Keycode: 'y', Text: "y"})
	if copied != opened {
		t.Fatal("copied another route", copied)
	}
	for _, condition := range []string{"stale", "disconnected", "disabled", "unauthorized", "not ready", "TCP", "held", "removed"} {
		saved := a.ports.snapshot
		a.ports.snapshot.Rows = append([]daemon.PortRoute(nil), saved.Rows...)
		a.ports.snapshot.Nodes = append([]daemon.PortNode(nil), saved.Nodes...)
		opened = ""
		switch condition {
		case "stale":
			a.ports.snapshot.LastPollAt = time.Now().Add(-time.Minute)
		case "disconnected":
			a.daemonDisconnected = true
		case "disabled":
			a.ports.snapshot.Enabled = false
		case "unauthorized":
			a.ports.snapshot.Authorized = false
		case "not ready":
			a.ports.snapshot.Nodes[0].Ready = false
		case "TCP":
			a.ports.snapshot.Rows[1].Kind = "tcp"
		case "held":
			a.ports.snapshot.Rows[1].Status = "held"
		case "removed":
			a.ports.snapshot.Rows = a.ports.snapshot.Rows[:1]
		}
		// Act before a redraw: disappearing rows cannot redirect the action.
		a.handleKey(vaxis.Key{Keycode: 'o', Text: "o"})
		if opened != "" {
			t.Errorf("%s opened %s", condition, opened)
		}
		a.ports.snapshot, a.daemonDisconnected = saved, false
	}
}

func TestTUIOverviewReadableTitlesDriftAndInvalidResourceSamples(t *testing.T) {
	a := overviewFixture(t, 150, 38)
	for _, panel := range []int{tuiOverviewIdentity, tuiOverviewResources, tuiOverviewRecent} {
		r := a.overview.rects[panel]
		if renderedCellStyle(t, a.vx, r.col+3, r.row).Foreground != a.managerTheme().text.Foreground {
			t.Fatal("inactive panel title lost contrast", panel)
		}
	}
	a.spec.nodeID = a.fleet.selectedID
	a.spec.snapshot.Differences = []nodeSpecDifference{{}}
	used, total := uint64(2<<30), uint64(1<<30)
	a.state.nodes[0].LastRuntimeObservation = &RuntimeObservation{MemoryUsedBytes: &used, MemoryTotalBytes: &total, DiskUsedBytes: &used, DiskTotalBytes: &total}
	a.draw()
	screen := renderedScreenText(t, a.vx, 150, 38)
	if !strings.Contains(screen, "changed →") {
		t.Fatal("template drift missing")
	}
	if strings.Contains(screen, "2 / 1 GiB") {
		t.Fatal("invalid memory/disk sample looked usable")
	}
	a.overview.session = "ended"
	a.draw()
	screen = renderedScreenText(t, a.vx, 150, 38)
	if strings.Contains(screen, "selectUI") || !strings.Contains(screen, "Session ended · ↑↓ to select") {
		t.Fatal("ended-session heading contains old text")
	}
}

func TestTUIOverviewExplicitShellOpenSelectsItsTerminal(t *testing.T) {
	a := overviewFixture(t, 150, 38)
	state := daemon.TerminalState{TerminalID: "overview-new", TabID: "overview-new-tab", Target: nodeTargetKey(a.fleet.selectedID), Kind: terminal.NodeHostShell.String(), CreatedAt: time.Now()}
	a.finishRawShell(tuiRawShellReadyEvent{State: state, Mode: "window"})
	if a.overview.session != state.TabID {
		t.Fatal("explicit creation did not select the new session")
	}
	state.TerminalID, state.TabID, state.Target = "other-new", "other-new-tab", nodeTargetKey(a.state.nodes[1].ID)
	a.finishRawShell(tuiRawShellReadyEvent{State: state, Mode: "window"})
	if a.overview.session != "overview-new-tab" {
		t.Fatal("another node's late completion changed selection")
	}
}

func TestTUIOverviewStoppedEmptySessionsOfferHostShell(t *testing.T) {
	a := overviewFixture(t, 150, 38)
	a.sessions.CloseTargetSessions(nodeTargetKey(a.fleet.selectedID))
	a.state.nodes[0].Status = NodeStatusStopped
	a.draw()
	if screen := renderedScreenText(t, a.vx, 150, 38); !strings.Contains(screen, "No sessions · T opens host shell") || strings.Contains(screen, "t creates a shell") {
		t.Fatal("stopped empty state offered an unavailable guest shell")
	}
}
