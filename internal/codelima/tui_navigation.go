package codelima

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/textinput"
)

func (a *vaxisTUIApp) switchView(view tuiView) {
	if view == tuiViewSystem && a.emptyNodesVisible() {
		a.systemPanel = tuiSystemDoctor
	}
	a.emptyNodes.scroll = 0
	a.cancelActivityAttachment()
	a.activity.input, a.activity.pendingNode = nil, ""
	a.ports.input, a.ports.pendingNode = nil, ""
	a.view, a.nodeDetail, a.detailScroll = view, false, 0
	a.state.focusTree()
	a.state.treePaneMode = tuiTreePaneModeInfo
	a.filterInput = nil
	a.loadManagerData(false)
	a.syncSessionFocus()
}

func (a *vaxisTUIApp) openNodeDetail(tab tuiDetailTab) {
	a.cancelActivityAttachment()
	if !a.state.selectedEntry().valid() {
		return
	}
	if tab == tuiDetailMetrics && a.nodeStarting() {
		tab = tuiDetailOverview
	}
	if tab == tuiDetailShell && !a.embeddedShellEnabled() {
		tab = tuiDetailOverview
	}
	a.view, a.nodeDetail, a.detailTab, a.detailScroll = tuiViewNodes, true, tab, 0
	a.state.focusTree()
	a.state.treePaneMode = tuiTreePaneModeInfo
	if tab == tuiDetailShell {
		a.state.treePaneMode = tuiTreePaneModeTerminal
	}
	a.loadManagerData(false)
}

func (a *vaxisTUIApp) dispatchManagerKey(key vaxis.Key) bool {
	return a.dispatchManagerAction(a.keyAction(key), key)
}

func (a *vaxisTUIApp) dispatchManagerAction(action string, key vaxis.Key) bool {
	a.createIntent = nil
	defer a.cancelHiddenMetricsLoad()
	defer a.cancelHiddenLogsLoad()
	defer a.cancelHiddenSpecLoad()
	defer a.cancelHiddenLifecycleLoad()
	a.refreshFleet()
	if a.handleLogsSearchAction(action, key) || a.handleActivitySearchAction(action, key) || a.handlePortsSearchAction(action, key) {
		return false
	}
	if a.filterInput != nil {
		switch {
		case key.EventType == vaxis.EventPaste:
			a.filterInput.Update(key)
			a.fleet.setFilter(a.filterInput.String())
		case action == "input.cancel" && tuiKeyActivates(key, true):
			a.fleet.setFilter(a.filterBefore)
			a.filterInput = nil
		case action == "input.accept" && tuiKeyActivates(key, true):
			a.filterInput = nil
		default:
			a.filterInput.Update(key)
			a.fleet.setFilter(a.filterInput.String())
		}
		a.selectFleetNode()
		return false
	}
	if !tuiKeyActivates(key, false) {
		return false
	}
	if a.nodeDetail && a.detailTab == tuiDetailShell && (action == "terminal.previous_tab" || action == "terminal.next_tab" || action == "terminal.move_tab_left" || action == "terminal.move_tab_right") {
		var err error
		switch action {
		case "terminal.previous_tab":
			err = a.switchTerminalTab(-1)
		case "terminal.next_tab":
			err = a.switchTerminalTab(1)
		case "terminal.move_tab_left":
			err = a.moveTerminalTab(-1)
		case "terminal.move_tab_right":
			err = a.moveTerminalTab(1)
		}
		if err != nil {
			a.setStatus(slog.LevelError, err.Error())
		}
		a.syncSessionFocus()
		return false
	}
	if tuiKeyActivates(key, true) {
		for i, viewAction := range []string{"view.nodes", "view.recipes", "view.ports", "view.activity", "view.system"} {
			if action == viewAction {
				a.claimShortcutKey(key)
				a.switchView(tuiView(i))
				return false
			}
		}
	}
	if a.handleRecipeAction(action, key) {
		return false
	}
	if a.handleEmptyNodesAction(action, key) {
		return false
	}
	if a.handleOverviewAction(action, key) {
		return false
	}
	if action == "navigation.up" || action == "navigation.down" {
		delta := 1
		if action == "navigation.up" {
			delta = -1
		}
		a.moveManagerSelection(delta)
		return false
	}
	if !tuiKeyActivates(key, true) {
		return false
	}
	a.claimShortcutKey(key)
	if a.handleLifecycleAction(action) {
		return false
	}
	if a.handleMetricsAction(action) || a.handleLogsAction(action) || a.handleSpecAction(action) || a.handleActivityAction(action) || a.handlePortsAction(action) {
		return false
	}
	var err error
	switch {
	case action == "app.quit":
		return true
	case action == "navigation.leave":
		if a.nodeDetail {
			a.nodeDetail = false
		} else {
			return true
		}
	case action == "navigation.back":
		switch {
		case a.nodeDetail:
			a.nodeDetail = false
			a.state.treePaneMode = tuiTreePaneModeInfo
		case a.view != tuiViewNodes:
			a.switchView(tuiViewNodes)
		case a.fleet.filter != "":
			a.fleet.setFilter("")
			a.selectFleetNode()
		default:
			a.fleet.clearMarks()
		}
	case action == "scope.toggle":
		a.emptyNodes.scroll = 0
		if a.treeWorkspaceRoot != "" {
			a.treeWorkspaceRoot = ""
		} else {
			if a.launchScope == "" {
				a.launchScope, err = os.Getwd()
			}
			a.treeWorkspaceRoot = a.launchScope
		}
		a.fleet.groupByDir = a.treeWorkspaceRoot == "" && a.managerSettings().GroupByDirectory
		a.fleet.rebuild()
		a.startDataRefresh("")
	case action == "view.templates":
		a.environmentsView = false
		a.switchView(tuiViewTemplates)
	case action == "view.environments" && !a.nodeDetail:
		a.environmentsView = true
		a.switchView(tuiViewTemplates)
	case action == "view.activity":
		a.switchView(tuiViewActivity)
	case action == "palette.open":
		a.openCommandPalette()
	case action == "help.open":
		a.openKeyHelp()
	case action == "navigation.next_tab", action == "navigation.previous_tab":
		switch {
		case a.view == tuiViewSystem:
			delta := 1
			if action == "navigation.previous_tab" {
				delta = -1
			}
			a.systemPanel = (a.systemPanel + delta + tuiSystemPanelCount) % tuiSystemPanelCount
		case a.view == tuiViewTemplates:
			a.switchRecipeKind(!a.environmentsView)
		case a.nodeDetail:
			delta, count := 1, 4
			if a.embeddedShellEnabled() {
				count = 5
			}
			if action == "navigation.previous_tab" {
				delta = -1
			}
			a.detailTab = tuiDetailTab((int(a.detailTab) + delta + count) % count)
			if a.detailTab == tuiDetailMetrics && a.nodeStarting() {
				a.detailTab = tuiDetailTab((int(a.detailTab) + delta + count) % count)
			}
			a.state.treePaneMode = tuiTreePaneModeInfo
			if a.detailTab == tuiDetailShell {
				a.state.treePaneMode = tuiTreePaneModeTerminal
			}
			a.detailScroll = 0
			a.loadManagerData(false)
		}
	case a.view == tuiViewTemplates:
		err = a.handleTemplateActionID(action)
	case a.view == tuiViewSystem && (action == "system.refresh" || action == "system.repair"):
		a.loadManagerData(action == "system.repair")
	case a.view == tuiViewSystem && action == "system.open" && a.systemPanel == tuiSystemSettings:
		err = a.editManagerSetting()
	case a.view == tuiViewSystem && action == "system.open" && a.systemPanel == tuiSystemDoctor:
		a.openDoctorDetails()
	case a.view == tuiViewSystem && action == "system.open" && a.systemPanel == tuiSystemDaemon:
		a.openDaemonDetails()
	case a.view == tuiViewSystem && action == "system.update":
		a.runSystemDaemonAction("update")
	case a.view == tuiViewSystem && action == "system.restart":
		a.runSystemDaemonAction("restart")
	case a.view == tuiViewSystem && action == "system.logs":
		a.runSystemDaemonAction("log")
	case a.view == tuiViewSystem && action == "system.diagnose":
		a.runSystemDaemonAction("diagnose")
	case a.view != tuiViewNodes:
		return false
	case action == "fleet.filter" && !a.nodeDetail:
		a.filterBefore = a.fleet.filter
		a.filterInput = textinput.New()
		a.filterInput.SetContent(a.fleet.filter)
	case action == "fleet.group" && !a.nodeDetail:
		a.fleet.groupByDir = !a.fleet.groupByDir
		a.fleet.rebuild()
	case (action == "fleet.sort_previous" || action == "fleet.sort_next") && !a.nodeDetail:
		delta := 1
		if action == "fleet.sort_previous" {
			delta = -1
		}
		a.fleet.sort = tuiFleetSort((int(a.fleet.sort) + delta + int(tuiFleetSortCount)) % int(tuiFleetSortCount))
		a.fleet.rebuild()
	case action == "fleet.reverse" && !a.nodeDetail:
		a.fleet.descending = !a.fleet.descending
		a.fleet.rebuild()
	case action == "fleet.mark" && !a.nodeDetail:
		a.fleet.toggleMark()
	case action == "fleet.mark_all" && !a.nodeDetail:
		a.fleet.markVisible()
	case action == "node.open":
		a.openNodeDetail(tuiDetailOverview)
	case action == "node.logs":
		a.openNodeDetail(tuiDetailLogs)
	case action == "node.create":
		err = a.openCreateNodeDialog()
	case action == "node.clone":
		err = a.performAction(tuiActionSpec{ID: tuiActionNodeClone})
	case action == "node.delete":
		err = a.fleetNodeAction(tuiActionNodeDelete)
	case action == "node.toggle":
		action := tuiActionNodeStart
		if selected, ok := a.fleetSelected(); ok && nodeIsRunning(selected.Node) {
			action = tuiActionNodeStop
		}
		err = a.fleetNodeAction(action)
	case action == "node.restart" && a.state.selectedEntry().node.Status == NodeStatusFailed && (a.nodeDetail || len(a.fleet.marks) == 0):
		err = a.performAction(tuiActionSpec{ID: tuiActionNodeRetry})
	case action == "node.restart":
		err = a.fleetNodeAction(tuiActionNodeRestart)
	case action == "node.copy_name":
		err = a.copyToHostClipboard(a.state.selectedEntry().node.Slug)
	case action == "node.open_port" && !a.nodeDetail:
		err = a.openFleetPort()
	case action == "node.shell_chooser":
		a.openShellChooser()
	case action == "node.shell":
		err = a.startShellMode(a.managerSettings().Shell.Mode, false, false, "")
	case a.nodeDetail && a.detailTab == tuiDetailShell:
		switch {
		case action == "session.guest":
			err = a.openTerminalTab()
		case action == "session.host":
			err = a.openHostTerminalTab()
		case action == "terminal.close_tab":
			err = a.closeTerminalTab()
		case action == "terminal.search":
			err = a.openTerminalSearch()
		}
	}
	if err != nil {
		a.setStatus(slog.LevelError, err.Error())
	}
	a.syncSessionFocus()
	return false
}

func (a *vaxisTUIApp) moveManagerSelection(delta int) {
	a.createIntent = nil
	if a.view == tuiViewPorts {
		a.movePortSelection(delta)
		return
	}
	if a.view == tuiViewActivity {
		a.moveActivitySelection(delta)
		return
	}
	if a.specVisible() {
		a.moveSpecSelection(delta)
		return
	}
	if a.logsVisible() {
		a.moveLogSelection(delta)
		return
	}
	if a.metricsVisible() {
		a.moveMetricProcess(delta)
		return
	}
	switch {
	case a.view == tuiViewSystem:
		switch a.systemPanel {
		case tuiSystemSettings:
			a.settingIndex = max(0, min(len(a.managerSettingRows())-1, a.settingIndex+delta))
		case tuiSystemDoctor:
			a.moveDoctorSelection(delta)
		default:
			a.systemScroll[a.systemPanel] = max(0, a.systemScroll[a.systemPanel]+delta)
		}
	case a.view == tuiViewTemplates:
		a.moveRecipeSelection(delta)
	case a.nodeDetail || a.view == tuiViewActivity:
		a.detailScroll = max(0, a.detailScroll-delta)
	default:
		a.fleet.move(delta)
		a.selectFleetNode()
	}
}

func (a *vaxisTUIApp) handleTemplateActionID(action string) error {
	if a.environmentsView {
		if action == "recipe.new" {
			a.openEnvironmentEditor(EnvironmentConfig{}, true)
		} else if a.environmentIndex < len(a.managerData.environments) {
			environment := a.managerData.environments[a.environmentIndex]
			switch {
			case action == "environment.test":
				return a.submitEnvironmentTest(environment)
			case action == "recipe.edit", action == "recipe.open":
				if action == "recipe.open" && a.recipes.details && a.openRecipeDependencyFix(environment, a.recipes.command) {
					break
				}
				e := a.openEnvironmentEditor(environment, false)
				if action == "recipe.open" {
					e.openCommands(a.recipes.command)
				}
			case action == "recipe.clone":
				a.openEnvironmentEditor(environment, true)
			case action == "recipe.delete":
				a.openDeleteEnvironmentConfigDialog(environment)
			case action == "recipe.restore":
				if _, ok := builtInEnvironment(environment.Slug); ok {
					a.openEnvironmentEditor(environment, false).restoreDefaults()
				}
			case action == "environment.command_add":
				e := a.openEnvironmentEditor(environment, false)
				e.openCommands(len(environment.BootstrapCommands))
				a.overlay.(*tuiCommandEditor).addCommand()
			case action == "environment.command_delete":
				if !a.recipes.details {
					return fmt.Errorf("focus a command with %s before removing it", a.keyHint("recipe.focus_details"))
				}
				if a.recipes.command < len(environment.BootstrapCommands) {
					e := a.openEnvironmentEditor(environment, false)
					e.setCommands(removeCommandsByIndex(environment.BootstrapCommands, []int{a.recipes.command}))
				}
			case action == "environment.command_editor":
				return a.editEnvironmentCommandExternally(environment, a.recipes.command)
			}
		}
		return nil
	}
	if action == "recipe.new" {
		a.openCreateConfigurationDialog()
		return nil
	}
	if a.templateIndex >= len(a.managerData.configurations) {
		return nil
	}
	configuration := a.managerData.configurations[a.templateIndex]
	switch {
	case action == "recipe.edit", action == "recipe.open":
		a.openUpdateConfigurationDialog(configuration)
	case action == "recipe.clone":
		a.openCloneConfigurationDialog(configuration)
	case action == "recipe.delete":
		a.openDeleteConfigurationDialog(configuration)
	case action == "recipe.new_node":
		return a.openCreateNodeDialogWithDefaults("", configuration.Slug)
	case action == "recipe.restore":
		if _, ok := builtInTemplateDefaults(configuration.Slug, a.managerSettings()); ok {
			editor := a.openTemplateEditor(configuration, false, nil, nil)
			editor.restoreDefaults()
		}
	}
	return nil
}

func (a *vaxisTUIApp) openEmbeddedShell(host bool) error {
	if !a.embeddedShellEnabled() {
		return fmt.Errorf("embedded terminal is off; enable Settings › shell.mode")
	}
	var err error
	if host {
		err = a.openHostTerminalTab()
	} else if len(a.state.sessions.TargetSessionKeys(a.state.selectedEntry().key())) == 0 {
		err = a.openTerminalTab()
	}
	if err != nil {
		return err
	}
	a.nodeDetail, a.detailTab = true, tuiDetailShell
	a.state.treePaneMode = tuiTreePaneModeTerminal
	return a.state.focusTerminal()
}

func (a *vaxisTUIApp) openShellChooser() {
	if err := a.openNodeShellChooser(a.state.selectedEntry().node.ID); err != nil {
		a.setStatus(slog.LevelError, err.Error())
	}
}

func (a *vaxisTUIApp) fleetNodeAction(action tuiActionID) error {
	if a.nodeDetail {
		return a.performAction(tuiActionSpec{ID: action})
	}
	if action == tuiActionNodeStart && a.emptyNodesVisible() {
		if reason := a.emptyNodeBlockReason(); reason != "" {
			return fmt.Errorf("%s", reason)
		}
	}
	targets := a.fleet.targets()
	if len(targets) == 0 {
		return fmt.Errorf("select a node first")
	}
	if len(targets) == 1 && len(a.fleet.marks) == 0 {
		return a.performAction(tuiActionSpec{ID: action})
	}
	plan := make([]tuiPaletteNodePlan, 0, len(targets))
	for _, record := range targets {
		plan = append(plan, tuiPaletteNodePlan{record.Node.ID, action})
	}
	verb := strings.TrimPrefix(string(action), "node.")
	verb = strings.ToUpper(verb[:1]) + verb[1:]
	return a.openBatchReview(fmt.Sprintf("%s %s?", verb, countLabel(len(targets), "node", "nodes")), plan, true)
}
