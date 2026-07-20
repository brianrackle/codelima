package codelima

import (
	"slices"

	"gopkg.in/yaml.v3"
)

const ()

type runtimeCommandsExample struct {
	RuntimeCommands RuntimeCommandTemplates `yaml:"runtime_commands"`
}

type runtimeCommandTemplateField struct {
	key    runtimeCommandKind
	values *[]string
}

func defaultRuntimeCommandTemplates() RuntimeCommandTemplates {
	return RuntimeCommandTemplates{
		Bootstrap: []string{},
		WorkspaceSeedPrepare: []string{
			`rm -rf {{target_path}} && mkdir -p {{target_parent}}`,
		},
	}
}

// legacyMSBCommandTemplates is retained only to recognize and remove the exact
// built-in templates written by CodeLima before the Go SDK migration. These
// strings are never executed by the production runtime.
func legacyMSBCommandTemplates() RuntimeCommandTemplates {
	return RuntimeCommandTemplates{
		Version: []string{
			"{{binary}} --version",
		},
		List: []string{
			"{{binary}} ls --format json",
		},
		Create: []string{
			"{{binary}} create --name {{sandbox_name}} --cpus 2 --memory 4G --init auto --shell /bin/bash{{mount_flags}}{{port_flags}}{{net_flags}} {{image}}",
			"{{binary}} stop {{sandbox_name}}",
		},
		Start: []string{
			"{{binary}} start {{sandbox_name}}",
		},
		Stop: []string{
			"{{binary}} stop {{sandbox_name}}",
		},
		Delete: []string{
			"{{binary}} rm -f {{sandbox_name}}",
		},
		Clone: []string{
			"{{binary}} snapshot create {{snapshot_name}} --from {{source_sandbox}} --force",
			"{{binary}} run --snapshot {{snapshot_name}} --name {{sandbox_name}} --detach --cpus 2 --memory 4G --init auto --shell /bin/bash{{mount_flags}}{{port_flags}}{{net_flags}}",
			"{{binary}} stop {{sandbox_name}}",
			"{{binary}} snapshot rm {{snapshot_name}}",
		},
		Bootstrap: defaultRuntimeCommandTemplates().Bootstrap,
		WorkspaceSeedPrepare: []string{
			`rm -rf {{target_path}} && mkdir -p {{target_parent}}`,
		},
		Copy: []string{
			"{{binary}} copy {{source_path}} {{copy_target}}",
		},
		ShellExec: []string{
			"{{binary}} exec{{workdir_flag}} {{sandbox_name}}{{command_args}}",
		},
		ShellLogin: []string{
			"{{binary}} exec -t{{workdir_flag}} {{sandbox_name}} -- {{login_command}}",
		},
	}
}

// guestRuntimeCommandKinds are the only template kinds the Go SDK executes;
// every other kind is a legacy CLI relic that is recognized (for removal) but
// never run.
func guestRuntimeCommandKind(kind runtimeCommandKind) bool {
	return kind == runtimeCommandBootstrap || kind == runtimeCommandWorkspaceSeedPrepare
}

func validateSDKRuntimeCommandTemplates(templates ...RuntimeCommandTemplates) error {
	legacy := legacyMSBCommandTemplates()
	for _, template := range templates {
		for _, field := range template.orderedFields() {
			if guestRuntimeCommandKind(field.key) || len(*field.values) == 0 {
				continue
			}
			if slices.Equal(*field.values, legacy.templates(field.key)) {
				continue
			}
			return preconditionFailed("runtime command override is unavailable with the Microsandbox Go SDK", map[string]any{
				"command": string(field.key),
			})
		}
	}
	return nil
}

func removeLegacyMSBCommandTemplates(template RuntimeCommandTemplates) RuntimeCommandTemplates {
	legacy := legacyMSBCommandTemplates()
	for _, field := range template.orderedFields() {
		if guestRuntimeCommandKind(field.key) {
			continue
		}
		if slices.Equal(*field.values, legacy.templates(field.key)) {
			*field.values = nil
		}
	}
	return template
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
		if !guestRuntimeCommandKind(runtimeCommandKind(key)) {
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
