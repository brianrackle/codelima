package codelima

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/brianrackle/test_lima/internal/codelima/terminal"
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
	configs, err := a.service.EnvironmentConfigList(false)
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
		return fmt.Errorf("no environment configs configured")
	}
	if multi && len(options) == 0 {
		description = append(description, "No reusable environment configs configured. Press Enter to keep none assigned.")
	}
	a.selector = newTUISelector(title, description, options, current, multi, onSubmit)
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
	config, err := a.service.EnvironmentConfigShow(configID)
	if err != nil {
		return err
	}
	a.openEnvironmentConfigCommandMenu(config)
	return nil
}

func (a *vaxisTUIApp) openCreateProjectDialog() {
	description := []string{
		"Create a top-level project rooted at a host workspace.",
		"Use project fork when you want a child project copied from an existing workspace snapshot.",
		"Use the Environment Configs field to choose shared defaults for future nodes from the selector.",
	}

	dialog := newTUIDialog(
		"Create Project",
		"Create",
		description,
		[]tuiDialogField{
			newTUIInputField("slug", "Project Slug", "", false),
			newTUIInputField("workspace_path", "Workspace Path", "", true),
			newTUISelectorField("environment_configs", "Environment Configs", "", false, nil),
		},
		func(values map[string]string) error {
			title := "Creating project"
			if values["slug"] != "" {
				title += " " + values["slug"]
			}
			return a.startOperation(tuiOperationRequest{
				Title:         title,
				DisplayStatus: "creating",
				ResourceKeys:  []string{"projects"},
				EntryKeys:     []string{"projects"},
				Run: func(ctx context.Context, service *Service) (tuiOperationResult, error) {
					project, err := service.ProjectCreate(ctx, ProjectCreateInput{
						Slug:               values["slug"],
						WorkspacePath:      values["workspace_path"],
						EnvironmentConfigs: parseCommaSeparatedValues(values["environment_configs"]),
					})
					if err != nil {
						return tuiOperationResult{}, err
					}
					return tuiOperationResult{
						Status:       "created project " + project.Slug,
						PreferredKey: terminal.ProjectTarget(project.ID).String(),
						ReloadData:   true,
					}, nil
				},
			})
		},
	)
	dialog.Fields[2].Value = commaSeparatedValues([]string{})
	dialog.Fields[2].Display = func(value string) string {
		return environmentConfigSelectionSummary(parseCommaSeparatedValues(value))
	}
	dialog.Fields[2].Activate = func() error {
		return a.openEnvironmentConfigSelector(
			"Select Environment Configs",
			[]string{"Choose reusable environment configs to assign shared defaults for future nodes in this project."},
			parseCommaSeparatedValues(dialog.Fields[2].Value),
			true,
			func(values []string) error {
				dialog.SetFieldValue("environment_configs", commaSeparatedValues(values))
				return nil
			},
		)
	}
	a.dialog = dialog
}

func (a *vaxisTUIApp) openCreateNodeDialog(project Project) {
	dialog := newTUIDialog(
		"Create Node",
		"Create",
		[]string{
			"Selected project: " + project.Slug,
		},
		[]tuiDialogField{
			newTUIInputField("slug", "Node Slug", project.Slug+"-node", true),
			newTUIValueSelectorField("workspace_mode", "Workspace Mode", WorkspaceModeCopy, true, workspaceModeDisplay, nil),
			newTUIInputField("lima_commands_file", "Lima Commands File (optional)", "", false),
		},
		func(values map[string]string) error {
			limaCommands, err := loadOptionalLimaCommandsFile(values["lima_commands_file"])
			if err != nil {
				return err
			}
			return a.startOperation(tuiOperationRequest{
				Title:         "Creating node " + values["slug"],
				DisplayStatus: "creating",
				ResourceKeys:  []string{terminal.ProjectTarget(project.ID).String()},
				EntryKeys:     []string{terminal.ProjectTarget(project.ID).String()},
				Run: func(ctx context.Context, service *Service) (tuiOperationResult, error) {
					node, err := service.NodeCreate(ctx, NodeCreateInput{
						Project:       project.ID,
						Slug:          values["slug"],
						WorkspaceMode: values["workspace_mode"],
						LimaCommands:  limaCommands,
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
	dialog.Fields[1].Activate = func() error {
		return a.openWorkspaceModeSelector(
			dialog.Fields[1].rawValue(),
			func(value string) error {
				dialog.SetFieldValue("workspace_mode", value)
				return nil
			},
		)
	}
	a.dialog = dialog
}

func (a *vaxisTUIApp) openWorkspaceModeSelector(current string, onSubmit func(value string) error) error {
	options := []tuiSelectorOption{
		{Label: workspaceModeDisplay(WorkspaceModeCopy), Value: WorkspaceModeCopy},
		{Label: workspaceModeDisplay(WorkspaceModeMounted), Value: WorkspaceModeMounted},
	}
	a.selector = newTUISelector(
		"Workspace Mode",
		nil,
		options,
		[]string{coalesce(current, WorkspaceModeCopy)},
		false,
		func(values []string) error {
			if len(values) == 0 {
				return fmt.Errorf("select a workspace mode")
			}
			return onSubmit(values[0])
		},
	)
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

func (a *vaxisTUIApp) openUpdateProjectDialog(project Project) {
	dialog := newTUIDialog(
		"Update Project",
		"Update",
		[]string{
			"Update the selected project slug, workspace path, and assigned environment configs.",
			"Edit the project file shown in the right pane when you need advanced per-project settings such as Lima command overrides.",
		},
		[]tuiDialogField{
			newTUIInputField("slug", "Project Slug", project.Slug, true),
			newTUIInputField("workspace_path", "Workspace Path", project.WorkspacePath, true),
			newTUISelectorField("environment_configs", "Environment Configs", commaSeparatedValues(project.EnvironmentConfigs), false, nil),
		},
		func(values map[string]string) error {
			slug := values["slug"]
			workspacePath := values["workspace_path"]
			return a.startOperation(tuiOperationRequest{
				Title:         "Saving project " + project.Slug,
				DisplayStatus: "updating",
				ResourceKeys:  []string{terminal.ProjectTarget(project.ID).String()},
				EntryKeys:     []string{terminal.ProjectTarget(project.ID).String()},
				Run: func(_ context.Context, service *Service) (tuiOperationResult, error) {
					updated, err := service.ProjectUpdate(project.ID, ProjectUpdateInput{
						Slug:               &slug,
						WorkspacePath:      &workspacePath,
						EnvironmentConfigs: parseCommaSeparatedValues(values["environment_configs"]),
					})
					if err != nil {
						return tuiOperationResult{}, err
					}
					return tuiOperationResult{
						Status:       "updated project " + updated.Slug,
						PreferredKey: terminal.ProjectTarget(updated.ID).String(),
						ReloadData:   true,
					}, nil
				},
			})
		},
	)
	dialog.Fields[2].Display = func(value string) string {
		return environmentConfigSelectionSummary(parseCommaSeparatedValues(value))
	}
	dialog.Fields[2].Activate = func() error {
		return a.openEnvironmentConfigSelector(
			"Select Environment Configs",
			[]string{"Choose reusable environment configs to keep assigned to this project."},
			parseCommaSeparatedValues(dialog.Fields[2].Value),
			true,
			func(values []string) error {
				dialog.SetFieldValue("environment_configs", commaSeparatedValues(values))
				return nil
			},
		)
	}
	a.dialog = dialog
}

func (a *vaxisTUIApp) openEnvironmentConfigsMenu() error {
	configs, err := a.service.EnvironmentConfigList(false)
	if err != nil {
		return err
	}

	description := []string{
		"No environment configs configured.",
	}
	if len(configs) > 0 {
		description = description[:0]
		description = append(description, "Configured defaults:")
		for _, config := range configs {
			description = append(description, fmt.Sprintf("- %s (%d bootstrap commands)", config.Slug, len(config.BootstrapCommands)))
		}
	} else {
		description = append(description[:0], "No environment configs configured.")
	}

	entries := []tuiMenuEntry{
		{Key: 'c', Label: "Create Config", Action: func() error { a.openCreateEnvironmentConfigDialog(); return nil }},
	}
	if len(configs) > 0 {
		entries = append(entries, tuiMenuEntry{Key: 'm', Label: "Manage Config", Action: func() error { return a.openManageEnvironmentConfigDialog(configs[0].Slug) }})
	}

	a.menu = &tuiMenu{
		Title:       "Environment Configs",
		Description: description,
		Entries:     entries,
	}

	return nil
}

func (a *vaxisTUIApp) openCreateEnvironmentConfigDialog() {
	a.dialog = newTUIDialog(
		"Create Environment Config",
		"Create",
		[]string{
			"Create a reusable environment config for project bootstrap commands.",
			"Create the config first, then add or reorder as many commands as you need from the command editor.",
		},
		[]tuiDialogField{
			newTUIInputField("slug", "Config Slug", "", true),
		},
		func(values map[string]string) error {
			config, err := a.service.EnvironmentConfigCreate(EnvironmentConfigCreateInput{
				Slug: values["slug"],
			})
			if err != nil {
				return err
			}
			a.status = "created environment config " + config.Slug
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
		"Manage Environment Config",
		[]string{"Choose an environment config to edit its commands or delete it."},
		selected,
		false,
		func(values []string) error {
			if len(values) == 0 {
				return fmt.Errorf("select an environment config")
			}
			config, err := a.service.EnvironmentConfigShow(values[0])
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

	a.menu = &tuiMenu{
		Title:       "Environment Config: " + config.Slug,
		Description: description,
		Entries: []tuiMenuEntry{
			{Key: 'a', Label: "Add Bootstrap Command", Action: func() error { a.openAddEnvironmentConfigCommandDialog(config); return nil }},
			{Key: 'r', Label: "Remove Bootstrap Command", Action: func() error { return a.openRemoveEnvironmentConfigCommandDialog(config) }},
			{Key: 'm', Label: "Move Bootstrap Command", Action: func() error { return a.openMoveEnvironmentConfigCommandDialog(config) }},
			{Key: 'c', Label: "Clear Bootstrap Commands", Action: func() error { a.openClearEnvironmentConfigCommandsDialog(config); return nil }},
			{Key: 'd', Label: "Delete Config", Action: func() error { a.openDeleteEnvironmentConfigDialog(config); return nil }},
		},
	}
}

func (a *vaxisTUIApp) openAddEnvironmentConfigCommandDialog(config EnvironmentConfig) {
	a.dialog = newTUIDialog(
		"Add Environment Config Bootstrap Command",
		"Add",
		[]string{"Add a bootstrap command to the reusable environment config."},
		[]tuiDialogField{
			newTUIInputField("command", "Command", "", true),
		},
		func(values map[string]string) error {
			commands := append(append([]string(nil), config.BootstrapCommands...), values["command"])
			updated, err := a.service.EnvironmentConfigUpdate(config.ID, EnvironmentConfigUpdateInput{BootstrapCommands: commands})
			if err != nil {
				return err
			}
			a.status = "updated environment config " + updated.Slug
			return a.reopenEnvironmentConfigCommandMenu(updated.ID)
		},
	)
}

func (a *vaxisTUIApp) openRemoveEnvironmentConfigCommandDialog(config EnvironmentConfig) error {
	if len(config.BootstrapCommands) == 0 {
		return fmt.Errorf("environment config %s has no commands", config.Slug)
	}

	a.selector = newTUISelector(
		"Remove Environment Config Bootstrap Commands",
		[]string{"Choose one or more reusable environment config bootstrap commands to remove."},
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

			description := []string{"Remove the selected reusable environment config commands?"}
			for _, index := range indices {
				description = append(description, fmt.Sprintf("%d. %s", index+1, config.BootstrapCommands[index]))
			}

			a.dialog = newTUIDialog(
				"Remove Environment Config Bootstrap Commands",
				"Remove",
				description,
				nil,
				func(map[string]string) error {
					updated, err := a.service.EnvironmentConfigUpdate(config.ID, EnvironmentConfigUpdateInput{
						BootstrapCommands: removeCommandsByIndex(config.BootstrapCommands, indices),
					})
					if err != nil {
						return err
					}
					a.status = "updated environment config " + updated.Slug
					return a.reopenEnvironmentConfigCommandMenu(updated.ID)
				},
			)
			return nil
		},
	)

	return nil
}

func (a *vaxisTUIApp) openMoveEnvironmentConfigCommandDialog(config EnvironmentConfig) error {
	if len(config.BootstrapCommands) < 2 {
		return fmt.Errorf("environment config %s needs at least two commands to change order", config.Slug)
	}

	a.selector = newTUISelector(
		"Move Environment Config Bootstrap Command",
		[]string{"Choose a reusable environment config bootstrap command to move up or down."},
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
					updated, err := a.service.EnvironmentConfigUpdate(config.ID, EnvironmentConfigUpdateInput{
						BootstrapCommands: moveCommand(config.BootstrapCommands, index, -1),
					})
					if err != nil {
						return err
					}
					a.status = "updated environment config " + updated.Slug
					return a.reopenEnvironmentConfigCommandMenu(updated.ID)
				}})
			}
			if index < len(config.BootstrapCommands)-1 {
				entries = append(entries, tuiMenuEntry{Key: 'd', Label: "Move Down", Action: func() error {
					updated, err := a.service.EnvironmentConfigUpdate(config.ID, EnvironmentConfigUpdateInput{
						BootstrapCommands: moveCommand(config.BootstrapCommands, index, 1),
					})
					if err != nil {
						return err
					}
					a.status = "updated environment config " + updated.Slug
					return a.reopenEnvironmentConfigCommandMenu(updated.ID)
				}})
			}

			a.menu = &tuiMenu{
				Title:       "Move Environment Config Bootstrap Command: " + command,
				Description: []string{"Choose how to reposition the selected reusable environment config bootstrap command."},
				Entries:     entries,
			}
			return nil
		},
	)
	return nil
}

func (a *vaxisTUIApp) openClearEnvironmentConfigCommandsDialog(config EnvironmentConfig) {
	a.dialog = newTUIDialog(
		"Clear Environment Config Bootstrap Commands",
		"Clear",
		[]string{"Remove all bootstrap commands from environment config " + config.Slug + "."},
		nil,
		func(_ map[string]string) error {
			updated, err := a.service.EnvironmentConfigUpdate(config.ID, EnvironmentConfigUpdateInput{ClearBootstrapCommands: true})
			if err != nil {
				return err
			}
			a.status = "cleared environment config " + updated.Slug
			return a.reopenEnvironmentConfigCommandMenu(updated.ID)
		},
	)
}

func (a *vaxisTUIApp) openDeleteEnvironmentConfigDialog(config EnvironmentConfig) {
	selectedKey := a.state.selectedEntry().key()
	a.dialog = newTUIDialog(
		"Delete Environment Config",
		"Delete",
		[]string{"Delete reusable environment config " + config.Slug + "."},
		nil,
		func(_ map[string]string) error {
			deleted, err := a.service.EnvironmentConfigDelete(config.ID)
			if err != nil {
				return err
			}
			a.status = "deleted environment config " + deleted.Slug
			return a.reloadData(selectedKey)
		},
	)
}

func (a *vaxisTUIApp) openDeleteProjectDialog(project Project) {
	a.dialog = newTUIDialog(
		"Delete Project",
		"Delete",
		[]string{
			"Delete project " + project.Slug + ".",
			"This only succeeds if the project has no live nodes or child projects.",
		},
		nil,
		func(_ map[string]string) error {
			return a.startOperation(tuiOperationRequest{
				Title:         "Deleting project " + project.Slug,
				DisplayStatus: "deleting",
				ResourceKeys:  []string{terminal.ProjectTarget(project.ID).String()},
				EntryKeys:     []string{terminal.ProjectTarget(project.ID).String()},
				Run: func(_ context.Context, service *Service) (tuiOperationResult, error) {
					deleted, err := service.ProjectDelete(project.ID)
					if err != nil {
						return tuiOperationResult{}, err
					}
					return tuiOperationResult{
						Status:     "deleted project " + deleted.Slug,
						ReloadData: true,
					}, nil
				},
			})
		},
	)
}

func (a *vaxisTUIApp) openDeleteNodeDialog(node Node) {
	a.dialog = newTUIDialog(
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

func (a *vaxisTUIApp) openCloneNodeDialog(node Node, project Project) {
	a.dialog = newTUIDialog(
		"Clone Node",
		"Clone",
		[]string{
			"Clone the selected node into another node in the same project.",
			"The cloned VM keeps the same guest workspace path and bootstrap state as the source.",
		},
		[]tuiDialogField{
			newTUIInputField("node_slug", "Cloned Node Slug", node.Slug+"-clone", true),
			newTUIInputField("lima_commands_file", "Lima Commands File (optional)", "", false),
		},
		func(values map[string]string) error {
			limaCommands, err := loadOptionalLimaCommandsFile(values["lima_commands_file"])
			if err != nil {
				return err
			}
			return a.startOperation(tuiOperationRequest{
				Title:         "Cloning node " + node.Slug,
				DisplayStatus: "cloning",
				ResourceKeys:  []string{terminal.NodeTarget(node.ID).String(), terminal.ProjectTarget(project.ID).String()},
				EntryKeys:     []string{terminal.NodeTarget(node.ID).String(), terminal.ProjectTarget(project.ID).String()},
				Run: func(ctx context.Context, service *Service) (tuiOperationResult, error) {
					childNode, err := service.NodeClone(ctx, NodeCloneInput{
						SourceNode:   node.ID,
						NodeSlug:     values["node_slug"],
						LimaCommands: limaCommands,
					})
					if err != nil {
						return tuiOperationResult{}, err
					}
					return tuiOperationResult{
						Status:       "cloned node " + node.Slug + " to " + childNode.Slug + " in " + project.Slug,
						PreferredKey: terminal.NodeTarget(childNode.ID).String(),
						ReloadData:   true,
					}, nil
				},
			})
		},
	)
}
