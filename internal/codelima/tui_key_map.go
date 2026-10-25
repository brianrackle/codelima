package codelima

import (
	"log/slog"
	"sync"

	"go.rockorager.dev/vaxis"
)

var defaultTUIKeyMap = sync.OnceValue(func() *compiledKeyMap {
	m, err := compileKeyBindings(nil, tuiKeyActionDefinitions())
	if err != nil {
		panic(err)
	} // A conflicting built-in registry is a programming error.
	return m
})

func (a *vaxisTUIApp) keyMap() *compiledKeyMap {
	if a.keys == nil {
		a.applyKeyboardConfig(a.managerSettings())
	}
	return a.keys
}

func (a *vaxisTUIApp) applyKeyBindings(config KeyBindingConfig) {
	a.applyKeyboardConfig(Config{Keys: config})
}

func (a *vaxisTUIApp) applyKeyboardConfig(config Config) {
	m, err := compileConfiguredKeyBindings(config)
	if err != nil {
		if a.keys == nil {
			a.keys = defaultTUIKeyMap()
		}
		if a.keyBindingError != err.Error() {
			a.keyBindingError = err.Error()
			a.setStatus(slog.LevelError, "Keyboard settings rejected; keeping effective bindings: "+err.Error())
		}
		return
	}
	a.keys, a.keyBindingError = m, ""
}

func (a *vaxisTUIApp) keyAction(key vaxis.Key) string {
	return a.keyMap().Action(a.bindingContext(), key)
}

func (a *vaxisTUIApp) keyHint(action string) string { return a.keyMap().Hint(action) }

func (a *vaxisTUIApp) bindingContext() string {
	if a.overlay != nil {
		return "overlay"
	}
	if a.search != nil {
		return "terminal.search"
	}
	if a.filterInput != nil || a.logsVisible() && a.logs.input != nil || a.view == tuiViewActivity && a.activity.input != nil || a.view == tuiViewPorts && a.ports.input != nil {
		return "filter"
	}
	if a.state != nil && a.state.focus == tuiFocusTerminal {
		return "terminal"
	}
	switch a.view {
	case tuiViewTemplates:
		if a.environmentsView {
			return "environments"
		}
		return "templates"
	case tuiViewPorts:
		return "ports"
	case tuiViewActivity:
		return "activity"
	case tuiViewSystem:
		return "system"
	}
	if a.nodeDetail {
		switch a.detailTab {
		case tuiDetailMetrics:
			return "detail.metrics"
		case tuiDetailLogs:
			return "detail.logs"
		case tuiDetailSpec:
			return "detail.spec"
		case tuiDetailShell:
			return "detail.shell"
		default:
			if a.nodeStarting() {
				return "detail.starting"
			}
			if a.nodeLifecycleFailed() {
				return "detail.failed"
			}
			return []string{"overview.sessions", "overview.ports", "overview.recent", "overview.identity", "overview.resources"}[a.overview.panel]
		}
	}
	if a.emptyNodesVisible() {
		return "nodes.empty"
	}
	return "nodes"
}
