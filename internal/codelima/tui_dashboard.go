package codelima

import (
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
)

type tuiView int

const (
	tuiViewNodes tuiView = iota
	tuiViewTemplates
	tuiViewPorts
	tuiViewActivity
	tuiViewSystem
)

type tuiDetailTab int

const (
	tuiDetailOverview tuiDetailTab = iota
	tuiDetailMetrics
	tuiDetailLogs
	tuiDetailSpec
	tuiDetailShell
)

func (a *vaxisTUIApp) refreshFleet() {
	if a.fleet == nil {
		a.fleet = newTUIFleet(a.treeWorkspaceRoot)
		a.fleet.groupByDir = a.treeWorkspaceRoot == "" && a.managerSettings().GroupByDirectory
		a.fleet.selectedID = a.state.selectedEntry().node.ID
	}
	records := make([]tuiFleetRecord, 0, len(a.state.nodes))
	for _, node := range a.state.nodes {
		record := tuiFleetRecord{Node: node, Status: a.fleetStatus(node)}
		if observation := node.LastRuntimeObservation; observation != nil && observation.UsageDetails != nil && observation.UsageDetails.UptimeSeconds != nil && observation.ResourceUsageSampledAt != nil {
			record.StartedAt = observation.ResourceUsageSampledAt.Add(-time.Duration(*observation.UsageDetails.UptimeSeconds * float64(time.Second)))
		}
		record.Sessions = len(a.state.sessions.TargetSessionKeys(tuiTreeEntry{node: node}.key()))
		for _, mapping := range node.Ports {
			parts := strings.Split(mapping, ":")
			if port, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
				record.Ports = append(record.Ports, port)
			}
		}
		slices.Sort(record.Ports)
		record.Ports = slices.Compact(record.Ports)
		records = append(records, record)
	}
	a.fleet.replace(records)
}

func (a *vaxisTUIApp) fleetStatus(node Node) string {
	if status := a.operationDisplayStatus(tuiTreeEntry{node: node}); status != "" {
		return status
	}
	// A booted VM can still have a failed bootstrap. Preserve the failure in
	// the manager while keeping its actual VM observation available in detail.
	if node.Status == NodeStatusFailed {
		return "failed"
	}
	switch nodeVMStatus(node) {
	case "provisioning", "registering", "installing":
		return "starting"
	case "terminating":
		return "deleting"
	default:
		return nodeVMStatus(node)
	}
}

func (a *vaxisTUIApp) fleetSelected() (tuiFleetRecord, bool) {
	for _, record := range a.fleet.records {
		if record.Node.ID == a.fleet.selectedID {
			return record, true
		}
	}
	return tuiFleetRecord{}, false
}

func (a *vaxisTUIApp) selectFleetNode() {
	if a.fleet.selectedID == "" {
		a.state.selection = -1
		return
	}
	for index, entry := range a.state.entries {
		if entry.node.ID == a.fleet.selectedID {
			_ = a.state.selectIndex(index)
			return
		}
	}
}

func (a *vaxisTUIApp) drawManager(window vaxis.Window) {
	width, height := window.Size()
	rgb := a.vx.CanRGB()
	switch a.managerSettings().Color {
	case "ansi":
		rgb = false
	case "truecolor":
		rgb = true
	}
	theme := newTUITheme(rgb)
	tuiFill(window, theme.text)
	if a.sessions != nil {
		cols, rows := a.activeTerminalSize(width, height, a.state.focus)
		a.sessions.SetPreferredTerminalSize(cols, rows)
	}
	a.refreshFleet()
	a.drawManagerHeader(window, theme)
	a.fleetMouseRows = nil
	switch a.view {
	case tuiViewNodes:
		if a.nodeDetail || a.state.focus == tuiFocusTerminal {
			a.drawNodeDetail(window, theme)
		} else {
			a.drawFleet(window, theme)
		}
	case tuiViewTemplates:
		a.drawTemplates(window, theme)
	case tuiViewPorts:
		a.drawPublishedPorts(window, theme)
	case tuiViewActivity:
		a.drawActivity(window, theme)
	case tuiViewSystem:
		a.drawSystem(window, theme)
	}
	status := a.status
	if !a.statusAt.IsZero() && time.Since(a.statusAt) >= 6*time.Second {
		status = ""
	}
	style := theme.muted
	if status == "" {
		status = a.backgroundOperationSummary()
		if len(a.operationOrder) == 1 {
			if operation := a.operations[a.operationOrder[0]]; operation != nil {
				status = "◐ " + operation.Title
			}
		}
	}
	if a.statusLevel >= slog.LevelWarn {
		style = theme.coral
	}
	tuiLine(window.New(1, height-2, width-2, 1), 0, status, style)
	footer := a.managerFooter()
	if a.overlay != nil {
		// Reuse existing form/selector behavior inside a centered modal. The
		// underlying manager retains both its selection and navigation state.
		modalWidth, modalHeight := min(width-4, 96), height-4
		modal := window.New((width-modalWidth)/2, 2, modalWidth, modalHeight)
		tuiFill(modal, theme.raised)
		a.overlay.Draw(modal, theme.mint, theme.muted, theme.coral)
		footer = a.overlay.FooterHint()
	}
	bar := window.New(0, height-1, width, 1)
	tuiFill(bar, theme.chrome)
	tuiLine(bar, 0, " "+footer, theme.chrome)
}

func (a *vaxisTUIApp) drawManagerHeader(win vaxis.Window, theme tuiTheme) {
	width, _ := win.Size()
	bar := win.New(0, 0, width, 1)
	tuiFill(bar, theme.chrome)
	logo := theme.mint
	logo.Background = theme.chrome.Background
	logo.Attribute = vaxis.AttrBold
	if a.hostTerminalTabActive() {
		logo.Foreground = theme.coral.Foreground
	}
	tuiLine(bar.New(0, 0, 9, 1), 0, a.headerLogoText(), logo)
	col := 10
	views := []string{"1 Nodes", "2 Templates", "3 Ports", "4 Activity", "5 System"}
	if count := len(a.activity.page.Running); count > 0 {
		views[tuiViewActivity] += fmt.Sprintf(" ◐%d", count)
	}
	a.viewRegions = nil
	for i, label := range views {
		style := theme.chrome
		if a.view == tuiView(i) {
			style = theme.selected
			style.Foreground = theme.mint.Foreground
		}
		label = " " + label + " "
		cells := renderedTextWidth(a.vx, label)
		tuiLine(bar.New(col, 0, cells, 1), 0, label, style)
		a.viewRegions = append(a.viewRegions, tuiRect{col: col, row: 0, width: cells, height: 1})
		col += cells
	}
	right := width
	if width >= 140 && !a.host.Capacity.ObservedAt.IsZero() {
		host := "host " + a.host.identity()
		hostWidth := min(renderedTextWidth(a.vx, host), width-col-20)
		right = width - hostWidth - 2
		tuiLine(bar.New(right+2, 0, hostWidth, 1), 0, host, theme.chrome)
	}
	if right-col > 18 {
		scope := "all nodes"
		if a.treeWorkspaceRoot != "" {
			scope = a.treeWorkspaceRoot
		}
		connection := "● daemon"
		if a.daemonDisconnected {
			connection = "○ reconnecting"
		} else if a.service == nil || a.service.daemonClient == nil {
			connection = "○ local"
		}
		tuiLine(bar.New(col+1, 0, right-col-1, 1), 0, connection+"   scope "+scope, theme.chrome)
	}
}

func (a *vaxisTUIApp) drawFleet(win vaxis.Window, theme tuiTheme) {
	width, height := win.Size()
	counts := map[string]int{}
	for _, record := range a.fleet.records {
		counts[record.Status]++
	}
	countText := fmt.Sprintf("%d nodes", len(a.fleet.records))
	for _, status := range []string{"running", "starting", "stopped", "created", "failed"} {
		if count := counts[status]; count > 0 {
			glyph, _ := theme.status(status)
			countText += fmt.Sprintf("   %s %d %s", glyph, count, status)
		}
	}
	tuiLine(win.New(2, 1, width-4, 1), 0, countText, theme.muted)
	if width >= 140 && !a.host.Capacity.ObservedAt.IsZero() {
		allocation := a.host.allocationText()
		space := width - renderedTextWidth(a.vx, countText) - 6
		allocationWidth := min(space, renderedTextWidth(a.vx, allocation))
		style := theme.muted
		if a.host.Capacity.CPUs > 0 && a.host.AllocatedCPUs > uint64(a.host.Capacity.CPUs) {
			style = theme.coral
		}
		tuiLine(win.New(width-allocationWidth-2, 1, allocationWidth, 1), 0, allocation, style)
	}
	layout := layoutTUIFleet(width, height, len(a.fleet.rows))
	table := win.New(layout.Table.col, layout.Table.row, layout.Table.width, layout.Table.height)
	inner := border.All(table, theme.mint)
	grouping := "flat"
	if a.fleet.grouped() {
		grouping = "grouped by directory"
	}
	direction := "▲"
	if a.fleet.descending {
		direction = "▼"
	}
	title := fmt.Sprintf(" Nodes %d · %s · sort %s %s ", len(a.fleet.visible), grouping, a.fleet.sort, direction)
	if len(a.fleet.marks) > 0 {
		title += fmt.Sprintf("· %d marked ", len(a.fleet.marks))
	}
	tuiLine(table.New(2, 0, layout.Table.width-4, 1), 0, title, theme.text)
	innerWidth, innerHeight := inner.Size()
	columns := tuiFleetColumns(innerWidth, layout.Compact)
	for _, column := range columns {
		tuiLine(inner.New(column.col, 0, column.width, 1), 0, column.title, theme.dim)
	}
	content := inner.New(0, 1, innerWidth, max(0, innerHeight-1))
	col, row := content.Origin()
	a.treeContentRect = tuiRect{col: col, row: row, width: innerWidth, height: max(0, innerHeight-1)}
	a.fleetMouseRows = slices.Clone(a.fleet.viewport(a.treeContentRect.height))
	byID := make(map[string]tuiFleetRecord, len(a.fleet.visible))
	for _, record := range a.fleet.visible {
		byID[record.Node.ID] = record
	}
	for row, item := range a.fleetMouseRows {
		if item.NodeID == "" {
			tuiLine(content, row, fmt.Sprintf("▾ %s · %d nodes", item.Directory, item.Count), theme.muted)
			continue
		}
		a.drawFleetRecord(content.New(0, row, innerWidth, 1), byID[item.NodeID], columns, layout.Compact, theme)
	}
	if len(a.fleet.visible) == 0 {
		message := "No nodes yet.  n Create node   a Browse configurations"
		if len(a.fleet.records) > 0 {
			message = "No matching nodes.  / Edit filter   Esc Clear filter"
		}
		tuiLine(content, 0, message, theme.muted)
	}
	if selected, ok := a.fleetSelected(); ok {
		if layout.Preview.height > 0 {
			a.drawNodePreview(win.New(layout.Preview.col, layout.Preview.row, layout.Preview.width, layout.Preview.height), selected, theme)
		} else {
			text := fmt.Sprintf("%s · %s · %d vCPU · %s memory · %s · Enter details", selected.Node.Slug, selected.Status, selected.Node.VCPUs, formatTUIResourceSize(selected.Node.MemoryMiB), selected.Node.DirectoryPath)
			tuiLine(win.New(layout.Summary.col, layout.Summary.row, layout.Summary.width, 1), 0, text, theme.muted)
		}
	}
	if a.fleet.filter != "" || a.filterInput != nil {
		input := win.New(1, height-3, width-2, 1)
		tuiFill(input, theme.selected)
		if a.filterInput != nil {
			a.filterInput.SetPrompt("/ ")
			a.filterInput.Prompt = theme.mint
			a.filterInput.Draw(input)
		} else {
			tuiLine(input, 0, "/ "+a.fleet.filter, theme.muted)
		}
	}
}

type tuiFleetColumn struct {
	field      tuiFleetSort
	title      string
	col, width int
}

func tuiFleetColumns(width int, compact bool) []tuiFleetColumn {
	columns := []tuiFleetColumn{
		{field: tuiFleetSortName, title: "NAME", width: 20},
		{field: tuiFleetSortStatus, title: "STATUS", width: 12},
		{field: tuiFleetSortTemplate, title: "TEMPLATE", width: 11},
		{field: tuiFleetSortCPU, title: "CPU", width: 6},
		{field: tuiFleetSortMemory, title: "MEMORY", width: 15},
		{field: tuiFleetSortDisk, title: "DISK", width: 15},
		{field: tuiFleetSortSessions, title: "SES", width: 5},
		{field: tuiFleetSortPorts, title: "PORTS", width: 12},
		{field: tuiFleetSortUptime, title: "UPTIME", width: 8},
	}
	if compact {
		columns = columns[:7]
		columns[1].width, columns[2].width = 11, 10
		columns[4].title, columns[5].title = "MEM%", "DISK%"
		columns[4].width, columns[5].width, columns[6].width = 6, 6, 4
	} else if width >= 134 {
		columns[3].width, columns[4].width, columns[5].width = 14, 21, 20
	}
	used := 3
	for _, column := range columns[1:] {
		used += column.width
	}
	columns[0].width = max(8, width-used)
	col := 3
	for i := range columns {
		columns[i].col = col
		col += columns[i].width
	}
	return columns
}

func (a *vaxisTUIApp) drawFleetRecord(win vaxis.Window, record tuiFleetRecord, columns []tuiFleetColumn, compact bool, theme tuiTheme) {
	style := theme.text
	marker := " "
	if record.Node.ID == a.fleet.selectedID {
		style = theme.selected
		marker = "▸"
	}
	if a.fleet.marks[record.Node.ID] {
		marker = "✓"
	}
	tuiFill(win, style)
	tuiLine(win.New(0, 0, 2, 1), 0, marker, style)
	for _, column := range columns {
		if !compact && record.Status != "running" && column.field == tuiFleetSortCPU {
			text := ""
			switch record.Status {
			case "created":
				text = "never started · s to start"
			case "stopped":
				text = "stopped · " + nodeWorkspaceMode(record.Node) + " workspace · s to start"
			case "failed", "broken":
				text = "failed · Enter details · l logs"
			default:
				text = record.Status
			}
			for _, operation := range a.entryOperations(tuiTreeEntry{node: record.Node}) {
				if len(operation.Lines) > 0 {
					text = operation.Lines[len(operation.Lines)-1]
				}
			}
			_, statusStyle := theme.status(record.Status)
			style.Foreground = statusStyle.Foreground
			tuiLine(win.New(column.col, 0, -1, 1), 0, text, style)
			break
		}
		text, foreground := a.fleetCell(record, column.field, compact, column.width, theme)
		cellStyle := style
		cellStyle.Foreground = foreground.Foreground
		tuiLine(win.New(column.col, 0, column.width-1, 1), 0, text, cellStyle)
	}
}

func (a *vaxisTUIApp) fleetCell(record tuiFleetRecord, field tuiFleetSort, compact bool, width int, theme tuiTheme) (string, vaxis.Style) {
	switch field {
	case tuiFleetSortName:
		return record.Node.Slug, theme.text
	case tuiFleetSortStatus:
		glyph, style := theme.status(record.Status)
		return glyph + " " + record.Status, style
	case tuiFleetSortTemplate:
		return record.Node.ConfigurationSlug, theme.violet
	case tuiFleetSortSessions:
		return strconv.Itoa(record.Sessions), theme.text
	case tuiFleetSortPorts:
		ports := make([]string, len(record.Ports))
		for i, port := range record.Ports {
			ports[i] = strconv.Itoa(port)
		}
		if len(ports) == 0 {
			return "—", theme.dim
		}
		return strings.Join(ports, " "), theme.muted
	case tuiFleetSortUptime:
		if !record.StartedAt.IsZero() {
			return max(time.Duration(0), time.Since(record.StartedAt)).Truncate(time.Second).String(), theme.muted
		}
		return "—", theme.dim
	default:
		value, ok := tuiFleetMetric(record, field)
		if !ok {
			return "—", theme.dim
		}
		text := fmt.Sprintf("%.0f%%", value)
		if !compact && width >= 14 {
			if field == tuiFleetSortCPU && record.Node.LastRuntimeObservation != nil {
				text = tuiCPUSparkline(record.Node.LastRuntimeObservation.CPUHistory, time.Now(), 8*time.Second, 8) + " " + text
			} else {
				text = tuiGauge(value, min(10, width-7)) + " " + text
			}
		}
		return text, theme.usage(value)
	}
}

func (a *vaxisTUIApp) drawNodePreview(win vaxis.Window, record tuiFleetRecord, theme tuiTheme) {
	inner := border.All(win, theme.border)
	width, height := inner.Size()
	tuiLine(win.New(2, 0, width-2, 1), 0, " "+record.Node.Slug+" preview · Enter open detail ", theme.text)
	third := width / 3
	identity := inner.New(1, 0, third-2, height)
	resources := inner.New(third+1, 0, third-2, height)
	sessions := inner.New(2*third+1, 0, width-2*third-2, height)
	a.drawNodeIdentity(identity, record, theme)
	tuiLine(resources, 0, "CPU · last 60s", theme.muted)
	var cpuHistory []daemon.NodeMetricPoint
	if observation := record.Node.LastRuntimeObservation; observation != nil {
		cpuHistory = observation.CPUHistory
	}
	graphHeight := max(1, min(4, height-6))
	drawTUIMetricChart(resources.New(0, 1, third-2, graphHeight), cpuHistory, time.Now(), time.Minute, metricCPU, theme.mint)
	for i, field := range []tuiFleetSort{tuiFleetSortMemory, tuiFleetSortDisk} {
		text, style := a.fleetCell(record, field, false, third-10, theme)
		tuiLine(resources, graphHeight+2+i, fmt.Sprintf("%-7s %s", field, text), style)
	}
	tuiLine(sessions, 0, fmt.Sprintf("Sessions · %d", record.Sessions), theme.muted)
	keys := a.state.sessions.TargetSessionKeys(tuiTreeEntry{node: record.Node}.key())
	for i, key := range keys {
		if i >= 3 {
			break
		}
		label := key
		if a.sessions != nil {
			if session, ok := a.sessions.Session(key); ok {
				label = terminalTabLabel(session, a.terminalMetadata(key))
			}
		}
		tuiLine(sessions, i+1, label, theme.text)
	}
	if len(keys) == 0 {
		tuiLine(sessions, 1, "No sessions", theme.dim)
	}
	tuiLine(sessions, 5, "Published ports", theme.muted)
	ports, _ := a.fleetCell(record, tuiFleetSortPorts, false, third, theme)
	tuiLine(sessions, 6, ports, theme.blue)
}

func (a *vaxisTUIApp) drawNodeIdentity(win vaxis.Window, record tuiFleetRecord, theme tuiTheme) {
	node := record.Node
	fields := [][2]string{
		{"status", record.Status}, {"template", node.ConfigurationSlug},
		{"resources", fmt.Sprintf("%d vCPU · %s mem · %s disk", node.VCPUs, formatTUIResourceSize(node.MemoryMiB), formatTUIResourceSize(node.DiskMiB))},
		{"image", node.Image}, {"directory", node.DirectoryPath}, {"workspace", nodeWorkspaceMode(node)},
		{"bootstrap", strconv.FormatBool(node.BootstrapCompleted)}, {"agent", node.AgentProfileName},
	}
	for i, field := range fields {
		tuiLine(win, i, fmt.Sprintf("%-10s %s", field[0], field[1]), theme.text)
	}
}

func (a *vaxisTUIApp) managerFooter() string {
	if a.state.focus == tuiFocusTerminal {
		return "Shell receives all keys · F6 return to TUI"
	}
	if a.filterInput != nil {
		return "status: template: dir: port: · Enter apply · Esc cancel"
	}
	if a.nodeDetail && a.detailTab == tuiDetailShell {
		return "F6 focus shell · [ ] tabs · t new guest · T new host · w close · F7 search · Esc overview"
	}
	if a.metricsVisible() {
		return "+ − window · p sort · Space pause · y PID · Tab view · x shell · Esc nodes"
	}
	if a.logsVisible() {
		if a.logs.input != nil {
			return "Enter keep search · Esc cancel · search pauses follow"
		}
		return "[] source · f follow · / search · w wrap · L level · Tab view · Esc nodes"
	}
	if a.specVisible() {
		return "N new from template · e edit · v YAML · y path · c clone · Tab view · Esc nodes"
	}
	if a.nodeDetail {
		if a.nodeStarting() {
			if operation, ok := a.activeStartOperation(); ok && operation.State == "canceling" {
				return "Canceling start · l logs · T host shell · Esc nodes"
			}
			if a.lifecycle.queuedOperation != "" {
				return "Attachment queued · Esc cancels queue · s cancel start · l logs · T host shell"
			}
			return "x attach when ready · s cancel start · l logs · T host shell · Esc nodes"
		}
		if a.nodeLifecycleFailed() {
			if a.state.selectedEntry().node.Status == NodeStatusFailed && a.lifecycleFailure().step != nil {
				return "r retry · x inspect · e env · E template · s stop/start · l logs · Esc nodes"
			}
			return "s start/stop · x inspect · E template · l logs · Esc nodes"
		}
		return "Tab/Shift-Tab view · x shell · s start/stop · l logs · Esc nodes · : commands · ? help"
	}
	switch a.view {
	case tuiViewTemplates:
		return "↑↓ select · Tab configs/envs · n new · e edit · c clone · d delete · ? help"
	case tuiViewPorts:
		return "Esc nodes · : commands · ? help · q quit"
	case tuiViewActivity:
		if a.activity.input != nil {
			return "Enter keep filter · Esc cancel · [] pages search older history"
		}
		return "↑↓ move · ⏎ open · K cancel · f level · / filter · y copy · [] pages · g top"
	case tuiViewSystem:
		return "Tab panel · ↑↓ move · Enter edit · C check · R repair · Esc nodes · ? help"
	}
	if a.vx != nil {
		if width, _ := a.vx.Window().Size(); width < 100 {
			return "↑↓ select  Enter detail  x shell  s start/stop  n new  / filter  ? help"
		}
	}
	return "↑↓ select · Enter detail · s start/stop · x shell · n new · / filter · : commands · ? help · q quit"
}
