package codelima

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
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
	a.createIntent = nil
	defer a.cancelHiddenMetricsLoad()
	defer a.cancelHiddenLogsLoad()
	defer a.cancelHiddenSpecLoad()
	defer a.cancelHiddenLifecycleLoad()
	a.refreshFleet()
	if a.handleLogsSearchKey(key) || a.handleActivitySearchKey(key) || a.handlePortsSearchKey(key) {
		return false
	}
	if a.filterInput != nil {
		switch {
		case key.EventType == vaxis.EventPaste:
			a.filterInput.Update(key)
			a.fleet.setFilter(a.filterInput.String())
		case key.Matches(vaxis.KeyEsc) && tuiKeyActivates(key, true):
			a.fleet.setFilter(a.filterBefore)
			a.filterInput = nil
		case key.Matches(vaxis.KeyEnter) && tuiKeyActivates(key, true):
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
	if a.nodeDetail && a.detailTab == tuiDetailShell && (key.MatchString("[") || key.MatchString("]")) {
		delta := 1
		if key.MatchString("[") {
			delta = -1
		}
		if err := a.switchTerminalTab(delta); err != nil {
			a.setStatus(slog.LevelError, err.Error())
		}
		a.syncSessionFocus()
		return false
	}
	if tuiKeyActivates(key, true) {
		for i := range 5 {
			if key.MatchString(strconv.Itoa(i + 1)) {
				a.claimShortcutKey(key)
				a.switchView(tuiView(i))
				return false
			}
		}
	}
	if a.handleRecipeKey(key) {
		return false
	}
	if a.handleEmptyNodesKey(key) {
		return false
	}
	if a.handleOverviewKey(key) {
		return false
	}
	if key.MatchString("Up") || key.MatchString("k") || key.MatchString("Down") || key.MatchString("j") {
		delta := 1
		if key.MatchString("Up") || key.MatchString("k") {
			delta = -1
		}
		a.moveManagerSelection(delta)
		return false
	}
	if !tuiKeyActivates(key, true) {
		return false
	}
	a.claimShortcutKey(key)
	if a.handleLifecycleKey(key) {
		return false
	}
	if a.handleMetricsKey(key) || a.handleLogsKey(key) || a.handleSpecKey(key) || a.handleActivityKey(key) || a.handlePortsKey(key) {
		return false
	}
	var err error
	switch {
	case key.MatchString("q"), isQuitKey(key):
		if a.nodeDetail {
			a.nodeDetail = false
		} else {
			return true
		}
	case key.Matches(vaxis.KeyEsc):
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
	case key.MatchString("0"):
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
	case key.MatchString("a"):
		a.environmentsView = false
		a.switchView(tuiViewTemplates)
	case key.MatchString("g") && !a.nodeDetail:
		a.environmentsView = true
		a.switchView(tuiViewTemplates)
	case key.MatchString("m"):
		a.switchView(tuiViewActivity)
	case key.MatchString(":"):
		a.openCommandPalette()
	case key.MatchString("?"):
		a.openKeyHelp()
	case key.MatchString("Tab"), key.Matches(vaxis.KeyTab, vaxis.ModShift):
		switch {
		case a.view == tuiViewSystem:
			delta := 1
			if key.Modifiers&vaxis.ModShift != 0 {
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
			if key.Modifiers&vaxis.ModShift != 0 {
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
		err = a.handleTemplateAction(key)
	case a.view == tuiViewSystem && (key.MatchString("C") || key.MatchString("R")):
		a.loadManagerData(key.MatchString("R"))
	case a.view == tuiViewSystem && key.MatchString("Enter") && a.systemPanel == tuiSystemSettings:
		err = a.editManagerSetting()
	case a.view == tuiViewSystem && key.MatchString("Enter") && a.systemPanel == tuiSystemDoctor:
		a.openDoctorDetails()
	case a.view == tuiViewSystem && key.MatchString("Enter") && a.systemPanel == tuiSystemDaemon:
		a.openDaemonDetails()
	case a.view == tuiViewSystem && key.MatchString("u"):
		a.runSystemDaemonAction("update")
	case a.view == tuiViewSystem && key.MatchString("S"):
		a.runSystemDaemonAction("restart")
	case a.view == tuiViewSystem && key.MatchString("L"):
		a.runSystemDaemonAction("log")
	case a.view == tuiViewSystem && key.MatchString("D"):
		a.runSystemDaemonAction("diagnose")
	case a.view != tuiViewNodes:
		return false
	case key.MatchString("/") && !a.nodeDetail:
		a.filterBefore = a.fleet.filter
		a.filterInput = textinput.New()
		a.filterInput.SetContent(a.fleet.filter)
	case key.MatchString("G") && !a.nodeDetail:
		a.fleet.groupByDir = !a.fleet.groupByDir
		a.fleet.rebuild()
	case (key.MatchString("<") || key.MatchString(">")) && !a.nodeDetail:
		delta := 1
		if key.MatchString("<") {
			delta = -1
		}
		a.fleet.sort = tuiFleetSort((int(a.fleet.sort) + delta + int(tuiFleetSortCount)) % int(tuiFleetSortCount))
		a.fleet.rebuild()
	case key.MatchString("=") && !a.nodeDetail:
		a.fleet.descending = !a.fleet.descending
		a.fleet.rebuild()
	case key.MatchString(" ") && !a.nodeDetail:
		a.fleet.toggleMark()
	case key.MatchString("*") && !a.nodeDetail:
		a.fleet.markVisible()
	case key.MatchString("Enter"):
		a.openNodeDetail(tuiDetailOverview)
	case key.MatchString("l"):
		a.openNodeDetail(tuiDetailLogs)
	case key.MatchString("n"):
		err = a.openCreateNodeDialog()
	case key.MatchString("c"):
		err = a.performAction(tuiActionSpec{ID: tuiActionNodeClone})
	case key.MatchString("d"):
		err = a.fleetNodeAction(tuiActionNodeDelete)
	case key.MatchString("s"):
		action := tuiActionNodeStart
		if selected, ok := a.fleetSelected(); ok && nodeIsRunning(selected.Node) {
			action = tuiActionNodeStop
		}
		err = a.fleetNodeAction(action)
	case key.MatchString("r") && a.state.selectedEntry().node.Status == NodeStatusFailed && (a.nodeDetail || len(a.fleet.marks) == 0):
		err = a.performAction(tuiActionSpec{ID: tuiActionNodeRetry})
	case key.MatchString("r"):
		err = a.fleetNodeAction(tuiActionNodeRestart)
	case key.MatchString("y"):
		err = a.copyToHostClipboard(a.state.selectedEntry().node.Slug)
	case key.MatchString("o") && !a.nodeDetail:
		err = a.openFleetPort()
	case key.MatchString("X"):
		a.openShellChooser()
	case key.MatchString("x"):
		err = a.startShellMode(a.managerSettings().Shell.Mode, false, false, "")
	case a.nodeDetail && a.detailTab == tuiDetailShell:
		switch {
		case key.MatchString("t"):
			err = a.openTerminalTab()
		case key.MatchString("T"):
			err = a.openHostTerminalTab()
		case key.MatchString("["):
			err = a.switchTerminalTab(-1)
		case key.MatchString("]"):
			err = a.switchTerminalTab(1)
		case key.MatchString("w"):
			err = a.closeTerminalTab()
		case key.Matches(vaxis.KeyF07):
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

func (a *vaxisTUIApp) handleTemplateAction(key vaxis.Key) error {
	if a.environmentsView {
		if key.MatchString("n") {
			a.openEnvironmentEditor(EnvironmentConfig{}, true)
		} else if a.environmentIndex < len(a.managerData.environments) {
			environment := a.managerData.environments[a.environmentIndex]
			switch {
			case key.MatchString("t"):
				return a.submitEnvironmentTest(environment)
			case key.MatchString("e"), key.MatchString("Enter"):
				if key.MatchString("Enter") && a.recipes.details && a.openRecipeDependencyFix(environment, a.recipes.command) {
					break
				}
				e := a.openEnvironmentEditor(environment, false)
				if key.MatchString("Enter") {
					e.openCommands(a.recipes.command)
				}
			case key.MatchString("c"):
				a.openEnvironmentEditor(environment, true)
			case key.MatchString("d"):
				a.openDeleteEnvironmentConfigDialog(environment)
			case key.MatchString("R"):
				if _, ok := builtInEnvironment(environment.Slug); ok {
					a.openEnvironmentEditor(environment, false).restoreDefaults()
				}
			case key.MatchString("o"):
				e := a.openEnvironmentEditor(environment, false)
				e.openCommands(len(environment.BootstrapCommands))
				_, _ = a.overlay.(*tuiCommandEditor).Update(vaxis.Key{Keycode: 'n', Modifiers: vaxis.ModCtrl})
			case key.MatchString("D"):
				if !a.recipes.details {
					return fmt.Errorf("focus a command with → before removing it")
				}
				if a.recipes.command < len(environment.BootstrapCommands) {
					e := a.openEnvironmentEditor(environment, false)
					e.setCommands(removeCommandsByIndex(environment.BootstrapCommands, []int{a.recipes.command}))
				}
			case key.MatchString("E"):
				return a.editEnvironmentCommandExternally(environment, a.recipes.command)
			}
		}
		return nil
	}
	if key.MatchString("n") {
		a.openCreateConfigurationDialog()
		return nil
	}
	if a.templateIndex >= len(a.managerData.configurations) {
		return nil
	}
	configuration := a.managerData.configurations[a.templateIndex]
	switch {
	case key.MatchString("e"), key.MatchString("Enter"):
		a.openUpdateConfigurationDialog(configuration)
	case key.MatchString("c"):
		a.openCloneConfigurationDialog(configuration)
	case key.MatchString("d"):
		a.openDeleteConfigurationDialog(configuration)
	case key.MatchString("N"):
		return a.openCreateNodeDialogWithDefaults("", configuration.Slug)
	case key.MatchString("R"):
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
