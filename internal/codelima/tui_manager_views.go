package codelima

import (
	"context"
	"fmt"
	"log/slog"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
)

type tuiManagerData struct {
	recipes        tuiRecipeCatalog
	configurations []Configuration
	environments   []EnvironmentConfig
	logs           []Event
	logNodeID      string
	loading        bool
	err            error
	preferences    *Config
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
	a.startDaemonOverview(true)
	a.cancelHiddenMetricsLoad()
	a.cancelHiddenLogsLoad()
	a.cancelHiddenSpecLoad()
	a.cancelHiddenLifecycleLoad()
	if a.service == nil {
		return
	}
	if a.view == tuiViewSystem {
		a.startDoctorCheck(repair, true)
	}
	a.managerGeneration++
	a.cancelSpecLoad()
	a.startSpecLoad()
	a.cancelLifecycleLoad()
	a.startLifecycleLoad()
	a.startActivityLoad(a.view == tuiViewActivity)
	if a.logsVisible() {
		a.cancelLogsLoad()
		a.startLogsLoad(true)
		return
	}
	if a.metricsVisible() {
		a.startMetricsLoad()
		return
	}
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
			if data.err == nil {
				data.recipes, data.err = a.service.readRecipeCatalog(ctx, data.environments)
			}
		case tuiViewSystem:
			var cfg Config
			cfg, data.err = a.service.SettingsRead()
			if data.err == nil {
				data.preferences = &cfg
			}
		case tuiViewNodes:
			if detail && nodeID != "" && tab == tuiDetailOverview {
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
	a.rememberRecipeSelection()
	a.managerData = event.data
	if event.view == tuiViewTemplates {
		a.restoreRecipeSelection()
		if a.environmentSelectID != "" {
			for i, e := range event.data.environments {
				if e.ID == a.environmentSelectID {
					a.environmentIndex = i
					a.environmentSelectID = ""
					break
				}
			}
		}
	}
	if a.templateSelectID != "" && event.view == tuiViewTemplates {
		for i, c := range event.data.configurations {
			if c.ID == a.templateSelectID {
				a.templateIndex = i
				a.templateSelectID = ""
				break
			}
		}
	}
	if event.view == tuiViewTemplates {
		commands := 0
		if a.environmentIndex < len(a.managerData.environments) {
			commands = len(a.managerData.environments[a.environmentIndex].BootstrapCommands)
		}
		a.recipes.command = max(0, min(a.recipes.command, commands-1))
		a.rememberRecipeSelection()
	}
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
		if tuiDetailTab(i) == tuiDetailMetrics && a.nodeStarting() {
			style = theme.dim
		}
		cells := len(name) + 4
		if tuiDetailTab(i) == tuiDetailSpec && a.spec.nodeID == record.Node.ID && a.spec.err == nil && len(a.spec.snapshot.Differences) > 0 {
			win.New(col, 3, cells+2, 1).PrintTruncate(0, vaxis.Segment{Text: " " + name + " ", Style: style}, vaxis.Segment{Text: "•", Style: theme.amber})
			cells += 2
		} else {
			tuiLine(win.New(col, 3, cells, 1), 0, " "+name+" ", style)
		}
		col += cells
	}
	body := win.New(1, 5, width-2, height-8)
	switch a.detailTab {
	case tuiDetailOverview:
		if a.drawNodeLifecycle(body, record, theme) {
			break
		}
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
		if a.nodeStarting() {
			tuiLine(body, 1, "Metrics unlock when the node is ready.", theme.dim)
		} else {
			a.drawNodeMetrics(body, theme)
		}
	case tuiDetailLogs:
		a.drawNodeLogs(body, theme)
	case tuiDetailSpec:
		a.drawNodeSpec(body, theme)
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
