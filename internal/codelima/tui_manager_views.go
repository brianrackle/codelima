package codelima

import (
	"context"
	"log/slog"

	"go.rockorager.dev/vaxis"
)

type tuiManagerData struct {
	recipes              tuiRecipeCatalog
	configurations       []Configuration
	configurationsLoaded bool
	environments         []EnvironmentConfig
	loading              bool
	err                  error
	preferences          *Config
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
	a.startFleetEvidenceLoad(true)
	a.startPortsLoad(true)
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
	detail := a.nodeDetail
	emptyFleet := len(a.state.nodes) == 0
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
			if !detail && emptyFleet {
				data.configurations, data.err = a.service.ConfigurationList(ctx, false)
				data.configurationsLoaded = true
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
	if a.detailTab == tuiDetailShell {
		a.drawEmbeddedIdentity(win.New(2, 2, width-4, 1), record, theme)
	} else {
		tuiLine(win.New(2, 2, width-4, 1), 0, "‹ Nodes / "+record.Node.Slug+"  ·  "+record.Status, theme.mint)
	}
	tabs := []string{"Overview", "Metrics", "Logs", "Spec"}
	if a.embeddedShellEnabled() {
		tabs = append(tabs, "Shell")
	}
	a.detailNodeID = record.Node.ID
	a.nodeBackRect = tuiRect{col: 2, row: 2, width: 7, height: 1}
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
		a.detailTargets = append(a.detailTargets, tuiDetailTarget{rect: tuiRect{col: col, row: 3, width: min(cells, width-col), height: 1}, tab: tuiDetailTab(i)})
		col += cells
	}
	body := win.New(1, 5, width-2, height-8)
	switch a.detailTab {
	case tuiDetailOverview:
		if a.drawNodeLifecycle(body, record, theme) {
			break
		}
		a.drawNodeOverview(body, record, theme)
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
		a.drawEmbeddedShell(body, record, theme)
	}
}
