package codelima

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/terminal"
	"go.rockorager.dev/vaxis"
)

const (
	tuiOverviewSessions = iota
	tuiOverviewPorts
	tuiOverviewRecent
	tuiOverviewIdentity
	tuiOverviewResources
	tuiOverviewPanelCount
)

var tuiOverviewPanelNames = [...]string{"Sessions", "Ports", "Recent events", "Identity", "Resources"}

type tuiOverviewControl struct {
	key  string
	rect tuiRect
}

type tuiOverviewRow struct {
	id    string
	panel int
	rect  tuiRect
}

// This is presentation state, scoped to one canonical node. Session keys and
// route IDs survive reordering; a missing selected ID is never substituted
// while dispatching an action. Only explicit navigation chooses a replacement.
type tuiOverviewState struct {
	nodeID, session, route string
	panel, rightPanel      int
	scroll                 [tuiOverviewPanelCount]int
	rects                  [tuiOverviewPanelCount]tuiRect
	rows                   []tuiOverviewRow
	controls               []tuiOverviewControl
}

func (a *vaxisTUIApp) overviewVisible() bool {
	return a.view == tuiViewNodes && a.nodeDetail && a.detailTab == tuiDetailOverview && !a.nodeStarting() && !a.nodeLifecycleFailed()
}

func (a *vaxisTUIApp) reconcileOverview(node Node) {
	if a.overview.nodeID != node.ID {
		a.overview = tuiOverviewState{nodeID: node.ID}
	}
	if a.overview.session == "" {
		if keys := a.state.sessions.TargetSessionKeys(terminal.NodeTarget(node.ID).String()); len(keys) > 0 {
			a.overview.session = keys[0]
		}
	}
	if a.overview.route == "" {
		if rows := a.fleetRoutes(node.ID); len(rows) > 0 {
			a.overview.route = rows[0].ID
		}
	}
}

func (a *vaxisTUIApp) moveOverviewSelection(delta int) {
	s := &a.overview
	var ids []string
	var selected *string
	switch s.panel {
	case tuiOverviewSessions:
		ids, selected = a.state.sessions.TargetSessionKeys(terminal.NodeTarget(s.nodeID).String()), &s.session
	case tuiOverviewPorts:
		for _, route := range a.fleetRoutes(s.nodeID) {
			ids = append(ids, route.ID)
		}
		selected = &s.route
	default:
		s.scroll[s.panel] = max(0, s.scroll[s.panel]+delta)
		return
	}
	if len(ids) > 0 {
		*selected = ids[max(0, min(len(ids)-1, slices.Index(ids, *selected)+delta))]
	}
}

func (a *vaxisTUIApp) handleOverviewAction(action string, key vaxis.Key) bool {
	if !a.overviewVisible() || !tuiKeyActivates(key, false) {
		return false
	}
	node := a.state.selectedEntry().node
	if a.overview.nodeID != node.ID {
		a.reconcileOverview(node)
	}
	s := &a.overview
	if action == "overview.previous_panel" || action == "overview.next_panel" {
		delta := 1
		if action == "overview.previous_panel" {
			delta = -1
		}
		s.panel = (s.panel + delta + tuiOverviewPanelCount) % tuiOverviewPanelCount
		return true
	}
	if action == "navigation.up" || action == "navigation.down" {
		delta := 1
		if action == "navigation.up" {
			delta = -1
		}
		a.moveOverviewSelection(delta)
		return true
	}
	if !tuiKeyActivates(key, true) {
		return false
	}
	var err error
	switch {
	case action == "session.guest", action == "session.host":
		err = a.startRawShell(action == "session.host", true, "")
	case action == "overview.open" && s.panel == tuiOverviewSessions:
		var session *tuiSession
		session, err = a.overviewSession()
		if err == nil {
			err = a.startRawShell(session.shellKind == terminal.NodeHostShell, false, string(session.terminalID))
		}
	case action == "session.end" && s.panel == tuiOverviewSessions:
		err = a.confirmOverviewSessionEnd()
	case action == "route.open" && s.panel == tuiOverviewPorts:
		err = a.openOverviewRoute()
	case action == "route.copy" && s.panel == tuiOverviewPorts:
		var route daemon.PortRoute
		route, err = a.overviewRoute()
		if err == nil {
			err = a.copyToHostClipboard(route.Address)
		}
	case action == "overview.open" && s.panel == tuiOverviewPorts:
		err = a.openOverviewRoute()
	case action == "overview.open" && s.panel == tuiOverviewRecent:
		a.openNodeDetail(tuiDetailLogs)
	case action == "overview.open" && s.panel == tuiOverviewIdentity:
		a.openNodeDetail(tuiDetailSpec)
	case action == "overview.open" && s.panel == tuiOverviewResources:
		a.openNodeDetail(tuiDetailMetrics)
	default:
		return false
	}
	a.claimShortcutKey(key)
	if err != nil {
		a.setStatus(slog.LevelError, err.Error())
	}
	return true
}

func (a *vaxisTUIApp) overviewSession() (*tuiSession, error) {
	if a.daemonDisconnected {
		return nil, fmt.Errorf("session actions require a daemon connection")
	}
	if a.sessions != nil {
		if session, ok := a.sessions.Session(a.overview.session); ok && session.target == terminal.NodeTarget(a.state.selectedEntry().node.ID).String() && session.terminalID != "" {
			return session, nil
		}
	}
	return nil, errors.New(a.keyHints("selected session is no longer available; use {navigation.up navigation.down} to select a session"))
}

func (a *vaxisTUIApp) confirmOverviewSessionEnd() error {
	session, err := a.overviewSession()
	if err != nil {
		return err
	}
	key, target, id := session.key, session.target, session.terminalID
	label := terminalTabLabel(session, a.terminalMetadata(key))
	a.overlay = newTUIDialog("End session · "+label, "End session", []string{
		"End this session and its running processes on " + a.state.selectedEntry().node.Slug + ".",
		a.keyHints("Other sessions remain open. {dialog.cancel} cancels."),
	}, nil, func(map[string]string) error {
		current, ok := a.sessions.Session(key)
		if !ok || current.terminalID != id || current.target != target {
			return fmt.Errorf("that session has already ended")
		}
		if a.daemonDisconnected {
			return fmt.Errorf("session actions require a daemon connection")
		}
		keys := a.sessions.TargetSessionKeys(target)
		a.sessions.CloseSession(key)
		if a.state.activeTab(target) == key {
			if next := nextActiveTerminalTabAfterClose(keys, key); next != "" {
				a.state.setActiveTab(target, next)
			} else {
				a.state.clearActiveTab(target)
			}
		}
		a.setStatus(slog.LevelInfo, "End requested · "+label)
		return nil
	})
	return nil
}

func (a *vaxisTUIApp) overviewRoute() (daemon.PortRoute, error) {
	for _, route := range a.fleetRoutes(a.state.selectedEntry().node.ID) {
		if route.ID == a.overview.route {
			return route, nil
		}
	}
	return daemon.PortRoute{}, errors.New(a.keyHints("selected route is no longer available; use {navigation.up navigation.down} to select a route"))
}

func (a *vaxisTUIApp) openOverviewRoute() error {
	route, err := a.overviewRoute()
	if err != nil {
		return err
	}
	record, ok := a.fleetSelected()
	if !ok || !a.fleetRouteOpenable(record, route) {
		return errors.New(a.keyHints("selected HTTP route is unavailable or stale; use {view.ports} Ports to inspect"))
	}
	return a.openHyperlink(route.Address)
}

func (a *vaxisTUIApp) handleOverviewMouse(mouse vaxis.Mouse) bool {
	if !a.overviewVisible() || mouse.EventType != vaxis.EventPress {
		return false
	}
	if mouse.Button == vaxis.MouseLeftButton {
		for _, control := range a.overview.controls {
			if !control.rect.contains(mouse.Col, mouse.Row) {
				continue
			}
			switch control.key {
			case "spec":
				a.openNodeDetail(tuiDetailSpec)
			case "metrics":
				a.openNodeDetail(tuiDetailMetrics)
			case "logs":
				a.openNodeDetail(tuiDetailLogs)
			case "ports":
				selected := a.overview.route
				a.switchView(tuiViewPorts)
				a.ports.query, a.ports.selected = "", selected
			case "overview.open":
				a.overview.panel = tuiOverviewSessions
				a.handleOverviewAction("overview.open", vaxis.Key{})
			default:
				a.overview.panel = tuiOverviewSessions
				if control.key == "route.open" || control.key == "route.copy" {
					a.overview.panel = tuiOverviewPorts
				}
				a.handleOverviewAction(control.key, vaxis.Key{})
			}
			return true
		}
	}
	for panel, rect := range a.overview.rects {
		if !rect.contains(mouse.Col, mouse.Row) {
			continue
		}
		a.overview.panel = panel
		switch mouse.Button {
		case vaxis.MouseWheelUp:
			a.moveOverviewSelection(-1)
		case vaxis.MouseWheelDown:
			a.moveOverviewSelection(1)
		case vaxis.MouseLeftButton:
			for _, row := range a.overview.rows {
				if row.panel == panel && row.rect.contains(mouse.Col, mouse.Row) {
					if panel == tuiOverviewSessions {
						a.overview.session = row.id
					} else {
						a.overview.route = row.id
					}
					break
				}
			}
		}
		return true
	}
	return false
}
