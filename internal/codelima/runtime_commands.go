package codelima

import "gopkg.in/yaml.v3"

type runtimeCommandsExample struct {
	RuntimeCommands RuntimeCommandTemplates `yaml:"runtime_commands"`
}

type runtimeCommandTemplateField struct {
	key    runtimeCommandKind
	values *[]string
}

func defaultRuntimeCommandTemplates() RuntimeCommandTemplates {
	return RuntimeCommandTemplates{
		Version: []string{
			"{{binary}} --version",
		},
		List: []string{
			"{{binary}} list --json",
		},
		Create: []string{
			"{{binary}} create -y --name {{sandbox_name}} {{template_path}}",
		},
		Start: []string{
			"{{binary}} start -y {{sandbox_name}}",
		},
		Stop: []string{
			"{{binary}} stop -y {{sandbox_name}}",
		},
		Delete: []string{
			"{{binary}} delete -f {{sandbox_name}}",
		},
		Clone: []string{
			"{{binary}} clone -y {{source_sandbox}} {{sandbox_name}}",
		},
		Bootstrap: []string{},
		WorkspaceSeedPrepare: []string{
			`owner="${SUDO_USER:-$(id -un)}" && group="$(id -gn "$owner")" && rm -rf {{target_path}} && mkdir -p {{target_parent}} && chown "$owner:$group" {{target_parent}}`,
		},
		Copy: []string{
			"{{binary}} copy{{recursive_flag}} {{source_path}} {{copy_target}}",
		},
		ShellExec: []string{
			"{{binary}} shell{{workdir_flag}} {{sandbox_name}}{{command_args}}",
		},
		ShellLogin: []string{
			"{{binary}} shell{{workdir_flag}} {{sandbox_name}}{{command_args}}",
		},
	}
}

func (t RuntimeCommandTemplates) ApplyDefaults(defaults RuntimeCommandTemplates) RuntimeCommandTemplates {
	for _, field := range t.orderedFields() {
		*field.values = applyDefaultCommandList(*field.values, defaults.templates(field.key))
	}
	return t
}

func (t RuntimeCommandTemplates) IsZero() bool {
	for _, field := range t.orderedFields() {
		if len(*field.values) != 0 {
			return false
		}
	}
	return true
}

func (t RuntimeCommandTemplates) templates(kind runtimeCommandKind) []string {
	for _, field := range t.orderedFields() {
		if field.key == kind {
			return copyCommandList(*field.values)
		}
	}
	return nil
}

// orderedFields is the single field table every per-field operation iterates:
// adding a template kind means adding one row here (plus the struct field and
// its wire counterpart), not editing five hand-written functions. The values
// are pointers into the receiver so table-driven mutation works; methods with
// value receivers rely on that to update their own copy before returning it.
func (t *RuntimeCommandTemplates) orderedFields() []runtimeCommandTemplateField {
	return []runtimeCommandTemplateField{
		{key: runtimeCommandVersion, values: &t.Version},
		{key: runtimeCommandList, values: &t.List},
		{key: runtimeCommandCreate, values: &t.Create},
		{key: runtimeCommandStart, values: &t.Start},
		{key: runtimeCommandStop, values: &t.Stop},
		{key: runtimeCommandDelete, values: &t.Delete},
		{key: runtimeCommandClone, values: &t.Clone},
		{key: runtimeCommandBootstrap, values: &t.Bootstrap},
		{key: runtimeCommandWorkspaceSeedPrepare, values: &t.WorkspaceSeedPrepare},
		{key: runtimeCommandCopy, values: &t.Copy},
		{key: runtimeCommandShellExec, values: &t.ShellExec},
		{key: runtimeCommandShellLogin, values: &t.ShellLogin},
	}
}

func loadRuntimeCommandsFile(path string) (RuntimeCommandTemplates, error) {
	var wrapped runtimeCommandsExample
	if err := readYAMLFile(path, &wrapped); err == nil && !wrapped.RuntimeCommands.IsZero() {
		return wrapped.RuntimeCommands, nil
	}

	var commands RuntimeCommandTemplates
	if err := readYAMLFile(path, &commands); err != nil {
		return RuntimeCommandTemplates{}, metadataCorruption("failed to load guest setup commands", err, map[string]any{"path": path})
	}

	return commands, nil
}

func configYAMLBytes(cfg Config) ([]byte, error) {
	settings := struct {
		Daemon struct {
			Autostart                       bool   `yaml:"autostart"`
			Restore                         string `yaml:"restore"`
			VirtioFSReclaim                 bool   `yaml:"virtiofs_reclaim"`
			VirtioFSReclaimThresholdPercent int    `yaml:"virtiofs_reclaim_threshold_percent"`
		} `yaml:"daemon"`
	}{}
	settings.Daemon.Autostart = cfg.Daemon.Autostart
	settings.Daemon.Restore = cfg.Daemon.Restore
	settings.Daemon.VirtioFSReclaim = cfg.Daemon.VirtioFSReclaim
	settings.Daemon.VirtioFSReclaimThresholdPercent = cfg.Daemon.VirtioFSReclaimThresholdPercent
	return yamlBytes(settings)
}

func nodeYAMLBytes(node Node, defaults RuntimeCommandTemplates) ([]byte, error) {
	_ = defaults
	return yamlBytes(newNodeFileWire(node))
}

func configFileNeedsRefresh(data []byte) bool {
	var stored struct {
		Daemon struct {
			Autostart                       *bool  `yaml:"autostart"`
			Restore                         string `yaml:"restore"`
			VirtioFSReclaim                 *bool  `yaml:"virtiofs_reclaim"`
			VirtioFSReclaimThresholdPercent int    `yaml:"virtiofs_reclaim_threshold_percent"`
		} `yaml:"daemon"`
	}
	return yaml.Unmarshal(data, &stored) != nil || stored.Daemon.Autostart == nil || stored.Daemon.Restore == "" || stored.Daemon.VirtioFSReclaim == nil || stored.Daemon.VirtioFSReclaimThresholdPercent == 0
}

// nodeFileNeedsRefresh decides whether an on-disk node.yaml is stale by
// PARSING it, not by substring-matching serialized text: field-order or
// quoting changes in the YAML marshaler must not silently flip refresh
// behavior. A file needs a rewrite when it still carries transient fields
// (status, reconciliation state), when its persisted lifecycle_state disagrees
// with the loaded node, or when its runtime_commands include legacy CLI kinds.
func nodeFileNeedsRefresh(data []byte, node Node, _ RuntimeCommandTemplates) bool {
	var stored struct {
		Status                 *string        `yaml:"status"`
		LastReconciledAt       *string        `yaml:"last_reconciled_at"`
		LastRuntimeObservation map[string]any `yaml:"last_runtime_observation"`
		LifecycleState         *NodeStatus    `yaml:"lifecycle_state"`
		RuntimeCommands        map[string]any `yaml:"runtime_commands"`
	}
	if yaml.Unmarshal(data, &stored) != nil {
		return true
	}
	if containsUnsupportedRuntimeCommandYAML(stored.RuntimeCommands) {
		return true
	}
	if stored.Status != nil || stored.LastReconciledAt != nil || stored.LastRuntimeObservation != nil {
		return true
	}

	persistedLifecycle := nodeLifecycleState(node)
	if persistedLifecycle == "" {
		return stored.LifecycleState != nil
	}
	return stored.LifecycleState == nil || *stored.LifecycleState != persistedLifecycle
}

func containsUnsupportedRuntimeCommandYAML(runtimeCommands map[string]any) bool {
	for key := range runtimeCommands {
		if !supportedRuntimeCommandKind(runtimeCommandKind(key)) {
			return true
		}
	}
	return false
}

func writeConfigFile(path string, cfg Config) error {
	data, err := configYAMLBytes(cfg)
	if err != nil {
		return err
	}

	return atomicWriteFile(path, data, 0o644)
}

func writeNodeFile(path string, node Node, defaults RuntimeCommandTemplates) error {
	data, err := nodeYAMLBytes(node, defaults)
	if err != nil {
		return err
	}

	return atomicWriteFile(path, data, 0o644)
}
