package codelima

import "slices"

var tuiMenuKeyContexts = []string{"menu", "menu.configuration.list", "menu.configuration", "menu.environment.list", "menu.environment.commands", "menu.command.move"}

func tuiOverlayKeyActionDefinitions() ([]keyActionDefinition, []string) {
	forms := []string{"dialog", "create.input", "create.choice", "template.input", "template.choice", "template.preview", "environment.editor"}
	reviews := []string{"review.input", "review.stop", "review.clone.input", "review.clone.choice", "review.batch"}
	readers := []string{"text", "daemon.log", "operation.output", "messages", "help"}
	inputs := []string{"command.editor", "template.picker", "palette", "help.search"}
	all := slices.Concat(forms, reviews, readers, inputs, tuiMenuKeyContexts, []string{"selector", "chooser", "discard", "discard.command"})
	var definitions []keyActionDefinition
	add := func(id, description string, defaults, contexts []string, textInput bool) {
		definitions = append(definitions, keyActionDefinition{ID: id, Description: description, Defaults: slices.Clone(defaults), Contexts: slices.Clone(contexts), TextInput: textInput})
	}
	add("dialog.cancel", "Cancel or close overlay", []string{"esc"}, withoutKeyContexts(all, "discard", "discard.command"), true)
	definitions[len(definitions)-1].Required = true
	add("dialog.submit", "Submit or save form", []string{"ctrl+s"}, slices.Concat(forms, reviews, []string{"command.editor"}), true)
	add("dialog.accept", "Open or confirm selection", []string{"enter"}, slices.Concat(withoutKeyContexts(forms, "template.preview"), reviews, tuiMenuKeyContexts, []string{"selector", "template.picker", "palette", "chooser"}), true)
	add("dialog.next", "Next field or choice", []string{"tab"}, slices.Concat(withoutKeyContexts(forms, "template.preview"), []string{"selector", "template.picker", "command.editor"}), true)
	add("dialog.previous", "Previous field or command", []string{"shift+tab"}, slices.Concat(withoutKeyContexts(forms, "template.preview"), []string{"command.editor"}, []string{"review.clone.input", "review.clone.choice"}), true)
	add("review.next", "Next clone option", []string{"tab"}, []string{"review.clone.input", "review.clone.choice"}, true)
	updown := slices.Concat(forms, []string{"selector", "template.picker", "palette", "chooser", "help.search"}, tuiMenuKeyContexts)
	add("dialog.up", "Move up in a form or list", []string{"up"}, updown, true)
	add("dialog.down", "Move down in a form or list", []string{"down"}, updown, true)
	add("reader.up", "Scroll up", []string{"up", "k"}, readers, false)
	add("reader.down", "Scroll down", []string{"down", "j"}, readers, false)
	pages := slices.Concat(reviews, readers, []string{"template.preview", "palette", "chooser", "help.search"})
	add("overlay.page_up", "Page up in overlay", []string{"pageup"}, pages, true)
	add("overlay.page_down", "Page down in overlay", []string{"pagedown"}, pages, true)
	ends := slices.Concat(readers, []string{"chooser", "review.stop"})
	add("overlay.first", "First row", []string{"home"}, ends, false)
	add("overlay.last", "Last row", []string{"end"}, ends, false)
	add("reader.top", "First output row", []string{"g"}, []string{"operation.output", "messages"}, false)
	add("reader.follow", "Follow output", []string{"G"}, []string{"operation.output", "messages"}, false)
	add("messages.page_back", "Previous messages page", []string{"ctrl+b"}, []string{"messages"}, false)
	add("messages.page_forward", "Next messages page", []string{"ctrl+f", "space"}, []string{"messages"}, false)
	add("dialog.right", "Open choices or increase value", []string{"right"}, []string{"dialog", "create.input", "create.choice", "template.choice", "environment.editor", "template.picker"}, true)
	add("dialog.left", "Decrease value", []string{"left"}, []string{"template.choice"}, true)
	add("dialog.toggle", "Toggle selected option", []string{"space"}, []string{"create.choice", "template.choice", "selector", "review.clone.choice"}, false)
	add("selector.clear", "Clear selected choices", []string{"ctrl+u"}, []string{"selector"}, false)
	add("form.restore", "Restore built-in defaults", []string{"ctrl+r"}, []string{"template.input", "template.choice", "template.preview", "environment.editor"}, true)
	add("form.reload", "Reload template", []string{"ctrl+l"}, []string{"template.input", "template.choice", "template.preview"}, true)
	add("form.preview", "Toggle template preview", []string{"f2"}, []string{"template.input", "template.choice", "template.preview"}, true)
	add("form.reorder_up", "Move selected environment up", []string{"shift+up"}, []string{"template.choice"}, false)
	add("form.reorder_down", "Move selected environment down", []string{"shift+down"}, []string{"template.choice"}, false)
	for _, action := range []struct{ id, description, key string }{
		{"add", "Add command", "ctrl+n"}, {"delete", "Remove command", "ctrl+x"}, {"move_up", "Move command up", "alt+up"}, {"move_down", "Move command down", "alt+down"},
	} {
		add("command."+action.id, action.description, []string{action.key}, []string{"command.editor"}, true)
	}
	add("discard.confirm", "Discard unsaved changes", []string{"y"}, []string{"discard", "discard.command"}, false)
	add("discard.cancel", "Keep editing", []string{"n", "esc"}, []string{"discard", "discard.command"}, false)
	definitions[len(definitions)-1].Required = true
	add("discard.keep", "Keep editing", []string{"enter"}, []string{"discard"}, false)
	add("review.run", "Run reviewed operation", []string{"s"}, []string{"review.stop", "review.batch"}, false)
	add("review.skip_idle", "Toggle skipping idle sessions", []string{"space"}, []string{"review.stop"}, false)
	add("review.attach", "Inspect session before stopping", []string{"x"}, []string{"review.stop"}, false)
	add("palette.complete", "Complete selected command name", []string{"tab"}, []string{"palette"}, true)
	add("help.close", "Close keyboard help", []string{"?"}, []string{"help"}, false)
	add("help.search", "Search keyboard help", []string{"/"}, []string{"help"}, false)
	add("help.next", "Next group; leave search", []string{"tab"}, []string{"help", "help.search"}, true)
	add("help.previous", "Previous group; leave search", []string{"shift+tab"}, []string{"help", "help.search"}, true)
	add("help.left", "Previous help group", []string{"left"}, []string{"help"}, false)
	add("help.right", "Next help group", []string{"right"}, []string{"help"}, false)
	add("daemon.log_reload", "Reload daemon log", []string{"C"}, []string{"daemon.log"}, false)
	add("daemon.log_pager", "Open daemon log in pager", []string{"o"}, []string{"daemon.log"}, false)
	add("operation.cancel", "Cancel displayed operation", []string{"K"}, []string{"operation.output"}, false)
	add("chooser.save_selected", "Open selection and save preferred mode", []string{"shift+enter"}, []string{"chooser"}, false)
	for _, action := range []struct{ id, key string }{{"attach", "a"}, {"window", "w"}, {"tmux", "t"}, {"host", "h"}, {"embedded", "e"}} {
		defaults := []string{action.key}
		if action.id == "attach" {
			defaults = append(defaults, "n")
		}
		add("chooser."+action.id, "Open "+action.id+" shell", defaults, []string{"chooser"}, false)
		for i := range defaults {
			defaults[i] = "shift+" + defaults[i]
		}
		add("chooser.save_"+action.id, "Open "+action.id+" shell and save mode where supported", defaults, []string{"chooser"}, false)
	}
	for i := range 9 {
		key := string(rune('1' + i))
		add("chooser.session_"+key, "Attach chooser session "+key, []string{key}, []string{"chooser"}, false)
	}
	for _, action := range []struct{ id, key, context string }{
		{"configuration_create", "c", "menu.configuration.list"}, {"configuration_manage", "m", "menu.configuration.list"},
		{"configuration_edit", "u", "menu.configuration"}, {"configuration_clone", "c", "menu.configuration"}, {"configuration_delete", "d", "menu.configuration"},
		{"environment_create", "c", "menu.environment.list"}, {"environment_manage", "m", "menu.environment.list"},
		{"command_add", "a", "menu.environment.commands"}, {"command_remove", "r", "menu.environment.commands"}, {"command_move", "m", "menu.environment.commands"}, {"command_clear", "c", "menu.environment.commands"}, {"environment_delete", "d", "menu.environment.commands"},
		{"command_up", "u", "menu.command.move"}, {"command_down", "d", "menu.command.move"},
	} {
		add("menu."+action.id, "Menu: "+action.id, []string{action.key}, []string{action.context}, false)
	}
	return definitions, all
}
