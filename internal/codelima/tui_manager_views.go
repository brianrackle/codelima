package codelima

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
)

type tuiManagerData struct {
	configurations []Configuration
	environments   []EnvironmentConfig
	logs           []Event
	logNodeID      string
	doctor         DoctorReport
	loading        bool
	err            error
	preferences    *Config
	daemon         *daemon.Status
	daemonErr      error
}

type tuiManagerDataEvent struct {
	generation         uint64
	preferenceRevision uint64
	view               tuiView
	data               tuiManagerData
}

// All filesystem/runtime work runs outside the event loop. A generation ties
// the response to the navigation that requested it, so late replies cannot
// replace another node's logs or clear a newer loading/error state.
func (a *vaxisTUIApp) loadManagerData(repair bool) {
	if a.service == nil {
		return
	}
	a.managerGeneration++
	generation, view := a.managerGeneration, a.view
	preferenceRevision := a.preferenceRevision
	nodeID := a.state.selectedEntry().node.ID
	detail, tab := a.nodeDetail, a.detailTab
	a.managerData.loading, a.managerData.err = true, nil
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	load := func() tuiManagerDataEvent {
		data := tuiManagerData{}
		switch view {
		case tuiViewTemplates:
			data.configurations, data.err = a.service.ConfigurationList(ctx, false)
			if data.err == nil {
				data.environments, data.err = a.service.EnvironmentConfigList(ctx, false)
			}
		case tuiViewSystem:
			if a.service.daemonClient != nil {
				var status daemon.Status
				data.daemonErr = a.service.daemonClient.Call(ctx, "daemon.status", nil, &status)
				if data.daemonErr == nil {
					data.daemon = &status
				}
			}
			var cfg Config
			cfg, data.err = a.service.SettingsRead()
			if data.err == nil {
				data.preferences = &cfg
				data.doctor, data.err = a.service.Doctor(ctx, repair)
			}
		case tuiViewNodes:
			if detail && nodeID != "" && (tab == tuiDetailLogs || tab == tuiDetailOverview) {
				data.logNodeID = nodeID
				data.logs, data.err = a.service.NodeLogs(ctx, nodeID)
			}
		}
		return tuiManagerDataEvent{generation: generation, preferenceRevision: preferenceRevision, view: view, data: data}
	}
	if a.postEvent == nil {
		a.applyManagerData(load())
		return
	}
	go func() { a.postCompletion(load()) }()
}

func (a *vaxisTUIApp) applyManagerData(event tuiManagerDataEvent) {
	if event.generation != a.managerGeneration || event.view != a.view {
		return
	}
	a.managerData = event.data
	if event.data.preferences != nil && event.preferenceRevision == a.preferenceRevision {
		a.preferences = event.data.preferences
	}
	if event.data.err != nil {
		a.setStatus(slog.LevelError, event.data.err.Error())
	}
}

func (a *vaxisTUIApp) drawNodeDetail(win vaxis.Window, theme tuiTheme) {
	width, height := win.Size()
	record, ok := a.fleetSelected()
	if !ok {
		tuiLine(win, 2, "  Node is no longer available. Esc returns to nodes.", theme.muted)
		return
	}
	if a.state.focus == tuiFocusTerminal {
		a.detailTab = tuiDetailShell
	}
	tuiLine(win.New(2, 2, width-4, 1), 0, "‹ Nodes / "+record.Node.Slug+"  ·  "+record.Status, theme.mint)
	tabs := []string{"Overview", "Metrics", "Logs", "Spec"}
	if len(a.state.sessions.TargetSessionKeys(a.state.selectedEntry().key())) > 0 || a.detailTab == tuiDetailShell {
		tabs = append(tabs, "Shell")
	}
	col := 2
	for i, name := range tabs {
		style := theme.muted
		if a.detailTab == tuiDetailTab(i) {
			style = theme.selected
		}
		tuiLine(win.New(col, 3, len(name)+4, 1), 0, " "+name+" ", style)
		col += len(name) + 4
	}
	body := win.New(1, 5, width-2, height-8)
	switch a.detailTab {
	case tuiDetailOverview:
		leftWidth := max(20, (width-4)/2)
		identity := border.All(body.New(0, 0, leftWidth, max(1, height-8)), theme.border)
		tuiLine(identity, 0, "Identity · frozen at creation", theme.muted)
		a.drawNodeIdentity(identity.New(1, 2, leftWidth-4, height-12), record, theme)
		right := border.All(body.New(leftWidth+1, 0, width-leftWidth-3, height-8), theme.border)
		tuiLine(right, 0, "Resources", theme.muted)
		for i, field := range []tuiFleetSort{tuiFleetSortCPU, tuiFleetSortMemory, tuiFleetSortDisk} {
			text, style := a.fleetCell(record, field, false, 25, theme)
			tuiLine(right, i+2, fmt.Sprintf("%-7s %s", field, text), style)
		}
		tuiLine(right, 6, fmt.Sprintf("Sessions · %d", record.Sessions), theme.muted)
		tuiLine(right, 7, "x open shell · X shell chooser", theme.dim)
		ports, _ := a.fleetCell(record, tuiFleetSortPorts, false, 40, theme)
		tuiLine(right, 9, "Ports  "+ports, theme.blue)
		tuiLine(right, 11, "Recent events", theme.muted)
		if a.managerData.logNodeID == record.Node.ID {
			for i, event := range a.managerData.logs[max(0, len(a.managerData.logs)-3):] {
				tuiLine(right, i+12, event.Timestamp.Format("15:04:05")+" "+event.Message, theme.text)
			}
		}
	case tuiDetailMetrics:
		inner := border.All(body, theme.border)
		tuiLine(inner, 0, "Guest resource observations", theme.muted)
		for i, field := range []tuiFleetSort{tuiFleetSortCPU, tuiFleetSortMemory, tuiFleetSortDisk} {
			text, style := a.fleetCell(record, field, false, width-15, theme)
			tuiLine(inner, i*3+2, fmt.Sprintf("%-8s %s", field, text), style)
		}
		if observation := record.Node.LastRuntimeObservation; observation != nil && observation.ResourceUsageSampledAt != nil {
			tuiLine(inner, 12, "Last sample "+observation.ResourceUsageSampledAt.Format(time.RFC3339), theme.dim)
		}
	case tuiDetailLogs:
		inner := border.All(body, theme.border)
		tuiLine(inner, 0, "Events", theme.muted)
		_, available := inner.Size()
		if a.managerData.loading {
			tuiLine(inner, 2, "Loading events…", theme.dim)
		} else if a.managerData.logNodeID == record.Node.ID {
			logs := a.managerData.logs
			start := max(0, len(logs)-max(0, available-2)-a.detailScroll)
			for i, event := range logs[start:] {
				if i >= available-2 {
					break
				}
				tuiLine(inner, i+2, event.Timestamp.Format("15:04:05")+"  "+event.Type+"  "+event.Message, theme.text)
			}
			if len(logs) == 0 {
				tuiLine(inner, 2, "No events recorded for this node.", theme.dim)
			}
		}
	case tuiDetailSpec:
		inner := border.All(body, theme.border)
		node := record.Node
		lines := []string{"Frozen specification · edits to templates affect future nodes", "", "Configuration: " + node.ConfigurationSlug,
			"Image: " + node.Image, fmt.Sprintf("Resources: %d vCPU · %s memory · %s disk", node.VCPUs, formatTUIResourceSize(node.MemoryMiB), formatTUIResourceSize(node.DiskMiB)),
			"Environments: " + strings.Join(node.Environments, " → "), "", "Resolved bootstrap commands"}
		for i, command := range node.BootstrapCommands {
			lines = append(lines, fmt.Sprintf("%d. %s", i+1, command))
		}
		for i, line := range lines[min(a.detailScroll, len(lines)):] {
			tuiLine(inner, i, line, theme.text)
		}
	case tuiDetailShell:
		body.PrintTruncate(0, a.terminalTabSegments(theme.selected, theme.muted)...)
		surface := body.New(0, 1, width-2, max(0, height-9))
		col, row := surface.Origin()
		cols, rows := surface.Size()
		a.terminalBodyRect = tuiRect{col: col, row: row, width: cols, height: rows}
		if a.sessions != nil {
			a.sessions.SetPreferredTerminalSize(cols, rows)
		}
		a.drawTerminalSurface(surface, tuiTreeEntry{node: record.Node}, theme.mint, theme.muted, theme.coral)
	}
}

func (a *vaxisTUIApp) drawTemplates(win vaxis.Window, theme tuiTheme) {
	width, height := win.Size()
	tuiLine(win.New(2, 2, width-4, 1), 0, "Templates   [Configs]   [Environments]   Tab switch", theme.mint)
	if a.environmentsView {
		tuiLine(win.New(2, 2, width-4, 1), 0, "Templates   [Configs]   [Environments ✓]   Tab switch", theme.mint)
	}
	inner := border.All(win.New(1, 4, width-2, height-7), theme.border)
	if a.managerData.loading {
		tuiLine(inner, 0, "Loading templates…", theme.dim)
		return
	}
	if a.environmentsView {
		tuiLine(inner, 0, "NAME                     COMMANDS", theme.dim)
		for i, environment := range a.managerData.environments {
			style := theme.text
			if i == a.environmentIndex {
				style = theme.selected
			}
			tuiLine(inner, i+2, fmt.Sprintf("%-24s %d", environment.Slug, len(environment.BootstrapCommands)), style)
		}
	} else {
		tuiLine(inner, 0, "NAME               VCPU   MEMORY      DISK        ENVIRONMENTS", theme.dim)
		for i, configuration := range a.managerData.configurations {
			style := theme.text
			if i == a.templateIndex {
				style = theme.selected
			}
			tuiLine(inner, i+2, fmt.Sprintf("%-18s %-6d %-11s %-11s %s", configuration.Slug, configuration.VCPUs, formatTUIResourceSize(configuration.MemoryMiB), formatTUIResourceSize(configuration.DiskMiB), strings.Join(configuration.Environments, ", ")), style)
		}
	}
	tuiLine(win.New(2, height-3, width-4, 1), 0, "n new · e edit · c clone · d delete · N create node · Tab configs/environments", theme.muted)
}

func (a *vaxisTUIApp) drawPublishedPorts(win vaxis.Window, theme tuiTheme) {
	width, height := win.Size()
	tuiLine(win.New(2, 2, width-4, 1), 0, "Ports · published TCP routes", theme.mint)
	inner := border.All(win.New(1, 4, width-2, height-7), theme.border)
	tuiLine(inner, 0, "NODE                     HOST:GUEST               STATE", theme.dim)
	row := 2
	for _, record := range a.fleet.records {
		for _, mapping := range record.Node.Ports {
			tuiLine(inner, row, fmt.Sprintf("%-24s %-24s %s", record.Node.Slug, mapping, record.Status), theme.text)
			row++
		}
	}
	if row == 2 {
		tuiLine(inner, 2, "No published ports configured.", theme.dim)
	}
}

func (a *vaxisTUIApp) drawActivity(win vaxis.Window, theme tuiTheme) {
	width, height := win.Size()
	tuiLine(win.New(2, 2, width-4, 1), 0, "Activity", theme.mint)
	inner := border.All(win.New(1, 4, width-2, height-7), theme.border)
	row := 0
	for _, id := range a.operationOrder {
		if operation := a.operations[id]; operation != nil {
			tuiLine(inner, row, "◐ "+operation.Title, theme.amber)
			row++
		}
	}
	if row == 0 {
		tuiLine(inner, row, "No operations running in this window.", theme.dim)
		row++
	}
	row++
	entries := a.messages.Entries()
	for i := len(entries) - 1 - a.detailScroll; i >= 0 && row < height-9; i-- {
		entry := entries[i]
		style := theme.text
		if entry.Level >= slog.LevelError {
			style = theme.coral
		} else if entry.Level >= slog.LevelWarn {
			style = theme.amber
		}
		tuiLine(inner, row, entry.Time.Format("15:04:05")+"  "+entry.Level.String()+"  "+entry.Text, style)
		row++
	}
}
