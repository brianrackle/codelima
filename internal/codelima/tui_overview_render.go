package codelima

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/terminal"
	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
)

func (a *vaxisTUIApp) drawNodeOverview(win vaxis.Window, record tuiFleetRecord, theme tuiTheme) {
	w, h := win.Size()
	a.reconcileOverview(record.Node)
	s := &a.overview
	s.rects, s.rows, s.controls = [tuiOverviewPanelCount]tuiRect{}, nil, nil
	if w < 44 || h < 8 {
		tuiLine(win, 0, "Overview · enlarge the terminal", theme.muted)
		return
	}
	if s.panel <= tuiOverviewRecent {
		s.rightPanel = s.panel
	}
	left := min(66, w/2+3)
	if w >= 116 && h >= 25 {
		identityHeight := 17
		a.drawOverviewPanel(win.New(0, 0, left, identityHeight), tuiOverviewIdentity, record, theme)
		a.drawOverviewPanel(win.New(0, identityHeight+1, left, h-identityHeight-2), tuiOverviewResources, record, theme)
		right := win.New(left+1, 0, w-left-1, h-1)
		a.drawOverviewPanel(right.New(0, 0, w-left-1, 9), tuiOverviewSessions, record, theme)
		a.drawOverviewPanel(right.New(0, 10, w-left-1, 7), tuiOverviewPorts, record, theme)
		a.drawOverviewPanel(right.New(0, 18, w-left-1, h-19), tuiOverviewRecent, record, theme)
	} else {
		panel := tuiOverviewIdentity
		if s.panel == tuiOverviewResources {
			panel = tuiOverviewResources
		}
		a.drawOverviewPanel(win.New(0, 0, left, h-1), panel, record, theme)
		a.drawOverviewPanel(win.New(left+1, 0, w-left-1, h-1), s.rightPanel, record, theme)
	}
	hint := "focus: " + tuiOverviewPanelNames[s.panel] + a.keyHints(" · {overview.previous_panel overview.next_panel} panels · {navigation.up navigation.down} rows · {navigation.next_tab} detail tabs")
	tuiLine(win, h-1, hint, theme.dim)
}

func (a *vaxisTUIApp) overviewControl(win vaxis.Window, col, row int, label, key string, theme tuiTheme) {
	w, h := win.Size()
	if row < 0 || row >= h || col < 0 || col >= w {
		return
	}
	if action := map[string]string{"t": "session.guest", "T": "session.host", "K": "session.end", "o": "route.open", "y": "route.copy", "attach": "overview.open"}[key]; action != "" {
		if key != "attach" {
			label = strings.Replace(label, key+" ", a.keyHint(action)+" ", 1)
		}
		key = action
	}
	width := min(w-col, renderedTextWidth(a.vx, label))
	tuiLine(win.New(col, row, width, 1), 0, label, theme.muted)
	x, y := win.Origin()
	a.overview.controls = append(a.overview.controls, tuiOverviewControl{key: key, rect: tuiRect{col: x + col, row: y + row, width: width, height: 1}})
}

func (a *vaxisTUIApp) drawOverviewPanel(win vaxis.Window, panel int, record tuiFleetRecord, theme tuiTheme) {
	w, h := win.Size()
	x, y := win.Origin()
	a.overview.rects[panel] = tuiRect{col: x, row: y, width: w, height: h}
	style := theme.border
	titleStyle := theme.text
	if a.overview.panel == panel {
		style = theme.mint
		titleStyle = theme.mint
	}
	box := border.All(win, style)
	title := " " + tuiOverviewPanelNames[panel] + " "
	if panel == tuiOverviewSessions {
		title = fmt.Sprintf(" Sessions · %d ", len(a.fleetSessions(record.Node)))
	}
	if panel == tuiOverviewPorts {
		title = fmt.Sprintf(" Ports · %d ", len(a.fleetRoutes(record.Node.ID)))
		if !a.fleetPortsCurrent() {
			title = " Ports · stale / unavailable "
		}
	}
	if panel == tuiOverviewRecent && a.fleetEvidenceStale() {
		title += "· stale "
	}
	tuiLine(win.New(2, 0, w-4, 1), 0, title, titleStyle)
	innerW, innerH := box.Size()
	content := box.New(1, 0, max(0, innerW-2), innerH)
	switch panel {
	case tuiOverviewIdentity:
		a.drawOverviewIdentity(content, record, theme)
		a.overviewControl(win, max(2, w-10), h-1, " Spec → ", "spec", theme)
	case tuiOverviewResources:
		a.drawOverviewResources(content, record, theme)
		a.overviewControl(win, max(2, w-13), h-1, " Metrics → ", "metrics", theme)
	case tuiOverviewSessions:
		a.drawOverviewSessions(content, record.Node, theme)
	case tuiOverviewPorts:
		a.drawOverviewPorts(content, record.Node.ID, theme)
		a.overviewControl(win, max(2, w-16), h-1, " all ports → ", "ports", theme)
	case tuiOverviewRecent:
		a.drawOverviewRecent(content, record.Node.ID, theme)
		label := a.keyHints(" {node.logs} all events → ")
		a.overviewControl(win, max(2, w-renderedTextWidth(a.vx, label)-1), h-1, label, "logs", theme)
	}
}

func (a *vaxisTUIApp) drawOverviewIdentity(win vaxis.Window, record tuiFleetRecord, theme tuiTheme) {
	n := record.Node
	w, h := win.Size()
	statusCell, style := a.fleetCell(record, tuiFleetSortStatus, false, w, theme)
	status := statusCell.String()
	if !record.StartedAt.IsZero() {
		status += " · since " + record.StartedAt.Local().Format("Jan 2 15:04")
	}
	created := "—"
	if !n.CreatedAt.IsZero() {
		created = n.CreatedAt.Local().Format("Jan 2 15:04")
	}
	authItems := "—"
	if p := a.fleetPreviewFor(n); p != nil && p.AuthImport != nil {
		authItems = strings.Join(p.AuthImport.Categories, ", ")
		if authItems == "" {
			authItems = "none"
		}
	}
	var clones []string
	for _, node := range a.state.nodes {
		if node.ParentNodeID == n.ID {
			clones = append(clones, node.Slug+" · "+string(node.Status))
		}
	}
	cloneLabel := "none in scope"
	if len(clones) > 0 {
		cloneLabel = strings.Join(clones, ", ")
	}
	agents, note := a.fleetAgentCheckSegments(n, theme)
	template := n.ConfigurationSlug + " · frozen " + created
	if a.spec.nodeID == n.ID && a.spec.err == nil && len(a.spec.snapshot.Differences) > 0 {
		template += " · changed →"
	}
	fields := [][2]string{
		{"status", status}, {"template", template},
		{"image", n.Image}, {"directory", n.DirectoryPath}, {"workspace", a.fleetWorkspaceLabel(n)},
		{"lima", n.SandboxName}, {"runtime", a.fleetRuntimeLabel(n)}, {"guest", a.fleetGuestPlatformLabel(n)}, {"agents", ""}, {"checks", note},
		{"host auth", a.fleetAuthLabel(n)}, {"items", authItems}, {"clones", cloneLabel}, {"created", created}, {"id", n.ID},
	}
	// Agent pairs wrap as a unit. Render rows against the parent clip so the
	// focused Identity panel can scroll without moving neighboring panels.
	offset := min(a.overview.scroll[tuiOverviewIdentity], max(0, len(fields)+2-h))
	a.overview.scroll[tuiOverviewIdentity] = offset
	virtual := win.New(0, -offset, w, h+offset)
	row := 0
	for i, field := range fields {
		tuiLine(virtual.New(0, row, 10, 1), 0, field[0], theme.dim)
		if field[0] == "agents" {
			row += a.drawFleetAgentChecks(virtual.New(10, row, max(0, w-10), max(1, 20-row)), agents)
			continue
		}
		valueStyle := theme.text
		if i == 0 {
			valueStyle = style
		} else if i == 1 {
			valueStyle = theme.violet
		}
		tuiLine(virtual.New(10, row, max(0, w-10), 1), 0, field[1], valueStyle)
		row++
	}
}

func (a *vaxisTUIApp) drawOverviewResources(win vaxis.Window, record tuiFleetRecord, theme tuiTheme) {
	w, h := win.Size()
	now := time.Now()
	var points []daemon.NodeMetricPoint
	if p := a.fleetPreviewFor(record.Node); p != nil {
		points = p.History
	}
	if len(points) == 0 && record.Node.LastRuntimeObservation != nil {
		points = record.Node.LastRuntimeObservation.CPUHistory
	}
	points = slices.DeleteFunc(slices.Clone(points), func(p daemon.NodeMetricPoint) bool {
		return p.SampledAt.Before(now.Add(-time.Minute)) || p.SampledAt.After(now)
	})
	stats, _, _ := strings.Cut(metricStatistics(points, metricCPU), " at ")
	cpu := "—"
	if v, ok := tuiFleetMetric(record, tuiFleetSortCPU); ok {
		cpu = fmt.Sprintf("%.0f%%", v)
	}
	label := "Observed usage · refresh 1s"
	o := record.Node.LastRuntimeObservation
	if a.daemonDisconnected || a.fleetEvidenceStale() || o == nil || o.Status != ObservationRunning || o.ResourceUsageSampledAt == nil || now.Sub(*o.ResourceUsageSampledAt) > 5*time.Second {
		label = "Last observed usage / unavailable"
	}
	lines := []tuiGaugeLine{{prefix: label}, {prefix: fmt.Sprintf("CPU    %d vCPU · %s", record.Node.VCPUs, cpu)}, {prefix: tuiCPUSparkline(points, now, time.Minute, 10) + " · " + stats + " · 1m"}}
	for _, resource := range []struct {
		name       string
		configured uint32
		field      tuiFleetSort
	}{{"memory", record.Node.MemoryMiB, tuiFleetSortMemory}, {"disk", record.Node.DiskMiB, tuiFleetSortDisk}} {
		line := tuiGaugeLine{suffix: "—"}
		var used, total *uint64
		if o != nil {
			used, total = o.MemoryUsedBytes, o.MemoryTotalBytes
			if resource.field == tuiFleetSortDisk {
				used, total = o.DiskUsedBytes, o.DiskTotalBytes
			}
			if percent, ok := tuiFleetUsagePercent(used, total); ok && *used <= *total {
				line = tuiGaugeLine{percent: percent, cells: 10, suffix: " " + tuiGiB(*used) + " / " + tuiGiB(*total) + " GiB"}
			}
		}
		lines = append(lines, tuiGaugeLine{prefix: fmt.Sprintf("%-6s %s configured", resource.name, formatTUIResourceSize(resource.configured))}, line)
		if resource.field == tuiFleetSortMemory && used != nil && total != nil && *used <= *total && *total > 0 {
			lines[len(lines)-1].suffix += " · " + tuiGiB(*total-*used) + " available"
		}
		if resource.field == tuiFleetSortDisk {
			lines[len(lines)-1].suffix += " root fs"
		}
	}
	if nodeWorkspaceMode(record.Node) == "mounted" {
		lines = append(lines, tuiGaugeLine{prefix: "Workspace on host; excluded from root fs"})
	}
	offset := max(0, min(a.overview.scroll[tuiOverviewResources], len(lines)-h))
	a.overview.scroll[tuiOverviewResources] = offset
	for i, line := range lines[offset:min(len(lines), offset+h)] {
		style := theme.text
		switch i + offset {
		case 0:
			style = theme.dim
		case 2, 6:
			style = theme.mint
		case 4:
			style = theme.blue
		}
		line.draw(win.New(0, i, w, 1), 0, style, theme)
	}
}

func overviewAge(created, now time.Time) string {
	if created.IsZero() || created.After(now) {
		return "—"
	}
	age := now.Sub(created)
	if age < time.Minute {
		return fmt.Sprintf("%ds", int(age.Seconds()))
	}
	if age < time.Hour {
		return fmt.Sprintf("%dm", int(age.Minutes()))
	}
	return fmt.Sprintf("%dh%02dm", int(age.Hours()), int(age.Minutes())%60)
}

func (a *vaxisTUIApp) overviewStart(panel, count, index, visible int) int {
	offset := a.overview.scroll[panel]
	if index >= 0 {
		offset = max(min(offset, index), index-visible+1)
	}
	offset = max(0, min(offset, count-visible))
	a.overview.scroll[panel] = offset
	return offset
}

func (a *vaxisTUIApp) drawOverviewSessions(win vaxis.Window, node Node, theme tuiTheme) {
	w, h := win.Size()
	rows := a.fleetSessions(node)
	index := slices.IndexFunc(rows, func(r tuiFleetSession) bool { return r.key == a.overview.session })
	heading, headingStyle := "Daemon-owned · survive the TUI", theme.dim
	if len(rows) > 0 && index < 0 {
		heading, headingStyle = a.keyHints("Session ended · {navigation.up navigation.down} to select"), theme.amber
	}
	tuiLine(win, 0, heading, headingStyle)
	compact := w < 58
	rowHeight, footer := 1, 1
	if compact {
		rowHeight, footer = 2, 2
	}
	visible := max(1, (h-2-footer)/rowHeight)
	start := a.overviewStart(tuiOverviewSessions, len(rows), index, visible)
	for i := start; i < min(len(rows), start+visible); i++ {
		r := rows[i]
		y := 1 + (i-start)*rowHeight
		row := win.New(0, y, w, rowHeight)
		style := theme.text
		cursor := " "
		if r.key == a.overview.session {
			style, cursor = theme.selected, "▸"
			tuiFill(row, style)
		}
		kind := "?"
		if r.kindKnown {
			kind = "shell"
			if r.kind == terminal.NodeHostShell {
				kind = "host"
			}
		}
		activity := r.activity
		if r.idleSince > 0 {
			activity = fleetIdleLabel(r.idleSince, time.Now())
		}
		age := overviewAge(r.createdAt, time.Now())
		if compact {
			tuiLine(row, 0, fmt.Sprintf("%s%d %-5s %s", cursor, i+1, kind, r.label), style)
			tuiLine(row, 1, "   "+activity+" · "+age, style)
		} else {
			tuiLine(row.New(0, 0, max(0, w-22), 1), 0, fmt.Sprintf("%s%d %-5s %s", cursor, i+1, kind, r.label), style)
			tuiLine(row.New(max(0, w-21), 0, 14, 1), 0, activity, style)
			tuiLine(row.New(max(0, w-7), 0, 7, 1), 0, age, style)
		}
		x, top := row.Origin()
		a.overview.rows = append(a.overview.rows, tuiOverviewRow{id: r.key, panel: tuiOverviewSessions, rect: tuiRect{col: x, row: top, width: w, height: rowHeight}})
	}
	if len(rows) == 0 {
		message := "No sessions · t creates a shell"
		if !nodeGuestShellReady(node) {
			message = "No sessions · T opens host shell"
		}
		tuiLine(win, 2, message, theme.dim)
	}
	a.overviewControl(win, 0, h-footer, a.keyHints("{overview.open} attach"), "attach", theme)
	a.overviewControl(win, 14, h-footer, "t new shell", "t", theme)
	if compact {
		a.overviewControl(win, 0, h-1, "T host shell", "T", theme)
		a.overviewControl(win, 14, h-1, "K end", "K", theme)
	} else {
		a.overviewControl(win, 28, h-1, "T host shell", "T", theme)
		a.overviewControl(win, 44, h-1, "K end session", "K", theme)
	}
}

func (a *vaxisTUIApp) drawOverviewPorts(win vaxis.Window, nodeID string, theme tuiTheme) {
	w, h := win.Size()
	rows := a.fleetRoutes(nodeID)
	rowHeight := 1
	if w < 58 {
		rowHeight = 2
	}
	visible := max(1, (h-1)/rowHeight)
	index := slices.IndexFunc(rows, func(r daemon.PortRoute) bool { return r.ID == a.overview.route })
	start := a.overviewStart(tuiOverviewPorts, len(rows), index, visible)
	for i := start; i < min(len(rows), start+visible); i++ {
		r := rows[i]
		row := win.New(0, (i-start)*rowHeight, w, rowHeight)
		style := theme.blue
		cursor := " "
		if r.ID == a.overview.route {
			cursor, style = "▸", theme.selected
			tuiFill(row, style)
		}
		text := fmt.Sprintf("%s%d %s", cursor, r.Port, r.Address)
		if rowHeight == 1 {
			tuiLine(row.New(0, 0, max(0, w-18), 1), 0, text, style)
			tuiLine(row.New(max(0, w-17), 0, 17, 1), 0, r.Process+" · "+r.Status, style)
		} else {
			tuiLine(row, 0, text, style)
			tuiLine(row, 1, "  "+r.Process+" · "+r.Status, style)
		}
		x, y := row.Origin()
		a.overview.rows = append(a.overview.rows, tuiOverviewRow{id: r.ID, panel: tuiOverviewPorts, rect: tuiRect{col: x, row: y, width: w, height: rowHeight}})
	}
	if len(rows) == 0 {
		tuiLine(win, 0, "No observed routes", theme.dim)
	}
	a.overviewControl(win, 0, h-1, "o open", "o", theme)
	a.overviewControl(win, 10, h-1, "y copy URL", "y", theme)
}

func (a *vaxisTUIApp) drawOverviewRecent(win vaxis.Window, nodeID string, theme tuiTheme) {
	_, h := win.Size()
	s := a.fleetEvidence
	if !s.loaded || s.snapshot.RecentNode != nodeID {
		tuiLine(win, 0, "Recent events unavailable", theme.dim)
		return
	}
	var lines []string
	if s.snapshot.RecentError != "" {
		lines = append(lines, "History incomplete · l to inspect")
	}
	for _, record := range s.snapshot.Recent {
		if record.NodeID == nodeID {
			lines = append(lines, record.Time.Local().Format("15:04:05")+" "+record.Text)
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "No retained events")
	}
	offset := max(0, min(a.overview.scroll[tuiOverviewRecent], len(lines)-h))
	a.overview.scroll[tuiOverviewRecent] = offset
	for i, line := range lines[offset:min(len(lines), offset+h)] {
		tuiLine(win, i, line, theme.muted)
	}
}
