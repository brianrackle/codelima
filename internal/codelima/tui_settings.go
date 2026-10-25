package codelima

import (
	"context"
	"fmt"
	"strconv"

	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/widgets/border"
)

type tuiManagerSetting struct {
	key, label, value string
	choices           []string
}

func (a *vaxisTUIApp) managerSettings() Config {
	if a.preferences != nil {
		return *a.preferences
	}
	if a.service != nil {
		return a.service.cfg
	}
	return DefaultConfig("")
}

func (a *vaxisTUIApp) managerSettingRows() []tuiManagerSetting {
	cfg := a.managerSettings()
	return []tuiManagerSetting{
		{key: "shell.mode", label: "shell.mode", value: cfg.Shell.Mode, choices: []string{"attach", "window", "tmux", "embedded"}},
		{key: "shell.window", label: "shell.window", value: cfg.Shell.Window},
		{label: "shell.detach_key", value: "ctrl+]"},
		{key: "import_host_auth", label: "import_host_auth", value: strconv.FormatBool(cfg.ImportHostAuth)},
		{label: "default template", value: DefaultConfigurationSlug + " · editable in Templates"},
		{key: "confirm_stop", label: "confirm stop", value: cfg.ConfirmStop, choices: []string{"busy", "always", "never"}},
		{key: "group_by_directory", label: "group by directory", value: strconv.FormatBool(cfg.GroupByDirectory)},
		{key: "color", label: "color", value: cfg.Color, choices: []string{"auto", "ansi", "truecolor"}},
	}
}

func (a *vaxisTUIApp) drawManagerSettings(win vaxis.Window, theme tuiTheme) {
	style := theme.border
	if a.systemPanel == tuiSystemSettings {
		style = theme.mint
	}
	inner := border.All(win, style)
	tuiLine(inner, 0, a.keyHints("Settings · {system.open} edit"), theme.muted)
	_, height := inner.Size()
	rows := a.managerSettingRows()
	start, visible := a.managerSettingsViewport(height)
	for i := start; i < len(rows) && i < start+visible; i++ {
		style := theme.text
		if rows[i].key == "" {
			style = theme.dim
		}
		if a.systemPanel == tuiSystemSettings && i == a.settingIndex {
			style = theme.selected
		}
		tuiLine(inner, 2+i-start, fmt.Sprintf("%-20s %s", rows[i].label, rows[i].value), style)
	}
	tuiLine(inner, height-2, "Changes preserve comments and unknown keys.", theme.dim)
	tuiLine(inner, height-1, a.keyHints("Host credentials: new nodes only · {navigation.next_tab} panel"), theme.dim)
}

func (a *vaxisTUIApp) managerSettingsViewport(height int) (start, visible int) {
	visible = max(1, height-5)
	return max(0, a.settingIndex-visible+1), visible
}

func (a *vaxisTUIApp) editManagerSetting() error {
	rows := a.managerSettingRows()
	if a.settingIndex < 0 || a.settingIndex >= len(rows) {
		return nil
	}
	setting := rows[a.settingIndex]
	if setting.key == "" {
		return fmt.Errorf("%s is fixed", setting.label)
	}
	if setting.key == "import_host_auth" || setting.key == "group_by_directory" {
		return a.saveManagerSetting(setting.key, strconv.FormatBool(setting.value != "true"), nil)
	}
	if len(setting.choices) > 0 {
		options := make([]tuiSelectorOption, 0, len(setting.choices))
		for _, choice := range setting.choices {
			options = append(options, tuiSelectorOption{Label: choice, Value: choice})
		}
		a.overlay = newTUISelector(setting.label, nil, options, []string{setting.value}, false, func(values []string) error {
			return a.saveManagerSetting(setting.key, values[0], nil)
		})
		return nil
	}
	a.overlay = newTUIDialog(setting.label, "Save", []string{"{cmd} expands to the session's command arguments.", "Example: ghostty -e {cmd}"}, []tuiDialogField{newTUIInputField("value", "Command", setting.value, true)}, func(values map[string]string) error {
		return a.saveManagerSetting(setting.key, values["value"], nil)
	})
	return nil
}

func (a *vaxisTUIApp) saveManagerSetting(key, value string, after func(*vaxisTUIApp) error) error {
	return a.startMetadataOperation("Saving "+key, "settings", func(ctx context.Context, service *Service) (tuiOperationResult, error) {
		cfg, err := service.SettingsUpdate(ctx, key, value)
		return tuiOperationResult{Status: "Saved " + key + " · " + value, Apply: func(app *vaxisTUIApp) error {
			app.preferences = &cfg
			app.applyKeyboardConfig(cfg)
			app.preferenceRevision++
			if key == "group_by_directory" && app.fleet != nil {
				app.fleet.groupByDir = app.treeWorkspaceRoot == "" && cfg.GroupByDirectory
				app.fleet.rebuild()
			}
			if after != nil {
				return after(app)
			}
			return nil
		}}, err
	})
}
