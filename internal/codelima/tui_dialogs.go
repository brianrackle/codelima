package codelima

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"github.com/brianrackle/codelima/internal/codelima/terminal"
)

func commaSeparatedValues(values []string) string {
	return strings.Join(values, ",")
}

func parseCommaSeparatedValues(value string) []string {
	if strings.TrimSpace(value) == "" {
		return []string{}
	}

	parts := strings.Split(value, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		values = append(values, part)
	}

	return values
}

func environmentConfigSelectionSummary(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

func (a *vaxisTUIApp) environmentConfigSelectorOptions() ([]tuiSelectorOption, error) {
	configs, err := a.service.EnvironmentConfigList(a.ctx, false)
	if err != nil {
		return nil, err
	}

	options := make([]tuiSelectorOption, 0, len(configs))
	for _, config := range configs {
		label := config.Slug
		if len(config.BootstrapCommands) > 0 {
			label = fmt.Sprintf("%s (%d bootstrap commands)", config.Slug, len(config.BootstrapCommands))
		}
		options = append(options, tuiSelectorOption{
			Label: label,
			Value: config.Slug,
		})
	}
	return options, nil
}

func (a *vaxisTUIApp) openEnvironmentConfigSelector(title string, description []string, current []string, multi bool, onSubmit func(values []string) error) error {
	options, err := a.environmentConfigSelectorOptions()
	if err != nil {
		return err
	}
	if !multi && len(options) == 0 {
		return fmt.Errorf("no environments configured")
	}
	if multi && len(options) == 0 {
		description = append(description, "No reusable environments configured. Press Enter to keep none assigned.")
	}
	selector := newTUISelector(title, description, options, current, multi, onSubmit)
	selector.EmptyText = "No environments configured."
	a.showSelector(selector)
	return nil
}

func commandSelectorOptions(commands []string) []tuiSelectorOption {
	options := make([]tuiSelectorOption, 0, len(commands))
	for index, command := range commands {
		options = append(options, tuiSelectorOption{
			Label: fmt.Sprintf("%d. %s", index+1, command),
			Value: strconv.Itoa(index),
		})
	}
	return options
}

func parseSelectorIndices(values []string, length int) ([]int, error) {
	indices := make([]int, 0, len(values))
	seen := map[int]bool{}
	for _, value := range values {
		index, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("invalid command selection")
		}
		if index < 0 || index >= length {
			return nil, fmt.Errorf("selected command is out of range")
		}
		if seen[index] {
			continue
		}
		indices = append(indices, index)
		seen[index] = true
	}
	sort.Ints(indices)
	return indices, nil
}

func removeCommandsByIndex(commands []string, indices []int) []string {
	if len(indices) == 0 {
		return append([]string(nil), commands...)
	}

	filtered := make([]string, 0, len(commands)-len(indices))
	selected := map[int]bool{}
	for _, index := range indices {
		selected[index] = true
	}
	for index, command := range commands {
		if selected[index] {
			continue
		}
		filtered = append(filtered, command)
	}
	return filtered
}

func moveCommand(commands []string, index int, delta int) []string {
	target := index + delta
	if index < 0 || index >= len(commands) || target < 0 || target >= len(commands) {
		return append([]string(nil), commands...)
	}

	moved := append([]string(nil), commands...)
	moved[index], moved[target] = moved[target], moved[index]
	return moved
}

func (a *vaxisTUIApp) reopenEnvironmentConfigCommandMenu(configID string) error {
	config, err := a.service.EnvironmentConfigShow(a.ctx, configID)
	if err != nil {
		return err
	}
	a.openEnvironmentConfigCommandMenu(config)
	return nil
}

func (a *vaxisTUIApp) openConfigurationsMenu() error {
	configurations, err := a.service.ConfigurationList(a.ctx, false)
	if err != nil {
		return err
	}
	description := []string{"Reusable configurations define image, agent, environments, bootstrap, and VM resources for future nodes."}
	for _, configuration := range configurations {
		description = append(description, fmt.Sprintf("- %s: %d CPU, %d MiB memory, %d MiB disk", configuration.Slug, configuration.VCPUs, configuration.MemoryMiB, configuration.DiskMiB))
	}
	entries := []tuiMenuEntry{{Key: 'c', Label: "Create Configuration", Action: func() error { a.openCreateConfigurationDialog(); return nil }}}
	if len(configurations) != 0 {
		entries = append(entries, tuiMenuEntry{Key: 'm', Label: "Manage Configuration", Action: func() error {
			return a.openManageConfigurationSelector(configurations[0].Slug)
		}})
	}
	a.overlay = &tuiMenu{Title: "Configurations", Description: description, Entries: entries}
	return nil
}

func (a *vaxisTUIApp) openConfigurationSelector(current string, onSubmit func(string) error) error {
	configurations, err := a.service.ConfigurationList(a.ctx, false)
	if err != nil {
		return err
	}
	options := make([]tuiSelectorOption, 0, len(configurations))
	for _, configuration := range configurations {
		options = append(options, tuiSelectorOption{Label: configurationSelectorLabel(configuration), Value: configuration.Slug})
	}
	selector := newTUISelector("Select Configuration", nil, options, []string{coalesce(current, DefaultConfigurationSlug)}, false, func(values []string) error {
		if len(values) != 1 {
			return fmt.Errorf("select a configuration")
		}
		return onSubmit(values[0])
	})
	selector.EmptyText = "No configurations configured."
	a.showSelector(selector)
	return nil
}

func configurationSelectorLabel(configuration Configuration) string {
	return fmt.Sprintf(
		"%s (%d vCPU, %s RAM, %s disk)",
		configuration.Slug,
		configuration.VCPUs,
		formatTUIResourceSize(configuration.MemoryMiB),
		formatTUIResourceSize(configuration.DiskMiB),
	)
}

func formatTUIResourceSize(valueMiB uint32) string {
	if valueMiB > 0 && valueMiB%1024 == 0 {
		return fmt.Sprintf("%d GiB", valueMiB/1024)
	}
	return fmt.Sprintf("%d MiB", valueMiB)
}

func (a *vaxisTUIApp) openManageConfigurationSelector(current string) error {
	return a.openConfigurationSelector(current, func(value string) error {
		configuration, err := a.service.ConfigurationShow(a.ctx, value)
		if err != nil {
			return err
		}
		a.openConfigurationMenu(configuration)
		return nil
	})
}

func (a *vaxisTUIApp) openConfigurationMenu(configuration Configuration) {
	entries := []tuiMenuEntry{
		{Key: 'u', Label: "Update Configuration", Action: func() error { a.openUpdateConfigurationDialog(configuration); return nil }},
		{Key: 'c', Label: "Clone Configuration", Action: func() error { a.openCloneConfigurationDialog(configuration); return nil }},
	}
	if configuration.Slug != DefaultConfigurationSlug {
		entries = append(entries, tuiMenuEntry{Key: 'd', Label: "Delete Configuration", Action: func() error { a.openDeleteConfigurationDialog(configuration); return nil }})
	}
	a.overlay = &tuiMenu{
		Title: "Configuration: " + configuration.Slug,
		Description: []string{
			"Image: " + configuration.Image,
			"Agent: " + configuration.AgentProfileName,
			fmt.Sprintf("Resources: %d CPU, %d MiB memory, %d MiB disk", configuration.VCPUs, configuration.MemoryMiB, configuration.DiskMiB),
			"Environments: " + environmentConfigSelectionSummary(configuration.Environments),
			fmt.Sprintf("Bootstrap commands: %d", len(configuration.BootstrapCommands)),
		},
		Entries: entries,
	}
}

func (a *vaxisTUIApp) openCreateConfigurationDialog() {
	a.overlay = newTUIDialog("Create Configuration", "Create", []string{"New configurations copy the current default configuration once."}, []tuiDialogField{
		newTUIInputField("slug", "Configuration Slug", "", true),
	}, func(values map[string]string) error {
		configuration, err := a.service.ConfigurationCreate(a.ctx, ConfigurationCreateInput{Slug: values["slug"]})
		if err != nil {
			return err
		}
		a.setStatus(slog.LevelInfo, "created configuration "+configuration.Slug)
		a.openConfigurationMenu(configuration)
		return nil
	})
}

func (a *vaxisTUIApp) openUpdateConfigurationDialog(configuration Configuration) {
	dialog := newTUIDialog("Update Configuration", "Update", []string{"Changes apply to future nodes only; existing nodes keep their frozen values."}, []tuiDialogField{
		newTUIInputField("slug", "Configuration Slug", configuration.Slug, true),
		newTUIInputField("image", "Image", configuration.Image, true),
		newTUIInputField("agent", "Agent Profile", configuration.AgentProfileName, true),
		newTUIInputField("vcpus", "vCPUs", strconv.Itoa(int(configuration.VCPUs)), true),
		newTUIInputField("memory", "Memory MiB", strconv.FormatUint(uint64(configuration.MemoryMiB), 10), true),
		newTUIInputField("disk", "Disk MiB", strconv.FormatUint(uint64(configuration.DiskMiB), 10), true),
		newTUISelectorField("environments", "Environments", commaSeparatedValues(configuration.Environments), false, nil),
	}, func(values map[string]string) error {
		vcpus64, err := strconv.ParseUint(values["vcpus"], 10, 8)
		if err != nil || vcpus64 == 0 {
			return fmt.Errorf("vCPUs must be a positive integer")
		}
		memory, err := parseSizeMiB(values["memory"])
		if err != nil {
			return fmt.Errorf("memory must be a positive MiB value")
		}
		disk, err := parseSizeMiB(values["disk"])
		if err != nil {
			return fmt.Errorf("disk must be a positive MiB value")
		}
		image, agent, vcpus := values["image"], values["agent"], uint8(vcpus64)
		updated, err := a.service.ConfigurationUpdate(a.ctx, configuration.ID, ConfigurationUpdateInput{
			Slug: values["slug"], Image: &image, AgentProfile: &agent,
			Environments: parseCommaSeparatedValues(values["environments"]), VCPUs: &vcpus, MemoryMiB: &memory, DiskMiB: &disk,
		})
		if err != nil {
			return err
		}
		a.setStatus(slog.LevelInfo, "updated configuration "+updated.Slug)
		a.openConfigurationMenu(updated)
		return a.reloadData(a.state.selectedEntry().key())
	})
	dialog.Fields[6].Display = func(value string) string { return environmentConfigSelectionSummary(parseCommaSeparatedValues(value)) }
	dialog.Fields[6].Activate = func() error {
		return a.openEnvironmentConfigSelector("Select Environments", []string{"Choose reusable environments for future nodes."}, parseCommaSeparatedValues(dialog.Fields[6].Value), true, func(values []string) error {
			dialog.SetFieldValue("environments", commaSeparatedValues(values))
			return nil
		})
	}
	a.overlay = dialog
}

func (a *vaxisTUIApp) openCloneConfigurationDialog(configuration Configuration) {
	a.overlay = newTUIDialog("Clone Configuration", "Clone", []string{"Copy this reusable configuration under a new slug."}, []tuiDialogField{
		newTUIInputField("slug", "New Configuration Slug", "", true),
	}, func(values map[string]string) error {
		cloned, err := a.service.ConfigurationClone(a.ctx, ConfigurationCloneInput{Source: configuration.ID, Slug: values["slug"]})
		if err != nil {
			return err
		}
		a.setStatus(slog.LevelInfo, "cloned configuration "+cloned.Slug)
		a.openConfigurationMenu(cloned)
		return nil
	})
}

func (a *vaxisTUIApp) openDeleteConfigurationDialog(configuration Configuration) {
	a.overlay = newTUIDialog("Delete Configuration", "Delete", []string{"Delete configuration " + configuration.Slug + ". Referenced configurations cannot be deleted."}, nil, func(map[string]string) error {
		deleted, err := a.service.ConfigurationDelete(a.ctx, configuration.ID)
		if err != nil {
			return err
		}
		a.setStatus(slog.LevelInfo, "deleted configuration "+deleted.Slug)
		return a.reloadData(a.state.selectedEntry().key())
	})
}

func (a *vaxisTUIApp) openCreateNodeDialog() error {
	cwd, err := canonicalPath(".")
	if err != nil {
		return err
	}
	dialog := newTUIDialog(
		"Create Node",
		"Create",
		[]string{"Create a directory-bound node from a reusable configuration."},
		[]tuiDialogField{
			newTUIInputField("slug", "Node Slug", "", true),
			newTUIInputField("directory", "Directory", "", false),
			newTUIValueSelectorField("configuration", "Configuration", DefaultConfigurationSlug, true, func(value string) string { return value }, nil),
			newTUIValueSelectorField("workspace_mode", "Workspace Mode", DefaultWorkspaceMode, true, workspaceModeDisplay, nil),
		},
		func(values map[string]string) error {
			return a.startOperation(tuiOperationRequest{
				Title:         "Creating node " + values["slug"],
				DisplayStatus: "creating",
				ResourceKeys:  []string{"nodes"},
				EntryKeys:     []string{"nodes"},
				Run: func(ctx context.Context, service *Service) (tuiOperationResult, error) {
					node, err := service.NodeCreate(ctx, NodeCreateInput{
						Configuration: values["configuration"],
						Directory:     values["directory"],
						Slug:          values["slug"],
						WorkspaceMode: values["workspace_mode"],
					})
					if err != nil {
						return tuiOperationResult{}, err
					}
					return tuiOperationResult{
						Status:           "created node " + node.Slug,
						PreferredKey:     terminal.NodeTarget(node.ID).String(),
						ReloadData:       true,
						ShowTerminalPane: true,
					}, nil
				},
			})
		},
	)
	dialog.Fields[1].Input.SetPrompt(cwd)
	dialog.Fields[1].Input.Prompt = tuiMutedStyle()
	dialog.Fields[2].Activate = func() error {
		return a.openConfigurationSelector(dialog.Fields[2].rawValue(), func(value string) error {
			dialog.SetFieldValue("configuration", value)
			return nil
		})
	}
	dialog.Fields[3].Activate = func() error {
		return a.openWorkspaceModeSelector(
			dialog.Fields[3].rawValue(),
			func(value string) error {
				dialog.SetFieldValue("workspace_mode", value)
				return nil
			},
		)
	}
	a.overlay = dialog
	return nil
}

func (a *vaxisTUIApp) openWorkspaceModeSelector(current string, onSubmit func(value string) error) error {
	options := []tuiSelectorOption{
		{Label: workspaceModeDisplay(WorkspaceModeCopy), Value: WorkspaceModeCopy},
		{Label: workspaceModeDisplay(WorkspaceModeMounted), Value: WorkspaceModeMounted},
	}
	a.showSelector(newTUISelector(
		"Workspace Mode",
		nil,
		options,
		[]string{coalesce(current, DefaultWorkspaceMode)},
		false,
		func(values []string) error {
			if len(values) == 0 {
				return fmt.Errorf("select a workspace mode")
			}
			return onSubmit(values[0])
		},
	))
	return nil
}

func workspaceModeDisplay(mode string) string {
	switch normalizeWorkspaceMode(mode) {
	case WorkspaceModeMounted:
		return "mounted: writable host workspace mount"
	default:
		return "copy: isolated guest workspace copy"
	}
}

func (a *vaxisTUIApp) openEnvironmentConfigsMenu() error {
	configs, err := a.service.EnvironmentConfigList(a.ctx, false)
	if err != nil {
		return err
	}

	description := []string{
		"No environments configured.",
	}
	if len(configs) > 0 {
		description = description[:0]
		description = append(description, "Configured defaults:")
		for _, config := range configs {
			description = append(description, fmt.Sprintf("- %s (%d bootstrap commands)", config.Slug, len(config.BootstrapCommands)))
		}
	} else {
		description = append(description[:0], "No environments configured.")
	}

	entries := []tuiMenuEntry{
		{Key: 'c', Label: "Create Environment", Action: func() error { a.openCreateEnvironmentConfigDialog(); return nil }},
	}
	if len(configs) > 0 {
		entries = append(entries, tuiMenuEntry{Key: 'm', Label: "Manage Environment", Action: func() error { return a.openManageEnvironmentConfigDialog(configs[0].Slug) }})
	}

	a.overlay = &tuiMenu{
		Title:       "Environments",
		Description: description,
		Entries:     entries,
	}

	return nil
}

func (a *vaxisTUIApp) openCreateEnvironmentConfigDialog() {
	a.overlay = newTUIDialog(
		"Create Environment",
		"Create",
		[]string{
			"Create a reusable environment for bootstrap commands.",
			"Create the environment first, then add or reorder as many commands as you need from the command editor.",
		},
		[]tuiDialogField{
			newTUIInputField("slug", "Environment Slug", "", true),
		},
		func(values map[string]string) error {
			config, err := a.service.EnvironmentConfigCreate(a.ctx, EnvironmentConfigCreateInput{
				Slug: values["slug"],
			})
			if err != nil {
				return err
			}
			a.setStatus(slog.LevelInfo, "created environment "+config.Slug)
			a.openEnvironmentConfigCommandMenu(config)
			return nil
		},
	)
}

func (a *vaxisTUIApp) openManageEnvironmentConfigDialog(defaultSlug string) error {
	selected := []string{}
	if strings.TrimSpace(defaultSlug) != "" {
		selected = append(selected, defaultSlug)
	}
	return a.openEnvironmentConfigSelector(
		"Manage Environment",
		[]string{"Choose an environment to edit its commands or delete it."},
		selected,
		false,
		func(values []string) error {
			if len(values) == 0 {
				return fmt.Errorf("select an environment")
			}
			config, err := a.service.EnvironmentConfigShow(a.ctx, values[0])
			if err != nil {
				return err
			}
			a.openEnvironmentConfigCommandMenu(config)
			return nil
		},
	)
}

func (a *vaxisTUIApp) openEnvironmentConfigCommandMenu(config EnvironmentConfig) {
	description := []string{
		"No bootstrap commands configured.",
	}
	if len(config.BootstrapCommands) > 0 {
		description = description[:0]
		description = append(description, "Configured bootstrap commands:")
		for index, command := range config.BootstrapCommands {
			description = append(description, fmt.Sprintf("%d. %s", index+1, command))
		}
	} else {
		description = append(description[:0], "No bootstrap commands configured.")
	}

	a.overlay = &tuiMenu{
		Title:       "Environment: " + config.Slug,
		Description: description,
		Entries: []tuiMenuEntry{
			{Key: 'a', Label: "Add Bootstrap Command", Action: func() error { a.openAddEnvironmentConfigCommandDialog(config); return nil }},
			{Key: 'r', Label: "Remove Bootstrap Command", Action: func() error { return a.openRemoveEnvironmentConfigCommandDialog(config) }},
			{Key: 'm', Label: "Move Bootstrap Command", Action: func() error { return a.openMoveEnvironmentConfigCommandDialog(config) }},
			{Key: 'c', Label: "Clear Bootstrap Commands", Action: func() error { a.openClearEnvironmentConfigCommandsDialog(config); return nil }},
			{Key: 'd', Label: "Delete Environment", Action: func() error { a.openDeleteEnvironmentConfigDialog(config); return nil }},
		},
	}
}

func (a *vaxisTUIApp) openAddEnvironmentConfigCommandDialog(config EnvironmentConfig) {
	a.overlay = newTUIDialog(
		"Add Environment Bootstrap Command",
		"Add",
		[]string{"Add a bootstrap command to the reusable environment."},
		[]tuiDialogField{
			newTUIInputField("command", "Command", "", true),
		},
		func(values map[string]string) error {
			commands := append(append([]string(nil), config.BootstrapCommands...), values["command"])
			updated, err := a.service.EnvironmentConfigUpdate(a.ctx, config.ID, EnvironmentConfigUpdateInput{BootstrapCommands: commands})
			if err != nil {
				return err
			}
			a.setStatus(slog.LevelInfo, "updated environment "+updated.Slug)
			return a.reopenEnvironmentConfigCommandMenu(updated.ID)
		},
	)
}

func (a *vaxisTUIApp) openRemoveEnvironmentConfigCommandDialog(config EnvironmentConfig) error {
	if len(config.BootstrapCommands) == 0 {
		return fmt.Errorf("environment %s has no commands", config.Slug)
	}

	a.showSelector(newTUISelector(
		"Remove Environment Bootstrap Commands",
		[]string{"Choose one or more reusable environment bootstrap commands to remove."},
		commandSelectorOptions(config.BootstrapCommands),
		nil,
		true,
		func(values []string) error {
			indices, err := parseSelectorIndices(values, len(config.BootstrapCommands))
			if err != nil {
				return err
			}
			if len(indices) == 0 {
				return fmt.Errorf("select at least one command to remove")
			}

			description := []string{"Remove the selected reusable environment commands?"}
			for _, index := range indices {
				description = append(description, fmt.Sprintf("%d. %s", index+1, config.BootstrapCommands[index]))
			}

			a.overlay = newTUIDialog(
				"Remove Environment Bootstrap Commands",
				"Remove",
				description,
				nil,
				func(map[string]string) error {
					updated, err := a.service.EnvironmentConfigUpdate(a.ctx, config.ID, EnvironmentConfigUpdateInput{
						BootstrapCommands: removeCommandsByIndex(config.BootstrapCommands, indices),
					})
					if err != nil {
						return err
					}
					a.setStatus(slog.LevelInfo, "updated environment "+updated.Slug)
					return a.reopenEnvironmentConfigCommandMenu(updated.ID)
				},
			)
			return nil
		},
	))

	return nil
}

func (a *vaxisTUIApp) openMoveEnvironmentConfigCommandDialog(config EnvironmentConfig) error {
	if len(config.BootstrapCommands) < 2 {
		return fmt.Errorf("environment %s needs at least two commands to change order", config.Slug)
	}

	a.showSelector(newTUISelector(
		"Move Environment Bootstrap Command",
		[]string{"Choose a reusable environment bootstrap command to move up or down."},
		commandSelectorOptions(config.BootstrapCommands),
		nil,
		false,
		func(values []string) error {
			indices, err := parseSelectorIndices(values, len(config.BootstrapCommands))
			if err != nil {
				return err
			}
			if len(indices) != 1 {
				return fmt.Errorf("select a single command to move")
			}
			index := indices[0]
			command := config.BootstrapCommands[index]

			entries := []tuiMenuEntry{}
			if index > 0 {
				entries = append(entries, tuiMenuEntry{Key: 'u', Label: "Move Up", Action: func() error {
					updated, err := a.service.EnvironmentConfigUpdate(a.ctx, config.ID, EnvironmentConfigUpdateInput{
						BootstrapCommands: moveCommand(config.BootstrapCommands, index, -1),
					})
					if err != nil {
						return err
					}
					a.setStatus(slog.LevelInfo, "updated environment "+updated.Slug)
					return a.reopenEnvironmentConfigCommandMenu(updated.ID)
				}})
			}
			if index < len(config.BootstrapCommands)-1 {
				entries = append(entries, tuiMenuEntry{Key: 'd', Label: "Move Down", Action: func() error {
					updated, err := a.service.EnvironmentConfigUpdate(a.ctx, config.ID, EnvironmentConfigUpdateInput{
						BootstrapCommands: moveCommand(config.BootstrapCommands, index, 1),
					})
					if err != nil {
						return err
					}
					a.setStatus(slog.LevelInfo, "updated environment "+updated.Slug)
					return a.reopenEnvironmentConfigCommandMenu(updated.ID)
				}})
			}

			a.overlay = &tuiMenu{
				Title:       "Move Environment Bootstrap Command: " + command,
				Description: []string{"Choose how to reposition the selected reusable environment bootstrap command."},
				Entries:     entries,
			}
			return nil
		},
	))
	return nil
}

func (a *vaxisTUIApp) openClearEnvironmentConfigCommandsDialog(config EnvironmentConfig) {
	a.overlay = newTUIDialog(
		"Clear Environment Bootstrap Commands",
		"Clear",
		[]string{"Remove all bootstrap commands from environment " + config.Slug + "."},
		nil,
		func(_ map[string]string) error {
			updated, err := a.service.EnvironmentConfigUpdate(a.ctx, config.ID, EnvironmentConfigUpdateInput{ClearBootstrapCommands: true})
			if err != nil {
				return err
			}
			a.setStatus(slog.LevelInfo, "cleared environment "+updated.Slug)
			return a.reopenEnvironmentConfigCommandMenu(updated.ID)
		},
	)
}

func (a *vaxisTUIApp) openDeleteEnvironmentConfigDialog(config EnvironmentConfig) {
	selectedKey := a.state.selectedEntry().key()
	a.overlay = newTUIDialog(
		"Delete Environment",
		"Delete",
		[]string{"Delete reusable environment " + config.Slug + "."},
		nil,
		func(_ map[string]string) error {
			deleted, err := a.service.EnvironmentConfigDelete(a.ctx, config.ID)
			if err != nil {
				return err
			}
			a.setStatus(slog.LevelInfo, "deleted environment "+deleted.Slug)
			return a.reloadData(selectedKey)
		},
	)
}

func (a *vaxisTUIApp) openDeleteNodeDialog(node Node) {
	a.overlay = newTUIDialog(
		"Delete Node",
		"Delete",
		[]string{
			"Delete node " + node.Slug + ".",
			"The associated Lima instance will be terminated.",
		},
		nil,
		func(_ map[string]string) error {
			return a.startOperation(tuiOperationRequest{
				Title:         "Deleting node " + node.Slug,
				DisplayStatus: "deleting",
				ResourceKeys:  []string{terminal.NodeTarget(node.ID).String()},
				EntryKeys:     []string{terminal.NodeTarget(node.ID).String()},
				Run: func(ctx context.Context, service *Service) (tuiOperationResult, error) {
					deleted, err := service.NodeDelete(ctx, node.ID)
					if err != nil {
						return tuiOperationResult{}, err
					}
					return tuiOperationResult{
						Status:      "deleted node " + deleted.Slug,
						CloseNodeID: deleted.ID,
						ReloadData:  true,
					}, nil
				},
			})
		},
	)
}

func (a *vaxisTUIApp) openCloneNodeDialog(node Node) {
	fields := []tuiDialogField{newTUIInputField("node_slug", "Cloned Node Slug", "", true)}
	a.overlay = newTUIDialog(
		"Clone Node",
		"Clone",
		[]string{
			"Clone the selected node in the same directory and configuration.",
			"The cloned VM keeps the source's frozen configuration and writable state.",
		},
		fields,
		func(values map[string]string) error {
			return a.startOperation(tuiOperationRequest{
				Title:         "Cloning node " + node.Slug,
				DisplayStatus: "cloning",
				ResourceKeys:  []string{terminal.NodeTarget(node.ID).String()},
				EntryKeys:     []string{terminal.NodeTarget(node.ID).String()},
				Run: func(ctx context.Context, service *Service) (tuiOperationResult, error) {
					childNode, err := service.NodeClone(ctx, NodeCloneInput{SourceNode: node.ID, NodeSlug: values["node_slug"]})
					if err != nil {
						return tuiOperationResult{}, err
					}
					return tuiOperationResult{
						Status:       "cloned node " + node.Slug + " to " + childNode.Slug,
						PreferredKey: terminal.NodeTarget(childNode.ID).String(),
						ReloadData:   true,
					}, nil
				},
			})
		},
	)
}
