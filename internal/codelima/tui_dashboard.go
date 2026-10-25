package codelima

import (
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

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
		record := tuiFleetRecord{Node: node, Status: a.fleetStatus(node), PortsUnavailable: !a.fleetPortsCurrent()}
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
		for _, route := range a.fleetRoutes(node.ID) {
			port := route.GuestPort
			if port == 0 {
				port = route.Port
			}
			if port > 0 && port <= 65535 {
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
	if a.fleetDeleteRetryable(node) {
		return "delete incomplete"
	}
	if a.fleetReceiptFailed(node) {
		return "failed"
	}
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
	defer a.startFleetEvidenceLoad(false)
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
	a.normalizeEmbeddedShellMode()
	theme := a.managerTheme()
	tuiFill(window, theme.text)
	if a.sessions != nil {
		cols, rows := a.activeTerminalSize(width, height, a.state.focus)
		a.sessions.SetPreferredTerminalSize(cols, rows)
	}
	a.refreshFleet()
	a.drawManagerHeader(window, theme)
	normalTheme := theme
	_, keyHelp := a.overlay.(*tuiKeyHelp)
	_, palette := a.overlay.(*tuiPalette)
	_, shellChooser := a.overlay.(*tuiShellChooser)
	_, nodeConfirmation := a.overlay.(*tuiNodeConfirmation)
	_, batchReview := a.overlay.(*tuiBatchReview)
	_, operationOutput := a.overlay.(*tuiOperationOutputView)
	if a.visibleCreateForm() != nil || keyHelp || palette || shellChooser || nodeConfirmation || batchReview || operationOutput {
		theme = theme.dimmed()
	}
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
	theme = normalTheme
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
	statusRow := height - 2
	if a.view == tuiViewTemplates {
		// Keep recipe actions visible and clickable while a notice replaces
		// the explanatory note above them.
		statusRow--
		if status != "" {
			tuiFill(window.New(1, statusRow, width-2, 1), theme.text)
		}
	}
	tuiLine(window.New(1, statusRow, width-2, 1), 0, status, style)
	footer := a.managerFooter()
	if a.overlay != nil {
		// Reuse existing form/selector behavior inside a centered modal. The
		// underlying manager retains both its selection and navigation state.
		drawOverlay := func(overlay tuiOverlay) {
			if full, ok := overlay.(interface{ FullScreen() bool }); ok && full.FullScreen() {
				overlay.Draw(window.New(0, 1, width, height-2), theme.mint, theme.muted, theme.coral)
				return
			}
			modalWidth, modalHeight := min(width-4, 96), height-4
			if sized, ok := overlay.(interface{ PreferredSize() (int, int) }); ok {
				preferredWidth, preferredHeight := sized.PreferredSize()
				modalWidth, modalHeight = min(width-4, preferredWidth), min(height-4, preferredHeight)
			}
			modal := window.New((width-modalWidth)/2, (height-modalHeight)/2, modalWidth, modalHeight)
			tuiFill(modal, theme.raised)
			overlay.Draw(modal, theme.mint, theme.muted, theme.coral)
		}
		if editor := a.visibleTemplateEditor(); editor != nil && a.overlay != editor {
			drawOverlay(editor)
		} else if parent := a.visibleCreateForm(); parent != nil && a.overlay != parent {
			drawOverlay(parent)
		}
		drawOverlay(a.overlay)
		footer = a.overlay.FooterHint()
	}
	bar := window.New(0, height-1, width, 1)
	tuiFill(bar, theme.chrome)
	if a.view == tuiViewActivity && a.overlay == nil {
		a.drawActivityFooter(bar, theme)
	} else {
		tuiLine(bar, 0, " "+footer, theme.chrome)
	}
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
	if width < 100 {
		for i := range views {
			if tuiView(i) != a.view {
				views[i] = strconv.Itoa(i + 1)
			}
		}
	}
	if count := len(a.activity.page.Running); count > 0 {
		views[tuiViewActivity] += fmt.Sprintf(" ◐%d", count)
	}
	if badge := a.doctorBadge(); badge != "" {
		views[tuiViewSystem] += " " + badge
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
		badge := ""
		if i == int(tuiViewActivity) && len(a.activity.page.Running) > 0 {
			badge = fmt.Sprintf("◐%d", len(a.activity.page.Running))
		} else if i == int(tuiViewSystem) {
			badge = a.doctorBadge()
		}
		if badge != "" {
			badgeWidth := renderedTextWidth(a.vx, badge)
			badgeStyle := style
			badgeStyle.Foreground = theme.amber.Foreground
			tuiLine(bar.New(col+cells-badgeWidth-1, 0, badgeWidth, 1), 0, badge, badgeStyle)
		}
		a.viewRegions = append(a.viewRegions, tuiRect{col: col, row: 0, width: cells, height: 1})
		col += cells
	}
	if a.nodeDetail && a.detailTab == tuiDetailShell {
		label := "shell.mode: embedded · ● daemon"
		if a.daemonDisconnected {
			label = "shell.mode: embedded · ○ reconnecting"
		} else if a.service == nil || a.service.daemonClient == nil {
			label = "shell.mode: embedded · ○ local"
		}
		if width < 100 {
			label = strings.TrimPrefix(label, "shell.mode: ")
		}
		cells := min(renderedTextWidth(a.vx, label), max(0, width-col-1))
		tuiLine(bar.New(width-cells, 0, cells, 1), 0, label, theme.chrome)
		return
	}
	if width < 100 {
		connection := "●"
		if a.daemonDisconnected || a.service == nil || a.service.daemonClient == nil {
			connection = "○"
		}
		text := connection + " " + a.host.compactAllocation()
		cells := min(renderedTextWidth(a.vx, text), max(0, width-col-1))
		style := theme.chrome
		if (a.host.Capacity.CPUs > 0 && a.host.AllocatedCPUs > uint64(a.host.Capacity.CPUs)) ||
			(a.host.Capacity.MemoryBytes != nil && a.host.AllocatedMemoryBytes > *a.host.Capacity.MemoryBytes) {
			style.Foreground = theme.coral.Foreground
		}
		tuiLine(bar.New(width-cells, 0, cells, 1), 0, text, style)
		return
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
	if a.emptyNodesVisible() {
		a.drawEmptyNodes(win, theme)
		return
	}
	width, height := win.Size()
	counts := map[string]int{}
	for _, record := range a.fleet.records {
		counts[record.Status]++
	}
	countText := countLabel(len(a.fleet.records), "node", "nodes")
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
	columns := tuiFleetColumns(innerWidth, layout.Compact, !a.fleet.grouped())
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
			tuiLine(content, row, fmt.Sprintf("▾ %s · %s", item.Directory, countLabel(item.Count, "node", "nodes")), theme.muted)
			continue
		}
		a.drawFleetRecord(content.New(0, row, innerWidth, 1), byID[item.NodeID], columns, layout.Compact, theme)
	}
	if len(a.fleet.visible) == 0 {
		message := "No nodes yet.  n Create node   a Browse configurations"
		if len(a.fleet.records) > 0 || a.fleet.filter != "" || a.filterInput != nil {
			message = "No matching nodes.  / Edit filter   Esc Clear filter"
		}
		tuiLine(content, 0, message, theme.muted)
	}
	if len(a.fleet.marks) > 0 {
		if layout.Preview.height > 0 {
			a.drawMarkedPreview(win.New(layout.Preview.col, layout.Preview.row, layout.Preview.width, layout.Preview.height), theme)
		} else {
			tuiLine(win.New(layout.Summary.col, layout.Summary.row, layout.Summary.width, 1), 0, a.markedImpact().summary(), theme.muted)
		}
	} else if selected, ok := a.fleetSelected(); ok {
		if layout.Preview.height > 0 {
			a.drawNodePreview(win.New(layout.Preview.col, layout.Preview.row, layout.Preview.width, layout.Preview.height), selected, theme)
		} else {
			uptime, _ := a.fleetCell(selected, tuiFleetSortUptime, true, 8, theme)
			ports, _ := a.fleetCell(selected, tuiFleetSortPorts, true, 16, theme)
			text := fmt.Sprintf("%s · up %s · ports %s · Enter details", selected.Node.Slug, uptime, ports)
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

func tuiFleetColumns(width int, compact, flat bool) []tuiFleetColumn {
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
	if !compact && flat {
		columns[7].field, columns[7].title, columns[7].width = tuiFleetSortDirectory, "DIRECTORY", 20
	}
	used := 3
	for _, column := range columns[1:] {
		used += column.width
	}
	if !compact && used+8 > width {
		// At the medium breakpoint directory context yields space to the
		// minimum name width, keeping the final uptime column on screen.
		overflow := used + 8 - width
		columns[7].width -= overflow
		used -= overflow
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
		marker += "✓"
	}
	tuiFill(win, style)
	tuiLine(win.New(0, 0, 2, 1), 0, marker, style)
	for _, column := range columns {
		if record.Status != "running" && (column.field == tuiFleetSortCPU || (compact && column.field == tuiFleetSortTemplate)) {
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
			if phase := a.fleetPhase(record.Node.ID); phase != "" {
				text = phase
			}
			if evidence := a.fleetEvidenceLine(record); evidence != "" {
				text = evidence
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
	case tuiFleetSortDirectory:
		return record.Node.DirectoryPath, theme.muted
	case tuiFleetSortSessions:
		return strconv.Itoa(record.Sessions), theme.text
	case tuiFleetSortPorts:
		ports := make([]string, len(record.Ports))
		for i, port := range record.Ports {
			ports[i] = strconv.Itoa(port)
		}
		if record.PortsUnavailable {
			ports = append(ports, "?")
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
		if !compact && (field == tuiFleetSortMemory || field == tuiFleetSortDisk) {
			text = tuiFleetBytes(record, field)
			if cells := min(10, width-len(text)-2); cells >= 3 {
				text = tuiGauge(value, cells) + " " + text
			}
			return text, theme.usage(value)
		}
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
	a.drawFleetResources(resources, record, theme)
	a.drawFleetSessions(sessions, record.Node, theme)
	a.drawFleetRoutes(sessions, record.Node.ID, 5, min(8, height), theme)
	a.drawFleetRecent(sessions, record.Node.ID, 9, height, theme)
}

func (a *vaxisTUIApp) managerFooter() string {
	if a.emptyNodesVisible() {
		return "n new · 2 templates · 5 doctor · 0 scope · R re-check · : commands · ? help"
	}
	if a.state.focus == tuiFocusTerminal {
		return "F6 back to TUI · all other keys, including Alt+←/→, reach the shell"
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
		return "Esc nodes · Tab view · ctrl+↑↓ panels · t/T new shell · s start/stop · r restart · l logs"
	}
	switch a.view {
	case tuiViewTemplates:
		return a.recipeFooter()
	case tuiViewPorts:
		if a.ports.input != nil {
			return "Enter keep filter · Esc restore filter"
		}
		return "↑↓ move · o open · y copy · p pin · Enter node · / filter · ? help"
	case tuiViewActivity:
		if a.activity.input != nil {
			return "Enter keep filter · Esc cancel · [] pages search older history"
		}
		return "↑↓ move · ⏎ open · K cancel · f level · / filter · y copy · [] pages · g top"
	case tuiViewSystem:
		if a.systemPanel == tuiSystemDaemon {
			return "u update · S restart · L log · D diagnose · Tab panel · Enter details"
		}
		return "Tab panel · ↑↓ move · Enter details/edit · C check · R repair · Esc nodes"
	}
	if len(a.fleet.marks) > 0 {
		return "Space mark · s start/stop · r restart · d delete · Esc clear · ? help"
	}
	failed := a.state.selectedEntry().node.Status == NodeStatusFailed
	if a.vx != nil {
		if width, _ := a.vx.Window().Size(); width < 100 {
			if failed {
				return "↑↓ select · Enter detail · r retry · s stop/start · l logs · ? help"
			}
			return "↑↓ select  Enter detail  x shell  s start/stop  n new  / filter  ? help"
		}
	}
	restart := "r restart"
	if failed {
		restart = "r retry"
	}
	return "↑↓ select · Enter detail · s start/stop · " + restart + " · x shell · o port · n new · / filter · : commands · ? help · q quit"
}
