package codelima

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/terminal"
	"go.rockorager.dev/vaxis"
)

func enableTestEmbeddedShell(a *vaxisTUIApp) {
	cfg := a.managerSettings()
	cfg.Shell.Mode = "embedded"
	a.preferences = &cfg
}

func TestTUIEmbeddedShellOptInOwnsDetailVisibilityAndF6(t *testing.T) {
	a, sessions := newTestManager(t, 100, 30)
	node := a.state.selectedEntry().node
	putTestSession(a.sessions, nodeTargetKey(node.ID), &tuiSession{shellKind: terminal.NodeShell}, newFakeTUITerminal())
	a.openNodeDetail(tuiDetailSpec)
	a.draw()
	if strings.Contains(renderedScreenText(t, a.vx, 100, 30), " Shell ") {
		t.Fatal("raw session made embedded detail visible")
	}
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyTab})
	if a.detailTab != tuiDetailOverview {
		t.Fatal("detail navigation entered an unconfigured embedded terminal")
	}
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyF06})
	if a.state.focus == tuiFocusTerminal || len(sessions.opened) != 0 {
		t.Fatal("F6 enabled embedded mode implicitly")
	}
	enableTestEmbeddedShell(a)
	a.openNodeDetail(tuiDetailSpec)
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyTab})
	if a.detailTab != tuiDetailShell {
		t.Fatal("configured embedded detail missing")
	}
	a.handleKey(vaxis.Key{Keycode: vaxis.KeyF06})
	if a.state.focus != tuiFocusTerminal {
		t.Fatal("F6 did not focus the existing terminal")
	}
}

func TestTUIEmbeddedShellFrameAndActiveTabSurviveResize(t *testing.T) {
	a, _ := newTestManager(t, 150, 38)
	enableTestEmbeddedShell(a)
	node := a.state.selectedEntry().node
	var active string
	for i := range 14 {
		kind := terminal.NodeShell
		if i%2 == 1 {
			kind = terminal.NodeHostShell
		}
		active = putTestSession(a.sessions, nodeTargetKey(node.ID), &tuiSession{shellKind: kind}, &metadataTestTerminal{
			fakeTUITerminal: newFakeTUITerminal(), metadata: TerminalMetadata{Title: fmt.Sprintf("task-%02d-long-title", i)},
		})
	}
	a.state.setActiveTab(nodeTargetKey(node.ID), active)
	a.openNodeDetail(tuiDetailShell)
	for _, size := range []vaxis.Resize{{Cols: 150, Rows: 38}, {Cols: 80, Rows: 24}, {Cols: 60, Rows: 14}, {Cols: 40, Rows: 10}, {Cols: 150, Rows: 38}} {
		_, _ = a.handleEvent(size)
		a.draw()
		if size.Cols < 60 {
			continue
		}
		text := renderedScreenText(t, a.vx, size.Cols, size.Rows)
		if !strings.Contains(text, "task-13") || !strings.Contains(text, "F6") {
			t.Fatalf("active tab or focus indication missing at %v:\n%s", size, text)
		}
		cols, rows := a.activeTerminalSize(size.Cols, size.Rows, a.state.focus)
		if a.terminalBodyRect.width != cols || a.terminalBodyRect.height != rows || a.terminalBodyRect.row != 8 {
			t.Fatal("terminal render/resize geometry diverged", size, a.terminalBodyRect, cols, rows)
		}
		if a.state.activeSessionKey() != active {
			t.Fatal("resize selected another terminal")
		}
	}
}

func TestTUIEmbeddedShellEmptyStateKeepsHostAvailable(t *testing.T) {
	a, _ := newTestManager(t, 80, 24)
	enableTestEmbeddedShell(a)
	a.state.nodes[0].Status = NodeStatusStopped
	a.refreshFleet()
	a.openNodeDetail(tuiDetailShell)
	a.draw()
	text := renderedScreenText(t, a.vx, 80, 24)
	if !strings.Contains(text, "No sessions") || !strings.Contains(text, "T host") || !strings.Contains(text, "stopped") {
		t.Fatal("empty embedded view lost actionable state", text)
	}
}

func TestTUIEmbeddedShellDisconnectedControlsDoNotLaunch(t *testing.T) {
	a, manager := newTestManager(t, 100, 30)
	enableTestEmbeddedShell(a)
	a.daemonDisconnected = true
	a.openNodeDetail(tuiDetailShell)
	a.draw()
	for _, rect := range []tuiRect{a.embedded.guest, a.embedded.host} {
		a.handleMouse(vaxis.Mouse{Col: rect.col, Row: rect.row, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress})
		if !strings.Contains(a.status, "disconnected") || len(manager.opened) != 0 {
			t.Fatal("disconnected control attempted launch", a.status, manager.opened)
		}
	}
}

func TestTUIEmbeddedShellMouseKeepsIdentityAndManagerNavigation(t *testing.T) {
	a, manager := newTestManager(t, 150, 38)
	enableTestEmbeddedShell(a)
	node := a.state.selectedEntry().node
	first := putTestSession(a.sessions, nodeTargetKey(node.ID), &tuiSession{shellKind: terminal.NodeShell}, newFakeTUITerminal())
	second := putTestSession(a.sessions, nodeTargetKey(node.ID), &tuiSession{shellKind: terminal.NodeHostShell}, newFakeTUITerminal())
	a.state.setActiveTab(nodeTargetKey(node.ID), first)
	a.openNodeDetail(tuiDetailShell)
	a.draw()
	click := func(rect tuiRect) {
		a.handleMouse(vaxis.Mouse{Col: rect.col, Row: rect.row, Button: vaxis.MouseLeftButton, EventType: vaxis.EventPress})
	}
	secondRect := a.embedded.tabs[1].rect
	click(secondRect)
	if a.state.activeSessionKey() != second || a.state.focus != tuiFocusTree {
		t.Fatal("tab mouse selection changed identity or keyboard ownership")
	}
	a.handleTerminalClosed(tuiTerminalClosedEvent{SessionKey: second})
	click(secondRect)
	if a.state.activeSessionKey() == second || !strings.Contains(a.status, "ended") {
		t.Fatal("stale tab click reopened or redirected the session", a.status)
	}
	a.draw()
	click(a.embedded.guest)
	if manager.opened[nodeTargetKey(node.ID)] != 1 {
		t.Fatal("new guest mouse control did not create one session")
	}
	a.draw()
	click(a.embedded.focus)
	if a.state.focus != tuiFocusTerminal {
		t.Fatal("focus control did not enter shell")
	}
	a.draw()
	click(a.embedded.focus)
	if a.state.focus != tuiFocusTree {
		t.Fatal("focus control did not return to manager")
	}
	for _, target := range a.detailTargets {
		if target.tab == tuiDetailSpec {
			click(target.rect)
			break
		}
	}
	if a.detailTab != tuiDetailSpec {
		t.Fatal("detail tab click did not navigate")
	}
	a.draw()
	click(a.nodeBackRect)
	if a.nodeDetail {
		t.Fatal("breadcrumb click did not return to Nodes")
	}
}

func TestTUIEmbeddedShellRouteRequiresCurrentLiveEvidence(t *testing.T) {
	a, _ := newTestManager(t, 150, 38)
	enableTestEmbeddedShell(a)
	node := a.state.selectedEntry().node
	a.ports = tuiPortsState{loaded: true, snapshot: daemon.PortsSnapshot{Enabled: true, Authorized: true, LastPollAt: time.Now(), Nodes: []daemon.PortNode{{ID: node.ID, Ready: true}}, Rows: []daemon.PortRoute{{NodeID: node.ID, Kind: "http", GuestPort: 5173, Address: "http://qa.localhost:5173", Status: "live"}}}}
	a.openNodeDetail(tuiDetailShell)
	a.draw()
	if text := renderedScreenText(t, a.vx, 150, 38); !strings.Contains(text, ":5173 forwarded") || !strings.Contains(text, "shell.mode: embedded") {
		t.Fatal("missing shell context", text)
	}
	a.ports.snapshot.LastPollAt = time.Now().Add(-time.Minute)
	a.draw()
	if text := renderedScreenText(t, a.vx, 150, 38); strings.Contains(text, ":5173 forwarded") || !strings.Contains(text, "Forwarding unavailable") {
		t.Fatal("stale route advertised as live", text)
	}
	cfg := a.managerSettings()
	cfg.Shell.Mode = "attach"
	a.preferences = &cfg
	a.draw()
	if a.detailTab == tuiDetailShell || a.terminalBodyRect.height != 0 {
		t.Fatal("disabled embedded mode retained terminal surface")
	}
}
