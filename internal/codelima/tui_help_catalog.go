package codelima

// This catalogue describes the manager's existing dispatchers. Help is a reader,
// never an action registry: searching or selecting a binding cannot execute it.
type tuiHelpBinding struct{ key, description string }
type tuiHelpSection struct {
	title    string
	bindings []tuiHelpBinding
}
type tuiHelpGroup struct {
	title    string
	sections []tuiHelpSection
}

func managerHelpGroups() []tuiHelpGroup {
	return []tuiHelpGroup{
		{"NODES", []tuiHelpSection{{"", []tuiHelpBinding{
			{"↑↓ j k", "move"},
			{"Enter", "open detail"},
			{"s", "start / stop selected or marked"},
			{"r", "restart; unmarked failed node retries failed step"},
			{"x", "shell (shell.mode)"},
			{"X", "shell chooser"},
			{"n", "new node"},
			{"c", "clone"},
			{"d", "delete"},
			{"l", "logs"},
			{"o", "open first live HTTP port"},
			{"y", "copy name"},
			{"Space", "mark node"},
			{"*", "mark all shown"},
			{"/", "filter: status: template: dir: port:"},
			{"< >", "sort column"},
			{"=", "reverse sort"},
			{"G", "group by directory"},
		}}}},
		{"NODE DETAIL", []tuiHelpSection{
			{"", []tuiHelpBinding{{"Tab ⇧Tab", "next / previous tab"},
				{"Esc / q", "back to Nodes"},
				{"s / r", "start-stop / restart (failed: retry)"},
				{"x / X", "shell / shell chooser"}}},
			{"OVERVIEW", []tuiHelpBinding{{"Ctrl+↑↓", "focus panel"},
				{"↑↓ j k", "select or scroll panel"},
				{"t / T", "new guest / host shell"},
				{"Enter", "attach selected session"},
				{"K", "end selected session (confirm)"},
				{"o / y", "open / copy selected HTTP port"}}},
			{"METRICS", []tuiHelpBinding{{"+ −", "time window: 1m 5m 15m 1h"},
				{"p", "sort processes: CPU / memory"},
				{"Space", "pause samples"},
				{"y", "copy selected PID"}}},
			{"LOGS", []tuiHelpBinding{{"[ ]", "source"},
				{"f / w", "follow / wrap"},
				{"L", "severity"},
				{"/", "search"},
				{"n / N", "next / previous match"},
				{"g / G", "top / end"},
				{"y / o", "copy line / open PAGER"}}},
			{"SPEC", []tuiHelpBinding{{"←→ Ctrl+↑↓", "focus panel"},
				{"Enter", "open bootstrap command output"},
				{"N / e", "new from template / edit template"},
				{"v", "Lima YAML pager"},
				{"y", "copy YAML path"}}},
		}},
		{"TEMPLATES & FORMS", []tuiHelpSection{
			{"", []tuiHelpBinding{{"Tab", "templates / environments"},
				{"n e c d", "new edit clone delete"},
				{"N", "node from template"},
				{"R", "restore built-in"},
				{"t", "test-run environment"},
				{"←→ / F2", "list / details focus"},
				{"PgUp PgDn", "page list / details"}}},
			{"IN FORMS", []tuiHelpBinding{{"Tab ⇧Tab", "next / previous field"},
				{"Enter / →", "open choices"},
				{"←→", "step a value"},
				{"Space", "toggle"},
				{"Shift+↑↓", "reorder commands"},
				{"Ctrl+s", "submit"},
				{"Esc", "cancel / review discard"}}},
		}},
		{"EVERYWHERE & SHELL", []tuiHelpSection{
			{"MANAGER FOCUS", []tuiHelpBinding{{"1–5", "switch view"},
				{":", "command palette"},
				{"?", "this help"},
				{"0", "scope: directory / all"},
				{"a / g", "templates / environments (g: outside detail)"},
				{"m", "activity"},
				{"Esc", "back / close / clear"},
				{"q", "quit at top level"}}},
			{"ATTACHED SHELL", []tuiHelpBinding{{"Ctrl+]", "detach, keep session running"},
				{"exit", "end session"}}},
			{"EMBEDDED SHELL", []tuiHelpBinding{{"F6", "TUI / terminal focus; other keys reach focused shell"},
				{"[ ]", "previous / next tab (TUI focus)"},
				{"t / T", "new guest / host (TUI focus)"},
				{"w", "close tab (TUI focus)"},
				{"F7", "search history (TUI focus)"}}},
		}},
		{"PORTS", []tuiHelpSection{{"", []tuiHelpBinding{{"↑↓ j k", "select route"},
			{"o", "open HTTP"},
			{"y", "copy address"},
			{"p", "pin bare localhost"},
			{"Enter", "go to node"},
			{"/", "filter"},
			{"C", "refresh"},
			{"PgUp PgDn", "scroll route details and compact By node list"}}}}},
		{"ACTIVITY", []tuiHelpSection{{"", []tuiHelpBinding{{"↑↓ j k", "move"},
			{"Enter", "open operation, log or session"},
			{"K", "cancel running operation"},
			{"f", "severity level"},
			{"/", "filter"},
			{"y", "copy selected record"},
			{"[ ]", "newer / older page"},
			{"g", "top"},
			{"PgUp PgDn", "page rows"}}}}},
		{"SYSTEM", []tuiHelpSection{{"", []tuiHelpBinding{{"Tab ⇧Tab", "next / previous panel"},
			{"C", "check / refresh"},
			{"R", "Doctor repair"},
			{"Enter", "selected Doctor / daemon / settings action"},
			{"u", "update daemon, keep sessions"},
			{"S", "restart daemon; ends live shell processes"},
			{"L", "open daemon log"},
			{"D", "diagnose freeze; save local evidence"}}}}},
	}
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
	groups := managerHelpGroups()
	for i, group := range groups {
		if group.title == context {
			copy(groups[1:i+1], groups[:i])
			groups[0] = group
			break
		}
	}
	a.overlay = newTUIKeyHelp(a, groups)
}
