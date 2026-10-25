package codelima

import "slices"

// Contexts are mutually exclusive focused surfaces. Reserving node commands on
// every detail tab lets lifecycle guards keep their state-dependent behavior.
func tuiKeyActionDefinitions() []keyActionDefinition {
	overview := []string{"overview.sessions", "overview.ports", "overview.recent", "overview.identity", "overview.resources"}
	detail := slices.Concat(overview, []string{"detail.starting", "detail.failed", "detail.metrics", "detail.logs", "detail.spec", "detail.shell"})
	nodes := slices.Concat([]string{"nodes", "nodes.empty"}, detail)
	recipes := []string{"templates", "environments"}
	manager := slices.Concat(nodes, recipes, []string{"ports", "activity", "system"})
	input := []string{"filter", "terminal.search"}
	var definitions []keyActionDefinition
	add := func(id, description string, defaults, contexts []string) {
		definitions = append(definitions, keyActionDefinition{ID: id, Description: description, Defaults: slices.Clone(defaults), Contexts: slices.Clone(contexts)})
	}
	add("app.quit", "Quit CodeLima", []string{"ctrl+c"}, slices.Concat(manager, input, []string{"overlay"}))
	definitions[len(definitions)-1].Required, definitions[len(definitions)-1].TextInput = true, true
	add("terminal.focus", "Switch manager / embedded terminal focus", []string{"f6"}, slices.Concat(manager, []string{"terminal"}))
	definitions[len(definitions)-1].Required, definitions[len(definitions)-1].TextInput = true, true
	add("navigation.back", "Back, close or clear selection", []string{"esc"}, manager)
	definitions[len(definitions)-1].Required = true
	add("navigation.leave", "Back from detail; quit at top level", []string{"q"}, manager)
	add("navigation.up", "Move up", []string{"up", "k"}, manager)
	add("navigation.down", "Move down", []string{"down", "j"}, manager)
	add("navigation.next_tab", "Next tab or panel", []string{"tab"}, slices.Concat(detail, recipes, []string{"system"}))
	add("navigation.previous_tab", "Previous tab or panel", []string{"shift+tab"}, slices.Concat(detail, recipes, []string{"system"}))
	pageContexts := slices.Concat(recipes, []string{"nodes.empty", "detail.logs", "detail.spec", "ports", "activity"})
	add("navigation.page_up", "Page up", []string{"pageup"}, pageContexts)
	add("navigation.page_down", "Page down", []string{"pagedown"}, pageContexts)
	for i, name := range []string{"nodes", "recipes", "ports", "activity", "system"} {
		defaults := []string{string(rune('1' + i))}
		if name == "activity" {
			defaults = append(defaults, "m")
		}
		add("view."+name, "Open "+name, defaults, manager)
	}
	add("view.templates", "Open Templates", []string{"a"}, manager)
	add("view.environments", "Open Environments", []string{"g"}, []string{"nodes", "nodes.empty", "templates", "environments", "ports", "system"})
	add("scope.toggle", "Toggle directory / all scope", []string{"0"}, manager)
	add("palette.open", "Open command palette", []string{":"}, manager)
	add("help.open", "Open keyboard help", []string{"?"}, manager)
	add("node.open", "Open node detail", []string{"enter"}, []string{"nodes", "nodes.empty", "detail.starting", "detail.failed", "detail.metrics", "detail.logs", "detail.shell"})
	for _, action := range []struct{ id, description, key string }{
		{"clone", "Review node clone", "c"}, {"delete", "Review node deletion", "d"},
		{"toggle", "Start or stop node", "s"}, {"restart", "Restart node; retry a failed step", "r"},
		{"logs", "Open node logs", "l"}, {"shell", "Open preferred shell", "x"}, {"shell_chooser", "Choose a shell", "X"},
	} {
		add("node."+action.id, action.description, []string{action.key}, nodes)
	}
	add("node.create", "Create a node", []string{"n"}, withoutKeyContexts(nodes, "detail.logs"))
	add("node.copy_name", "Copy node name", []string{"y"}, withoutKeyContexts(nodes, "overview.ports", "detail.metrics", "detail.logs", "detail.spec"))
	fleet := []string{"nodes", "nodes.empty"}
	add("fleet.filter", "Filter nodes", []string{"/"}, fleet)
	add("fleet.group", "Group nodes by directory", []string{"G"}, fleet)
	add("fleet.sort_previous", "Previous sort column", []string{"<"}, fleet)
	add("fleet.sort_next", "Next sort column", []string{">"}, fleet)
	add("fleet.reverse", "Reverse sort", []string{"="}, fleet)
	add("fleet.mark", "Mark selected node", []string{"space"}, fleet)
	add("fleet.mark_all", "Mark visible nodes", []string{"*"}, fleet)
	add("fleet.recheck", "Recheck empty-fleet prerequisites", []string{"R"}, []string{"nodes.empty"})
	add("node.open_port", "Open first HTTP port", []string{"o"}, fleet)
	add("overview.previous_panel", "Previous Overview panel", []string{"ctrl+up"}, overview)
	add("overview.next_panel", "Next Overview panel", []string{"ctrl+down"}, overview)
	add("overview.open", "Open selected session, route or detail", []string{"enter"}, overview)
	add("session.end", "Review ending selected session", []string{"K"}, []string{"overview.sessions"})
	add("session.guest", "New guest shell", []string{"t"}, slices.Concat(overview, []string{"detail.shell"}))
	add("session.host", "New host shell", []string{"T"}, detail)
	add("route.open", "Open selected HTTP route", []string{"o"}, []string{"overview.ports", "ports"})
	add("route.copy", "Copy selected route address", []string{"y"}, []string{"overview.ports", "ports"})
	for _, action := range []struct{ id, description, key string }{
		{"previous_tab", "Previous embedded shell tab", "["}, {"next_tab", "Next embedded shell tab", "]"},
		{"close_tab", "Close embedded shell tab", "w"}, {"search", "Search embedded history", "f7"},
	} {
		add("terminal."+action.id, action.description, []string{action.key}, []string{"detail.shell"})
	}
	for _, action := range []struct{ id, description, key string }{
		{"window_more", "Longer metrics window", "plus"}, {"window_less", "Shorter metrics window", "-"},
		{"pause", "Pause or resume metrics", "space"}, {"sort", "Sort processes by CPU / memory", "p"}, {"copy_pid", "Copy selected PID", "y"},
	} {
		add("metrics."+action.id, action.description, []string{action.key}, []string{"detail.metrics"})
	}
	for _, action := range []struct{ id, description, key string }{
		{"source_previous", "Previous log source", "["}, {"source_next", "Next log source", "]"},
		{"follow", "Follow logs", "f"}, {"search", "Search logs", "/"}, {"match_next", "Next log match", "n"},
		{"match_previous", "Previous log match", "N"}, {"wrap", "Wrap log lines", "w"}, {"level", "Filter log severity", "L"},
		{"copy", "Copy selected log line", "y"}, {"pager", "Open logs in pager", "o"},
	} {
		add("logs."+action.id, action.description, []string{action.key}, []string{"detail.logs"})
	}
	add("logs.first", "Go to first log line", []string{"g", "home"}, []string{"detail.logs"})
	add("logs.last", "Follow last log line", []string{"G", "end"}, []string{"detail.logs"})
	add("spec.previous_panel", "Previous Spec panel", []string{"left", "ctrl+up"}, []string{"detail.spec"})
	add("spec.next_panel", "Next Spec panel", []string{"right", "ctrl+down"}, []string{"detail.spec"})
	for _, action := range []struct{ id, description, key string }{
		{"first", "First Spec row", "home"}, {"last", "Last Spec row", "end"},
		{"new_node", "New node from source template", "N"}, {"edit", "Edit source template", "e"},
		{"copy_path", "Copy Lima YAML path", "y"}, {"pager", "Open Lima YAML in pager", "v"}, {"open", "Open selected command output", "enter"},
	} {
		add("spec."+action.id, action.description, []string{action.key}, []string{"detail.spec"})
	}
	add("failure.edit_template", "Edit failed node's source template", []string{"E"}, []string{"detail.failed"})
	add("failure.edit_environment", "Edit failed step's source environment", []string{"e"}, []string{"detail.failed"})
	for _, action := range []struct{ id, description, key string }{
		{"new", "New template or environment", "n"}, {"edit", "Edit template or environment", "e"},
		{"open", "Open selected recipe or command", "enter"}, {"clone", "Clone recipe", "c"},
		{"delete", "Delete recipe", "d"}, {"restore", "Restore built-in recipe", "R"},
		{"focus_list", "Focus recipe list", "left"}, {"focus_details", "Focus recipe details", "right"}, {"focus_toggle", "Toggle recipe focus", "f2"},
	} {
		add("recipe."+action.id, action.description, []string{action.key}, recipes)
	}
	add("recipe.new_node", "Create node from template", []string{"N"}, []string{"templates"})
	for _, action := range []struct{ id, description, key string }{
		{"test", "Test environment", "t"}, {"command_add", "Append bootstrap command", "o"}, {"command_delete", "Remove selected command", "D"},
		{"command_editor", "Edit command externally", "E"}, {"command_up", "Move command up", "shift+up"}, {"command_down", "Move command down", "shift+down"},
	} {
		add("environment."+action.id, action.description, []string{action.key}, []string{"environments"})
	}
	for _, action := range []struct{ id, description, key string }{
		{"filter", "Filter routes", "/"}, {"refresh", "Refresh routes", "C"}, {"pin", "Pin bare localhost route", "p"}, {"open_node", "Open selected route's node", "enter"},
	} {
		add("ports."+action.id, action.description, []string{action.key}, []string{"ports"})
	}
	for _, action := range []struct{ id, description, key string }{
		{"filter", "Filter activity", "/"}, {"level", "Filter activity severity", "f"}, {"newer", "Newer activity page", "["},
		{"older", "Older activity page", "]"}, {"copy", "Copy activity record", "y"}, {"cancel", "Cancel selected operation", "K"}, {"open", "Open selected activity", "enter"},
	} {
		add("activity."+action.id, action.description, []string{action.key}, []string{"activity"})
	}
	add("activity.first", "Latest activity page", []string{"g", "home"}, []string{"activity"})
	for _, action := range []struct{ id, description, key string }{
		{"refresh", "Refresh System", "C"}, {"repair", "Repair Doctor findings", "R"}, {"open", "Open selected System item", "enter"},
		{"update", "Update daemon", "u"}, {"restart", "Restart daemon", "S"}, {"logs", "Open daemon logs", "L"}, {"diagnose", "Save local diagnostic evidence", "D"},
	} {
		add("system."+action.id, action.description, []string{action.key}, []string{"system"})
	}
	for _, action := range []struct{ id, description, key string }{
		{"cancel", "Cancel text search", "esc"}, {"accept", "Accept text search", "enter"},
	} {
		add("input."+action.id, action.description, []string{action.key}, []string{"filter"})
		definitions[len(definitions)-1].TextInput = true
		definitions[len(definitions)-1].Required = action.id == "cancel"
	}
	add("terminal.search_close", "Close history search", []string{"esc", "f7"}, []string{"terminal.search"})
	definitions[len(definitions)-1].Required, definitions[len(definitions)-1].TextInput = true, true
	add("terminal.search_next", "Next history match", []string{"enter"}, []string{"terminal.search"})
	definitions[len(definitions)-1].TextInput = true
	add("terminal.search_previous", "Previous history match", []string{"shift+enter"}, []string{"terminal.search"})
	definitions[len(definitions)-1].TextInput = true
	overlays, overlayContexts := tuiOverlayKeyActionDefinitions()
	definitions[0].Contexts = append(definitions[0].Contexts, overlayContexts...)
	return append(definitions, overlays...)
}

func withoutKeyContexts(contexts []string, excluded ...string) []string {
	return slices.DeleteFunc(slices.Clone(contexts), func(context string) bool { return slices.Contains(excluded, context) })
}
