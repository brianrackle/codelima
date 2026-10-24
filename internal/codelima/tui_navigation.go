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
	a.view, a.nodeDetail, a.detailScroll = view, false, 0
	a.state.focusTree()
	a.state.treePaneMode = tuiTreePaneModeInfo
	a.filterInput = nil
	a.loadManagerData(false)
	a.syncSessionFocus()
}

func (a *vaxisTUIApp) openNodeDetail(tab tuiDetailTab) {
	if !a.state.selectedEntry().valid() {
		return
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
	a.refreshFleet()
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
		if a.view == tuiViewTemplates {
			a.environmentsView = !a.environmentsView
		} else if a.nodeDetail {
			delta, count := 1, 4
			if len(a.state.sessions.TargetSessionKeys(a.state.selectedEntry().key())) > 0 {
				count = 5
			}
			if key.Modifiers&vaxis.ModShift != 0 {
				delta = -1
			}
			a.detailTab = tuiDetailTab((int(a.detailTab) + delta + count) % count)
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
		if selected, ok := a.fleetSelected(); ok && selected.Status == "running" {
			action = tuiActionNodeStop
		}
		err = a.fleetNodeAction(action)
	case key.MatchString("y"):
		err = a.copyToHostClipboard(a.state.selectedEntry().node.Slug)
	case key.MatchString("X"):
		a.openShellChooser()
	case key.MatchString("x"):
		err = a.startRawShell(false, false, "")
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
	switch {
	case a.view == tuiViewTemplates:
		if a.environmentsView {
			a.environmentIndex = max(0, min(len(a.managerData.environments)-1, a.environmentIndex+delta))
		} else {
			a.templateIndex = max(0, min(len(a.managerData.configurations)-1, a.templateIndex+delta))
		}
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
			a.openCreateEnvironmentConfigDialog()
		} else if a.environmentIndex < len(a.managerData.environments) {
			environment := a.managerData.environments[a.environmentIndex]
			switch {
			case key.MatchString("e"), key.MatchString("Enter"):
				a.openEnvironmentConfigCommandMenu(environment)
			case key.MatchString("c"):
				a.openCloneEnvironmentDialog(environment)
			case key.MatchString("d"):
				a.openDeleteEnvironmentConfigDialog(environment)
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
		if err := a.openCreateNodeDialog(); err != nil {
			return err
		}
		if dialog := a.activeDialog(); dialog != nil {
			dialog.SetFieldValue("configuration", configuration.Slug)
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
		Description: []string{"Ctrl+] detaches from an attached shell; the session keeps running.", "F6 returns from an embedded shell to the manager."},
		Entries: []tuiMenuEntry{
			{Key: 'a', Label: "Attach here", Action: func() error { return a.startRawShell(false, false, "") }},
			{Key: 'n', Label: "New guest shell · attach here", Action: func() error { return a.startRawShell(false, true, "") }},
			{Key: 'h', Label: "Host shell in node directory", Action: func() error { return a.startRawShell(true, false, "") }},
			{Key: 'e', Label: "Embedded guest shell", Action: func() error { return a.openEmbeddedShell(false) }},
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

func (a *vaxisTUIApp) openCloneEnvironmentDialog(environment EnvironmentConfig) {
	a.overlay = newTUIDialog("Clone environment "+environment.Slug, "Clone", nil,
		[]tuiDialogField{newTUIInputField("slug", "Name", environment.Slug+"-copy", true)},
		func(values map[string]string) error {
			return a.startMetadataOperation("Cloning environment "+environment.Slug, "environments", func(ctx context.Context, service *Service) (tuiOperationResult, error) {
				_, err := service.EnvironmentConfigCreate(ctx, EnvironmentConfigCreateInput{Slug: values["slug"], BootstrapCommands: environment.BootstrapCommands})
				return tuiOperationResult{Status: "Cloned environment", Apply: func(a *vaxisTUIApp) error { a.loadManagerData(false); return nil }}, err
			})
		})
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
