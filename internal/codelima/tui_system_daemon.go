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
		return []string{"Daemon status unavailable · C re-check"}
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
		rows = append(rows, fmt.Sprintf("            %d ports degraded · %d routes lapsed", f.DegradedPorts, f.LapsedRoutes))
	}
	if f.InventoryError != "" {
		rows = append(rows, "            node inventory unavailable · Enter details")
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
		rows = append(rows, fmt.Sprintf("            last %s · %d nodes", r.LastRunAt.Local().Format("15:04:05"), r.LastReclaimedNodes))
	}
	if r.LastError != "" {
		rows = append(rows, "            reclaim failed · Enter details")
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
		message = "No daemon connection · C re-check"
	}
	tuiLine(win, 1, plainLogText(message), style)
	rows := a.daemonOverviewRows()
	for i, row := range visibleSystemLines(rows, &a.systemScroll[tuiSystemDaemon], h-3) {
		rowStyle := t.text
		if strings.Contains(row, "degraded") || strings.Contains(row, "unavailable") || strings.Contains(row, "failed") {
			rowStyle = t.amber
		}
		tuiLine(win, i+2, plainLogText(row), rowStyle)
	}
	s.controls = nil
	col := 0
	for _, action := range [][2]string{{"details", "Enter details"}, {"check", "C re-check"}} {
		text := action[1] + "  "
		if col+len(text) > w {
			break
		}
		button := win.New(col, h-1, len(text), 1)
		tuiLine(button, 0, text, t.mint)
		x, y := button.Origin()
		s.controls = append(s.controls, tuiSystemDaemonControl{tuiRect{col: x, row: y, width: len(text), height: 1}, action[0]})
		col += len(text)
	}
	if len(rows) > h-3 {
		tuiLine(win.New(col, h-1, max(0, w-col), 1), 0, "↑↓ more", t.dim)
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
			if c.action == "details" {
				a.openDaemonDetails()
			} else {
				a.startDaemonOverview(true)
			}
			return true
		}
	}
	return false
}
