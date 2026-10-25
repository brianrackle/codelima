package codelima

func (a *vaxisTUIApp) recipeDependencyWarning(env EnvironmentConfig, command int) (environmentDependencyWarning, bool) {
	for _, warning := range a.managerData.recipes.dependencies[env.ID].Warnings {
		if warning.Command == command {
			return warning, true
		}
	}
	return environmentDependencyWarning{}, false
}

func (a *vaxisTUIApp) openRecipeDependencyFix(env EnvironmentConfig, command int) bool {
	warning, ok := a.recipeDependencyWarning(env, command)
	if !ok {
		return false
	}
	draft, ok := applyEnvironmentDependencyFix(env.BootstrapCommands, warning)
	if !ok {
		return false
	}
	editor := a.openEnvironmentEditor(env, false)
	editor.setCommands(draft)
	editor.openCommands(warning.Fix.Command)
	return true
}

func (a *vaxisTUIApp) environmentCurlProviders(env EnvironmentConfig) []string {
	var providers []string
	for _, candidate := range a.managerData.environments {
		report := a.managerData.recipes.dependencies[candidate.ID]
		if candidate.ID != env.ID && report.CurlInstalled && len(report.Unavailable) == 0 {
			providers = append(providers, candidate.Slug)
		}
	}
	return providers
}
