package codelima

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
)

type tuiDetailTarget struct {
	rect tuiRect
	tab  tuiDetailTab
}

type tuiEmbeddedTabTarget struct {
	rect            tuiRect
	key, terminalID string
}

type tuiEmbeddedShellState struct {
	nodeID                             string
	first                              int
	tabs                               []tuiEmbeddedTabTarget
	previous, next, guest, host, focus tuiRect
}

func (s *tuiEmbeddedShellState) clearTargets() {
	s.tabs = nil
	s.previous, s.next, s.guest, s.host, s.focus = tuiRect{}, tuiRect{}, tuiRect{}, tuiRect{}, tuiRect{}
}

func (a *vaxisTUIApp) embeddedShellEnabled() bool {
	return a.managerSettings().Shell.Mode == "embedded"
}

func (a *vaxisTUIApp) normalizeEmbeddedShellMode() {
	if a.embeddedShellEnabled() || a.detailTab != tuiDetailShell && a.state.focus != tuiFocusTerminal {
		return
	}
	a.state.focusTree()
	a.state.treePaneMode = tuiTreePaneModeInfo
	a.detailTab = tuiDetailOverview
}

func (a *vaxisTUIApp) drawEmbeddedIdentity(win vaxis.Window, record tuiFleetRecord, t tuiTheme) {
	glyph, status := t.status(record.Status)
	detail := ""
	if !record.StartedAt.IsZero() {
		detail = " · up " + max(time.Duration(0), time.Since(record.StartedAt)).Truncate(time.Second).String()
	}
	win.PrintTruncate(0,
		vaxis.Segment{Text: "‹ Nodes  ", Style: t.dim},
		vaxis.Segment{Text: record.Node.Slug + "  ", Style: t.text},
		vaxis.Segment{Text: glyph + " " + record.Status, Style: status},
		vaxis.Segment{Text: detail + " · ", Style: t.dim},
		vaxis.Segment{Text: record.Node.ConfigurationSlug, Style: t.violet},
		vaxis.Segment{Text: " · " + plainLogText(record.Node.DirectoryPath), Style: t.dim},
	)
}

func (a *vaxisTUIApp) drawEmbeddedShell(win vaxis.Window, record tuiFleetRecord, t tuiTheme) {
	w, h := win.Size()
	if w < 24 || h < 6 {
		return
	}
	if a.embedded.nodeID != record.Node.ID {
		a.embedded = tuiEmbeddedShellState{nodeID: record.Node.ID}
	}
	a.drawEmbeddedTabs(win.New(0, 0, w, 2), record.Node, t)
	frame := win.New(0, 2, w, h-3)
	accent := t.border
	if a.state.focus == tuiFocusTerminal {
		accent = t.mint
	}
	if a.hostTerminalTabActive() {
		accent = t.coral
	}
	surface := border.All(frame, accent)
	col, row := surface.Origin()
	cols, rows := surface.Size()
	a.terminalBodyRect = tuiRect{col: col, row: row, width: cols, height: rows}
	if a.sessions != nil {
		a.sessions.SetPreferredTerminalSize(cols, rows)
	}
	if _, ok := a.sessions.SessionTerminal(a.state.activeSessionKey()); ok {
		a.drawTerminalSurface(surface, tuiTreeEntry{node: record.Node}, t.mint, t.muted, t.coral)
	} else {
		tuiLine(surface, 0, a.keyHints("No sessions · {session.guest} guest · {session.host} host"), t.muted)
		if err := a.sessions.SessionError(terminal.NodeTarget(record.Node.ID).String()); err != nil {
			tuiLine(surface, 1, plainLogText(err.Error()), t.coral)
		} else if !nodeGuestShellReady(record.Node) || record.Status != "running" {
			tuiLine(surface, 1, "Guest unavailable · VM "+record.Status+" · setup "+record.Setup+a.keyHints(" · {session.host} opens a host shell"), t.dim)
		}
	}
	focus := a.keyHints(" TUI focus · {terminal.focus} enters ")
	if a.state.focus == tuiFocusTerminal {
		focus = a.keyHints(" terminal focus · {terminal.focus} returns ")
	}
	focusWidth := min(w-4, renderedTextWidth(a.vx, focus))
	focusWin := frame.New(w-focusWidth-2, 0, focusWidth, 1)
	tuiLine(focusWin, 0, focus, t.dim)
	col, row = focusWin.Origin()
	a.embedded.focus = tuiRect{col: col, row: row, width: focusWidth, height: 1}
	a.drawEmbeddedRoute(win.New(1, h-1, w-2, 1), record, t)
}

func (a *vaxisTUIApp) drawEmbeddedTabs(win vaxis.Window, node Node, t tuiTheme) {
	w, _ := win.Size()
	s := &a.embedded
	keys := a.sessions.TargetSessionKeys(terminal.NodeTarget(node.ID).String())
	active := a.state.activeSessionKey()
	labels, widths := make([]string, len(keys)), make([]int, len(keys))
	guestLabel, hostLabel := a.keyHints("{session.guest} guest"), a.keyHints("{session.host} host")
	guestWidth := min(max(1, w/4), renderedTextWidth(a.vx, guestLabel)+1)
	hostWidth := min(max(1, w/4), renderedTextWidth(a.vx, hostLabel)+1)
	controlsWidth := guestWidth + hostWidth + 10
	available := max(1, w-controlsWidth)
	for i, key := range keys {
		labels[i], _ = a.terminalTabLabelForKey(key)
		labels[i] = strings.Replace(labels[i], "host:", "host: ", 1)
		widths[i] = min(available, min(38, renderedTextWidth(a.vx, labels[i])+2))
	}
	index := slices.Index(keys, active)
	s.first = min(max(0, s.first), max(0, len(keys)-1))
	if index >= 0 {
		s.first = min(s.first, index)
		used := 0
		for i := s.first; i <= index; i++ {
			used += widths[i] + 1
		}
		for used > available+1 && s.first < index {
			used -= widths[s.first] + 1
			s.first++
		}
	}
	col := 3
	last := s.first
	for i := s.first; i < len(keys); i++ {
		cells := widths[i]
		if col+cells > 3+available {
			break
		}
		session, ok := a.sessions.Session(keys[i])
		if !ok {
			continue
		}
		style, accent := t.muted, t.border
		if keys[i] == active {
			style, accent = t.selected, t.mint
			style.Attribute |= vaxis.AttrBold
		}
		if session.shellKind == terminal.NodeHostShell {
			accent = t.coral
		}
		tuiLine(win.New(col, 0, cells, 1), 0, strings.Repeat("─", cells), accent)
		labelWin := win.New(col, 1, cells, 1)
		tuiFill(labelWin, style)
		tuiLine(labelWin, 0, " "+labels[i]+" ", style)
		x, y := win.New(col, 0, cells, 2).Origin()
		s.tabs = append(s.tabs, tuiEmbeddedTabTarget{rect: tuiRect{col: x, row: y, width: cells, height: 2}, key: keys[i], terminalID: string(session.terminalID)})
		col += cells + 1
		last = i + 1
	}
	control := func(x, width int, label string, style vaxis.Style) tuiRect {
		part := win.New(x, 1, width, 1)
		tuiLine(part, 0, label, style)
		col, row := part.Origin()
		return tuiRect{col: col, row: row, width: width, height: 1}
	}
	if s.first > 0 {
		s.previous = control(0, 2, "‹", t.mint)
	}
	if last < len(keys) {
		s.next = control(w-guestWidth-hostWidth-6, 2, "›", t.mint)
	}
	guest, host := t.mint, t.coral
	if !nodeGuestShellReady(node) || a.fleetStatus(node) != "running" || a.daemonDisconnected {
		guest = t.dim
	}
	if a.daemonDisconnected {
		host = t.dim
	}
	s.guest = control(w-guestWidth-hostWidth-3, guestWidth, guestLabel, guest)
	s.host = control(w-hostWidth-1, hostWidth, hostLabel, host)
}

func (a *vaxisTUIApp) drawEmbeddedRoute(win vaxis.Window, record tuiFleetRecord, t tuiTheme) {
	w, _ := win.Size()
	label, style := a.keyHints("No live forwarded ports · {view.ports} Ports"), t.dim
	if !a.fleetPortsCurrent() {
		label = a.keyHints("Forwarding unavailable / stale · {view.ports} Ports")
	} else {
		for _, route := range a.fleetRoutes(record.Node.ID) {
			if a.fleetRouteOpenable(record, route) {
				label, style = fmt.Sprintf("i :%d forwarded → %s", route.GuestPort, route.Address), t.blue
				break
			}
		}
	}
	if w >= 100 {
		const renderer = "libghostty · one worker per tab"
		cells := len(renderer)
		tuiLine(win.New(w-cells, 0, cells, 1), 0, renderer, t.dim)
		w -= cells + 2
	}
	tuiLine(win.New(0, 0, w, 1), 0, label, style)
}

func (a *vaxisTUIApp) handleDetailNavigationMouse(mouse vaxis.Mouse) bool {
	if !a.nodeDetail || mouse.EventType != vaxis.EventPress || mouse.Button != vaxis.MouseLeftButton {
		return false
	}
	if a.detailNodeID != a.state.selectedEntry().node.ID {
		return false
	}
	if a.nodeBackRect.contains(mouse.Col, mouse.Row) {
		a.switchView(tuiViewNodes)
		return true
	}
	for _, target := range a.detailTargets {
		if target.rect.contains(mouse.Col, mouse.Row) {
			a.openNodeDetail(target.tab)
			a.syncSessionFocus()
			return true
		}
	}
	return false
}

func (a *vaxisTUIApp) handleEmbeddedShellMouse(mouse vaxis.Mouse) bool {
	if !a.nodeDetail || a.detailTab != tuiDetailShell || !a.embeddedShellEnabled() || mouse.EventType != vaxis.EventPress {
		return false
	}
	s := &a.embedded
	if s.nodeID != a.state.selectedEntry().node.ID {
		return false
	}
	var err error
	handled := false
	if mouse.Button == vaxis.MouseLeftButton {
		for _, target := range s.tabs {
			if !target.rect.contains(mouse.Col, mouse.Row) {
				continue
			}
			handled = true
			session, ok := a.sessions.Session(target.key)
			if !ok || session.target != terminal.NodeTarget(s.nodeID).String() || string(session.terminalID) != target.terminalID {
				err = fmt.Errorf("that session has ended")
			} else {
				a.cancelTerminalMouseGesture()
				a.state.setActiveTab(terminal.NodeTarget(s.nodeID).String(), target.key)
			}
			break
		}
		if !handled {
			handled = true
			switch {
			case s.previous.contains(mouse.Col, mouse.Row):
				err = a.switchTerminalTab(-1)
			case s.next.contains(mouse.Col, mouse.Row):
				err = a.switchTerminalTab(1)
			case s.guest.contains(mouse.Col, mouse.Row):
				err = a.openTerminalTab()
			case s.host.contains(mouse.Col, mouse.Row):
				err = a.openHostTerminalTab()
			case s.focus.contains(mouse.Col, mouse.Row):
				if a.state.focus == tuiFocusTerminal {
					a.state.focusTree()
				} else {
					err = a.openEmbeddedShell(false)
				}
			default:
				handled = false
			}
		}
	}
	if handled {
		if err != nil {
			a.setStatus(slog.LevelError, err.Error())
		}
		a.syncSessionFocus()
	}
	return handled
}
