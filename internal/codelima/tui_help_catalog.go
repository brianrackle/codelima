package codelima

import "strings"

// Help reads effective bindings. Searching or selecting a binding never runs it.
type tuiHelpBinding struct{ key, description, actions string }

func helpBinding(legend, description string) tuiHelpBinding {
	return tuiHelpBinding{key: legend, description: description, actions: legend}
}

type tuiHelpSection struct {
	title    string
	bindings []tuiHelpBinding
}
type tuiHelpGroup struct {
	title    string
	sections []tuiHelpSection
}

func managerHelpGroups(keys *compiledKeyMap) []tuiHelpGroup {
	groups := []tuiHelpGroup{
		{"NODES", []tuiHelpSection{{"", []tuiHelpBinding{
			helpBinding("{navigation.up navigation.down}", "move"),
			helpBinding("{node.open}", "open detail"),
			helpBinding("{node.toggle}", "start / stop selected or marked"),
			helpBinding("{node.restart}", "restart; unmarked failed node retries failed step"),
			helpBinding("{node.shell}", "shell (shell.mode)"),
			helpBinding("{node.shell_chooser}", "shell chooser"),
			helpBinding("{node.create}", "new node"),
			helpBinding("{node.clone}", "clone"),
			helpBinding("{node.delete}", "delete"),
			helpBinding("{node.logs}", "logs"),
			helpBinding("{node.open_port}", "open first live HTTP port"),
			helpBinding("{node.copy_name}", "copy name"),
			helpBinding("{fleet.mark}", "mark node"),
			helpBinding("{fleet.mark_all}", "mark all shown"),
			helpBinding("{fleet.filter}", "filter: status: template: dir: port:"),
			helpBinding("{fleet.sort_previous fleet.sort_next}", "sort column"),
			helpBinding("{fleet.reverse}", "reverse sort"),
			helpBinding("{fleet.group}", "group by directory"),
		}}}},
		{"NODE DETAIL", []tuiHelpSection{
			{"", []tuiHelpBinding{helpBinding("{navigation.next_tab navigation.previous_tab}", "next / previous tab"),
				helpBinding("{navigation.back navigation.leave}", "back to Nodes"),
				helpBinding("{node.toggle node.restart}", "start-stop / restart (failed: retry)"),
				helpBinding("{node.shell node.shell_chooser}", "shell / shell chooser")}},
			{"OVERVIEW", []tuiHelpBinding{helpBinding("{overview.previous_panel overview.next_panel}", "focus panel"),
				helpBinding("{navigation.up navigation.down}", "select or scroll panel"),
				helpBinding("{session.guest session.host}", "new guest / host shell"),
				helpBinding("{overview.open}", "attach selected session"),
				helpBinding("{session.end}", "end selected session (confirm)"),
				helpBinding("{route.open route.copy}", "open / copy selected HTTP port")}},
			{"METRICS", []tuiHelpBinding{helpBinding("{metrics.window_more metrics.window_less}", "time window: 1m 5m 15m 1h"),
				helpBinding("{metrics.sort}", "sort processes: CPU / memory"),
				helpBinding("{metrics.pause}", "pause samples"),
				helpBinding("{metrics.copy_pid}", "copy selected PID")}},
			{"LOGS", []tuiHelpBinding{helpBinding("{logs.source_previous logs.source_next}", "source"),
				helpBinding("{logs.follow logs.wrap}", "follow / wrap"),
				helpBinding("{logs.level}", "severity"),
				helpBinding("{logs.search}", "search"),
				helpBinding("{logs.match_next logs.match_previous}", "next / previous match"),
				helpBinding("{logs.first logs.last}", "top / end"),
				helpBinding("{logs.copy logs.pager}", "copy line / open PAGER")}},
			{"SPEC", []tuiHelpBinding{helpBinding("{spec.previous_panel spec.next_panel}", "focus panel"),
				helpBinding("{spec.open}", "open bootstrap command output"),
				helpBinding("{spec.new_node spec.edit}", "new from template / edit template"),
				helpBinding("{spec.pager}", "Lima YAML pager"),
				helpBinding("{spec.copy_path}", "copy YAML path")}},
		}},
		{"TEMPLATES & FORMS", []tuiHelpSection{
			{"", []tuiHelpBinding{helpBinding("{navigation.next_tab}", "templates / environments"),
				helpBinding("{recipe.new recipe.edit recipe.clone recipe.delete}", "new edit clone delete"),
				helpBinding("{recipe.new_node}", "node from template"),
				helpBinding("{recipe.restore}", "restore built-in"),
				helpBinding("{environment.test}", "test-run environment"),
				helpBinding("{recipe.focus_list recipe.focus_details recipe.focus_toggle}", "list / details focus"),
				helpBinding("{navigation.page_up navigation.page_down}", "page list / details")}},
			{"IN FORMS", []tuiHelpBinding{helpBinding("{dialog.next dialog.previous}", "next / previous field"),
				helpBinding("{dialog.accept dialog.right}", "open choices"),
				helpBinding("{dialog.left dialog.right}", "step a value"),
				helpBinding("{dialog.toggle}", "toggle"),
				helpBinding("{form.reorder_up form.reorder_down}", "reorder environments"),
				helpBinding("{dialog.submit}", "submit"),
				helpBinding("{dialog.cancel}", "cancel / review discard")}},
		}},
		{"EVERYWHERE & SHELL", []tuiHelpSection{
			{"MANAGER FOCUS", []tuiHelpBinding{helpBinding("{view.nodes view.recipes view.ports view.activity view.system}", "switch view"),
				helpBinding("{palette.open}", "command palette"),
				helpBinding("{help.open}", "this help"),
				helpBinding("{scope.toggle}", "scope: directory / all"),
				helpBinding("{view.templates view.environments}", "templates / environments (environments: outside detail)"),
				helpBinding("{view.activity}", "activity"),
				helpBinding("{navigation.back}", "back / close / clear"),
				helpBinding("{navigation.leave}", "quit at top level")}},
			{"ATTACHED SHELL", []tuiHelpBinding{helpBinding("Ctrl+]", "detach, keep session running"),
				helpBinding("exit", "end session")}},
			{"EMBEDDED SHELL", []tuiHelpBinding{helpBinding("{terminal.focus}", "TUI / terminal focus; other keys reach focused shell"),
				helpBinding("{terminal.previous_tab terminal.next_tab}", "previous / next tab (TUI focus)"),
				helpBinding("{terminal.move_tab_left terminal.move_tab_right}", "move tab left / right (TUI focus)"),
				helpBinding("{session.guest session.host}", "new guest / host (TUI focus)"),
				helpBinding("{terminal.close_tab}", "close tab (TUI focus)"),
				helpBinding("{terminal.search}", "search history (TUI focus)")}},
		}},
		{"PORTS", []tuiHelpSection{{"", []tuiHelpBinding{helpBinding("{navigation.up navigation.down}", "select route"),
			helpBinding("{route.open}", "open HTTP"),
			helpBinding("{route.copy}", "copy address"),
			helpBinding("{ports.pin}", "pin bare localhost"),
			helpBinding("{ports.open_node}", "go to node"),
			helpBinding("{ports.filter}", "filter"),
			helpBinding("{ports.refresh}", "refresh"),
			helpBinding("{navigation.page_up navigation.page_down}", "scroll route details and compact By node list")}}}},
		{"ACTIVITY", []tuiHelpSection{{"", []tuiHelpBinding{helpBinding("{navigation.up navigation.down}", "move"),
			helpBinding("{activity.open}", "open operation, log or session"),
			helpBinding("{activity.cancel}", "cancel running operation"),
			helpBinding("{activity.level}", "severity level"),
			helpBinding("{activity.filter}", "filter"),
			helpBinding("{activity.copy}", "copy selected record"),
			helpBinding("{activity.newer activity.older}", "newer / older page"),
			helpBinding("{activity.first}", "top"),
			helpBinding("{navigation.page_up navigation.page_down}", "page rows")}}}},
		{"SYSTEM", []tuiHelpSection{{"", []tuiHelpBinding{helpBinding("{navigation.next_tab navigation.previous_tab}", "next / previous panel"),
			helpBinding("{system.refresh}", "check / refresh"),
			helpBinding("{system.repair}", "Doctor repair"),
			helpBinding("{system.open}", "selected Doctor / daemon / settings action"),
			helpBinding("{system.update}", "update daemon, keep sessions"),
			helpBinding("{system.restart}", "restart daemon; ends live shell processes"),
			helpBinding("{system.logs}", "open daemon log"),
			helpBinding("{system.diagnose}", "diagnose freeze; save local evidence")}}}},
	}
	covered := map[string]bool{}
	for gi := range groups {
		for si := range groups[gi].sections {
			for bi := range groups[gi].sections[si].bindings {
				binding := &groups[gi].sections[si].bindings[bi]
				if strings.HasPrefix(binding.actions, "{") {
					for _, action := range strings.Fields(strings.Trim(binding.actions, "{}")) {
						covered[action] = true
					}
				}
				binding.key = keys.formatHints(binding.key, true)
			}
		}
	}
	more := tuiHelpSection{title: "MORE CONFIGURABLE ACTIONS"}
	for _, entry := range keys.Describe() {
		if covered[entry.Action] {
			continue
		}
		more.bindings = append(more.bindings, tuiHelpBinding{key: keys.Hint(entry.Action), description: entry.Description + " · " + entry.Action, actions: entry.Action + " " + strings.Join(entry.Contexts, " ")})
	}
	groups[3].sections = append(groups[3].sections, more)
	return groups

}

func (a *vaxisTUIApp) openKeyHelp() {
	context := "NODES"
	switch {
	case a.nodeDetail:
		context = "NODE DETAIL"
	case a.view == tuiViewTemplates:
		context = "TEMPLATES & FORMS"
	case a.view == tuiViewPorts:
		context = "PORTS"
	case a.view == tuiViewActivity:
		context = "ACTIVITY"
	case a.view == tuiViewSystem:
		context = "SYSTEM"
	}
	groups := managerHelpGroups(a.keyMap())
	for i, group := range groups {
		if group.title == context {
			copy(groups[1:i+1], groups[:i])
			groups[0] = group
			break
		}
	}
	a.overlay = newTUIKeyHelp(a, groups)
}
