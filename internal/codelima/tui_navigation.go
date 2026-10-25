package codelima

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/textinput"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
)

func (a *vaxisTUIApp) switchView(view tuiView) {
	a.cancelActivityAttachment()
	a.activity.input, a.activity.pendingNode = nil, ""
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
	if a.handleLogsSearchKey(key) || a.handleActivitySearchKey(key) {
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
	if a.handleMetricsKey(key) || a.handleLogsKey(key) || a.handleSpecKey(key) || a.handleActivityKey(key) {
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
			if len(a.state.sessions.TargetSessionKeys(a.state.selectedEntry().key())) > 0 {
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
	case key.MatchString("r") && a.state.selectedEntry().node.Status == NodeStatusFailed:
		err = a.performAction(tuiActionSpec{ID: tuiActionNodeRetry})
	case key.MatchString("y"):
		err = a.copyToHostClipboard(a.state.selectedEntry().node.Slug)
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
		if a.systemPanel == tuiSystemSettings {
			a.settingIndex = max(0, min(len(a.managerSettingRows())-1, a.settingIndex+delta))
		} else {
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
			case key.MatchString("e"), key.MatchString("Enter"):
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
	menu := &tuiMenu{Title: "Open shell · " + a.state.selectedEntry().node.Slug,
		Description: []string{"Ctrl+] detaches · F6 returns from embedded · x uses " + a.managerSettings().Shell.Mode,
			"Shift+choice saves it as x's default."},
		Entries: []tuiMenuEntry{
			{Key: 'a', Label: "Attach here", Action: func() error { return a.startRawShell(false, false, "") }, ShiftAction: a.defaultShellChoice("attach")},
			{Key: 'w', Label: "New window", Action: func() error { return a.startShellMode("window", false, false, "") }, ShiftAction: a.defaultShellChoice("window")},
			{Key: 't', Label: "tmux window", Action: func() error { return a.startShellMode("tmux", false, false, "") }, ShiftAction: a.defaultShellChoice("tmux")},
			{Key: 'n', Label: "New guest shell · attach here", Action: func() error { return a.startRawShell(false, true, "") }},
			{Key: 'h', Label: "Host shell in node directory", Action: func() error { return a.startRawShell(true, false, "") }},
			{Key: 'e', Label: "Embedded guest shell", Action: func() error { return a.openEmbeddedShell(false) }, ShiftAction: a.defaultShellChoice("embedded")},
		},
	}
	if a.sessions != nil {
		for index, key := range a.sessions.TargetSessionKeys(a.state.selectedEntry().key()) {
			session := a.sessions.sessions[key]
			if session == nil {
				continue
			}
			id, host := string(session.terminalID), session.shellKind == terminal.NodeHostShell
			label := coalesce(session.label, session.shellKind.String())
			shortcut := rune(0)
			if index < 9 {
				shortcut = rune('1' + index)
			}
			menu.Entries = append(menu.Entries, tuiMenuEntry{Key: shortcut, Label: "Existing · " + label + " · " + id,
				Action: func() error { return a.startRawShell(host, false, id) }})
		}
	}
	a.overlay = menu
}

func (a *vaxisTUIApp) defaultShellChoice(mode string) func() error {
	return func() error {
		return a.saveManagerSetting("shell.mode", mode, func(app *vaxisTUIApp) error { return app.startShellMode(mode, false, false, "") })
	}
}

func (a *vaxisTUIApp) fleetNodeAction(action tuiActionID) error {
	targets := a.fleet.targets()
	if len(targets) == 0 {
		return fmt.Errorf("select a node first")
	}
	if len(targets) == 1 && len(a.fleet.marks) == 0 {
		return a.performAction(tuiActionSpec{ID: action})
	}
	verb := strings.TrimPrefix(string(action), "node.")
	expected := strconv.Itoa(len(targets))
	labels, keys := make([]string, 0, len(targets)), make([]string, 0, len(targets))
	for _, record := range targets {
		labels = append(labels, record.Node.Slug)
		keys = append(keys, terminal.NodeTarget(record.Node.ID).String())
	}
	fields := []tuiDialogField{}
	if action == tuiActionNodeDelete {
		fields = append(fields, newTUIInputField("confirm", "Type "+expected+" to delete these nodes", "", true))
	}
	a.overlay = newTUIDialog(fmt.Sprintf("%s %d nodes", verb, len(targets)), verb, labels, fields, func(values map[string]string) error {
		if action == tuiActionNodeDelete && values["confirm"] != expected {
			return fmt.Errorf("type %s to confirm deletion", expected)
		}
		return a.startOperation(tuiOperationRequest{Title: fmt.Sprintf("%s %d nodes", verb, len(targets)), ResourceKeys: keys, EntryKeys: keys,
			Run: func(ctx context.Context, service *Service) (tuiOperationResult, error) {
				for _, record := range targets {
					var err error
					switch action {
					case tuiActionNodeStart:
						_, err = service.NodeStart(ctx, record.Node.ID)
					case tuiActionNodeStop:
						_, err = service.NodeStop(ctx, record.Node.ID)
					case tuiActionNodeDelete:
						_, err = service.NodeDelete(ctx, record.Node.ID)
					}
					if err != nil {
						return tuiOperationResult{}, fmt.Errorf("%s %s: %w", verb, record.Node.Slug, err)
					}
				}
				return tuiOperationResult{ReloadData: true, Status: fmt.Sprintf("%s completed for %d nodes", verb, len(targets)), Apply: func(a *vaxisTUIApp) error { a.fleet.clearMarks(); return nil }}, nil
			},
		})
	})
	return nil
}
