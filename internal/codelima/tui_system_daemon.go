package codelima

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"go.rockorager.dev/vaxis"
)

const tuiDaemonOverviewInterval = 3 * time.Second

type tuiSystemDaemonState struct {
	tuiDaemonWorkState
	overview             daemon.Overview
	clientID             string
	checkedAt, startedAt time.Time
	err                  error
	loading              bool
	generation           uint64
	cancel               context.CancelFunc
	controls             []tuiSystemDaemonControl
}

type tuiSystemDaemonControl struct {
	rect   tuiRect
	action string
}

type tuiDaemonOverviewEvent struct {
	generation uint64
	checkedAt  time.Time
	clientID   string
	overview   daemon.Overview
	err        error
}

func (a *vaxisTUIApp) startDaemonOverview(force bool) {
	if a.view != tuiViewSystem {
		a.cancelDaemonOverview()
		return
	}
	if a.service == nil || a.service.daemonClient == nil || a.daemonDisconnected {
		return
	}
	s := &a.systemDaemon
	if s.loading || (!force && time.Since(s.startedAt) < tuiDaemonOverviewInterval) {
		return
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	s.generation++
	s.loading, s.startedAt, s.cancel = true, time.Now(), cancel
	generation, client := s.generation, a.service.daemonClient
	load := func() tuiDaemonOverviewEvent {
		defer cancel()
		e := tuiDaemonOverviewEvent{generation: generation}
		e.err = client.Call(ctx, "daemon.overview", nil, &e.overview)
		if ctx.Err() != nil {
			e.err = ctx.Err()
		}
		e.clientID, e.checkedAt = client.HelloSnapshot().ClientID, time.Now()
		return e
	}
	if a.postEvent == nil {
		a.applyDaemonOverview(load())
		return
	}
	go func() { a.postCompletion(load()) }()
}

func (a *vaxisTUIApp) cancelDaemonOverview() {
	s := &a.systemDaemon
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.generation++
	s.loading = false
}

func (a *vaxisTUIApp) applyDaemonOverview(e tuiDaemonOverviewEvent) {
	s := &a.systemDaemon
	if e.generation != s.generation {
		return
	}
	s.loading, s.cancel, s.err = false, nil, e.err
	if e.err == nil {
		s.overview, s.clientID, s.checkedAt = e.overview, e.clientID, e.checkedAt
	}
}

func (a *vaxisTUIApp) daemonOverviewRows() []string {
	s := &a.systemDaemon
	if s.checkedAt.IsZero() {
		return []string{a.keyHints("Daemon status unavailable · {system.refresh} re-check")}
	}
	status := s.overview.Status
	state := "unavailable"
	if status.Running {
		state = "running"
	}
	seat := "another client holds seat"
	if status.InputOwner == "" {
		seat = "no seat owner"
	} else if status.InputOwner == s.clientID {
		seat = "this TUI holds seat"
	}
	rows := []string{
		fmt.Sprintf("status      %s · pid %d", state, status.PID),
		fmt.Sprintf("version     %s · protocol %d", status.Version, status.Protocol),
	}
	uptime := "unknown"
	if !status.StartedAt.IsZero() {
		uptime = max(time.Duration(0), time.Since(status.StartedAt)).Round(time.Second).String()
	}
	if runtime := s.overview.Runtime; runtime != nil && !runtime.UpdatedAt.IsZero() {
		uptime += " · updated in place " + runtime.UpdatedAt.Local().Format("15:04:05")
	}
	rows = append(rows, "uptime      "+uptime, fmt.Sprintf("sessions    %d · %d subscribers · %s", status.TerminalCount, status.Subscribers, seat))
	runtime := s.overview.Runtime
	if runtime == nil {
		return append(rows, "runtime summary unavailable")
	}
	f := runtime.Forwarding
	forwarding, telemetry := "off", "off"
	if f.Enabled {
		forwarding = fmt.Sprintf("%d listeners · %d ports", f.Listeners, f.Ports)
		telemetry = fmt.Sprintf("%ds · CPU, memory, disk over SSH", f.IntervalSeconds)
		if !f.Authorized {
			telemetry = "waiting for authorization"
		}
	}
	rows = append(rows, "forwarding  "+forwarding)
	if f.DegradedPorts > 0 || f.LapsedRoutes > 0 {
		rows = append(rows, fmt.Sprintf("            %s degraded · %s lapsed", countLabel(f.DegradedPorts, "port", "ports"), countLabel(f.LapsedRoutes, "route", "routes")))
	}
	if f.InventoryError != "" {
		rows = append(rows, a.keyHints("            node inventory unavailable · {system.open} details"))
	}
	rows = append(rows, "telemetry   "+telemetry)
	r := runtime.Reclaim
	reclaim := "off"
	switch {
	case !r.Supported:
		reclaim = "unsupported on host"
	case r.Enabled && r.IntervalSeconds > 0:
		reclaim = fmt.Sprintf("reclaim on · every %ds", r.IntervalSeconds)
	case r.Enabled:
		reclaim = "configured on · ticker unavailable"
	}
	rows = append(rows, "virtiofs    "+reclaim)
	if !r.LastRunAt.IsZero() {
		rows = append(rows, fmt.Sprintf("            last %s · %s", r.LastRunAt.Local().Format("15:04:05"), countLabel(r.LastReclaimedNodes, "node", "nodes")))
	}
	if r.LastError != "" {
		rows = append(rows, a.keyHints("            reclaim failed · {system.open} details"))
	}
	return rows
}

func (a *vaxisTUIApp) drawSystemDaemon(win vaxis.Window, t tuiTheme) {
	s := &a.systemDaemon
	w, h := win.Size()
	tuiLine(win, 0, "Daemon · sessions, forwarding, telemetry", t.muted)
	message, style := "Waiting for daemon status…", t.dim
	if !s.checkedAt.IsZero() {
		message = "Checked " + s.checkedAt.Local().Format("15:04:05")
	}
	switch {
	case a.daemonDisconnected:
		message, style = "Disconnected · showing stale observations", t.amber
	case s.err != nil:
		message, style = "Refresh failed · stale: "+s.err.Error(), t.coral
	case s.loading:
		message += " · refreshing…"
	case s.checkedAt.IsZero() && (a.service == nil || a.service.daemonClient == nil):
		message = a.keyHints("No daemon connection · {system.refresh} re-check")
	}
	if s.lifecycle != "" {
		message, style = "Daemon "+s.lifecycle+a.keyHints(" running… · {system.logs} log · {system.diagnose} diagnose"), t.amber
	}
	busy := s.lifecycle != "" || (s.loading && !a.daemonDisconnected && s.err == nil)
	if busy {
		message = "  " + message
	}
	tuiLine(win, 1, plainLogText(message), style)
	if busy {
		a.drawSpinner(win.New(0, 1, 1, 1), t.amber)
	}
	type button struct {
		action, text string
		col, row     int
	}
	var buttons []button
	col, controlRow := 0, 0
	for _, action := range [][2]string{{"update", "{system.update} update, keep sessions"}, {"restart", "{system.restart} restart"}, {"log", "{system.logs} daemon log"}, {"diagnose", "{system.diagnose} diagnose freeze"}, {"details", "{system.open} details"}, {"check", "{system.refresh} re-check"}} {
		action[1] = a.keyHints(action[1])
		if col > 0 && col+len(action[1]) > w {
			col, controlRow = 0, controlRow+1
		}
		buttons = append(buttons, button{action[0], action[1], col, controlRow})
		col += len(action[1]) + 2
	}
	controlStart := max(2, h-controlRow-1)
	rows := a.daemonOverviewRows()
	for i, row := range visibleSystemLines(rows, &a.systemScroll[tuiSystemDaemon], controlStart-2) {
		rowStyle := t.text
		if strings.Contains(row, "degraded") || strings.Contains(row, "unavailable") || strings.Contains(row, "failed") {
			rowStyle = t.amber
		}
		tuiLine(win, i+2, plainLogText(row), rowStyle)
	}
	s.controls = nil
	for _, b := range buttons {
		if controlStart+b.row >= h {
			break
		}
		button := win.New(b.col, controlStart+b.row, min(w-b.col, len(b.text)), 1)
		style := t.mint
		if s.lifecycle != "" && (b.action == "update" || b.action == "restart") {
			style = t.dim
		}
		tuiLine(button, 0, b.text, style)
		x, y := button.Origin()
		s.controls = append(s.controls, tuiSystemDaemonControl{tuiRect{col: x, row: y, width: min(w-b.col, len(b.text)), height: 1}, b.action})
	}
	if len(rows) > controlStart-2 {
		tuiLine(win.New(col, h-1, max(0, w-col), 1), 0, a.keyHints("{navigation.up navigation.down} more"), t.dim)
	}
}

func (a *vaxisTUIApp) openDaemonDetails() {
	s := &a.systemDaemon
	lines := []string{"Observed " + s.checkedAt.Local().Format(time.RFC3339)}
	if s.checkedAt.IsZero() {
		lines = []string{"No successful status check"}
	}
	if a.daemonDisconnected {
		lines = append(lines, "Disconnected · observations are stale")
	}
	if s.err != nil {
		lines = append(lines, "Refresh failed: "+s.err.Error())
	}
	lines = append(lines, "")
	lines = append(lines, a.daemonOverviewRows()...)
	if runtime := s.overview.Runtime; runtime != nil {
		f, r := runtime.Forwarding, runtime.Reclaim
		lines = append(lines, "", "Forwarding last poll: "+observedDaemonTime(f.LastPollAt))
		if f.InventoryError != "" {
			lines = append(lines, "Node inventory error: "+f.InventoryError)
		}
		if f.LastError != "" {
			lines = append(lines, "Last forwarding error: "+f.LastError)
		}
		if r.LastError != "" {
			lines = append(lines, "Last reclaim error: "+r.LastError)
		}
		if !r.NextRunAt.IsZero() {
			lines = append(lines, "Next reclaim: "+observedDaemonTime(r.NextRunAt))
		}
	}
	lines = append(lines, "", "Subscribers count unique event clients, including other observers.", "Overview reads cached state; it does not probe guests or render terminals.")
	a.overlay = &tuiTextView{title: "Daemon status", text: strings.Join(lines, "\n")}
}

func observedDaemonTime(t time.Time) string {
	if t.IsZero() {
		return "not observed"
	}
	return t.Local().Format(time.RFC3339)
}

func (a *vaxisTUIApp) handleSystemDaemonMouse(mouse vaxis.Mouse) bool {
	if a.view != tuiViewSystem || mouse.EventType != vaxis.EventPress || mouse.Button != vaxis.MouseLeftButton {
		return false
	}
	for _, c := range a.systemDaemon.controls {
		if c.rect.contains(mouse.Col, mouse.Row) {
			a.systemPanel = tuiSystemDaemon
			a.runSystemDaemonAction(c.action)
			return true
		}
	}
	return false
}
