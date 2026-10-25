package codelima

import "go.rockorager.dev/vaxis"

// tuiOverlayKeys supplies the effective snapshot to standalone widgets. The app
// binds it before input and drawing, including nested dialogs; tests and other
// callers without a frontend get the same compiled defaults.
type tuiOverlayKeys struct{ bindings *compiledKeyMap }

func (k *tuiOverlayKeys) setKeyBindings(bindings *compiledKeyMap) { k.bindings = bindings }
func (k *tuiOverlayKeys) overlayKeys() *compiledKeyMap {
	if k.bindings != nil {
		return k.bindings
	}
	return defaultTUIKeyMap()
}
func (k *tuiOverlayKeys) bindingHint(action string) string { return k.overlayKeys().Hint(action) }

func bindOverlayKeys(overlay tuiOverlay, bindings *compiledKeyMap) {
	if target, ok := overlay.(interface{ setKeyBindings(*compiledKeyMap) }); ok {
		target.setKeyBindings(bindings)
	}
	switch view := overlay.(type) {
	case *tuiCreateNodeForm:
		view.dialog.setKeyBindings(bindings)
	case *tuiTemplateEditor:
		view.dialog.setKeyBindings(bindings)
	case *tuiEnvironmentEditor:
		view.dialog.setKeyBindings(bindings)
	case *tuiNodeConfirmation:
		view.dialog.setKeyBindings(bindings)
	case *tuiBatchReview:
		view.dialog.setKeyBindings(bindings)
	}
}

func overlayKeyAction(overlay tuiOverlay, key vaxis.Key) string {
	m := defaultTUIKeyMap()
	if provider, ok := overlay.(interface{ overlayKeys() *compiledKeyMap }); ok {
		m = provider.overlayKeys()
	}
	return m.Action(overlayBindingContext(overlay), key)
}

func overlayBindingContext(overlay tuiOverlay) string {
	switch view := overlay.(type) {
	case *tuiDialog:
		if view.confirmDiscard {
			return "discard"
		}
		return "dialog"
	case *tuiCreateNodeForm:
		if view.dialog.confirmDiscard {
			return "discard"
		}
		if view.dialog.Fields[view.dialog.FieldIndex].Input == nil {
			return "create.choice"
		}
		return "create.input"
	case *tuiTemplateEditor:
		if view.dialog.confirmDiscard {
			return "discard"
		}
		if view.preview {
			return "template.preview"
		}
		if view.dialog.FieldIndex >= 3 {
			return "template.choice"
		}
		return "template.input"
	case *tuiEnvironmentEditor:
		if view.dialog.confirmDiscard {
			return "discard"
		}
		return "environment.editor"
	case *tuiCommandEditor:
		if view.confirmDiscard {
			return "discard.command"
		}
		return "command.editor"
	case *tuiTemplatePicker:
		return "template.picker"
	case *tuiSelector:
		return "selector"
	case *tuiMenu:
		if view.keyContext != "" {
			return view.keyContext
		}
		if len(view.Entries) > 0 && view.Entries[0].BindingAction != "" {
			for _, entry := range view.overlayKeys().info {
				if entry.Action == view.Entries[0].BindingAction {
					return entry.Contexts[0]
				}
			}
		}
		return "menu"
	case *tuiNodeConfirmation:
		if view.action == tuiActionNodeStop {
			return "review.stop"
		}
		if view.clone != nil {
			if view.clone.checkFocus {
				return "review.clone.choice"
			}
			return "review.clone.input"
		}
		return "review.input"
	case *tuiBatchReview:
		if len(view.dialog.Fields) == 0 {
			return "review.batch"
		}
		return "review.input"
	case *tuiPalette:
		return "palette"
	case *tuiKeyHelp:
		if view.searching {
			return "help.search"
		}
		return "help"
	case *tuiShellChooser:
		return "chooser"
	case *tuiDaemonLogView:
		return "daemon.log"
	case *tuiOperationOutputView:
		return "operation.output"
	case *tuiMessagesView:
		return "messages"
	default:
		return "text"
	}
}
