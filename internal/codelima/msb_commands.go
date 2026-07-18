package codelima

import (
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	projectRuntimeCommandsComment         = "# Project-specific guest setup commands.\n# Omitted commands inherit from CODELIMA_HOME/_config/config.yaml and can still be overridden per node.\n"
	projectRuntimeCommandsTemplateComment = "# Project-specific guest setup commands.\n# Uncomment entries below to override global guest command defaults.\n#\n"
)

type runtimeCommandsExample struct {
	RuntimeCommands RuntimeCommandTemplates `yaml:"runtime_commands"`
}

type runtimeCommandTemplateField struct {
	key    string
	values []string
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

func validateSDKRuntimeCommandTemplates(templates ...RuntimeCommandTemplates) error {
	legacy := legacyMSBCommandTemplates()
	for _, template := range templates {
		for _, field := range template.orderedFields() {
			if field.key == "bootstrap" || field.key == "workspace_seed_prepare" || len(field.values) == 0 {
				continue
			}
			if slices.Equal(field.values, legacy.templates(runtimeCommandKind(field.key))) {
				continue
			}
			return preconditionFailed("runtime command override is unavailable with the Microsandbox Go SDK", map[string]any{
				"command": field.key,
			})
		}
	}
	return nil
}

func removeLegacyMSBCommandTemplates(template RuntimeCommandTemplates) RuntimeCommandTemplates {
	legacy := legacyMSBCommandTemplates()
	if slices.Equal(template.Version, legacy.Version) {
		template.Version = nil
	}
	if slices.Equal(template.List, legacy.List) {
		template.List = nil
	}
	if slices.Equal(template.Create, legacy.Create) {
		template.Create = nil
	}
	if slices.Equal(template.Start, legacy.Start) {
		template.Start = nil
	}
	if slices.Equal(template.Stop, legacy.Stop) {
		template.Stop = nil
	}
	if slices.Equal(template.Delete, legacy.Delete) {
		template.Delete = nil
	}
	if slices.Equal(template.Clone, legacy.Clone) {
		template.Clone = nil
	}
	if slices.Equal(template.Copy, legacy.Copy) {
		template.Copy = nil
	}
	if slices.Equal(template.ShellExec, legacy.ShellExec) {
		template.ShellExec = nil
	}
	if slices.Equal(template.ShellLogin, legacy.ShellLogin) {
		template.ShellLogin = nil
	}
	return template
}

func (t RuntimeCommandTemplates) ApplyDefaults(defaults RuntimeCommandTemplates) RuntimeCommandTemplates {
	t.Version = applyDefaultCommandList(t.Version, defaults.Version)
	t.List = applyDefaultCommandList(t.List, defaults.List)
	t.Create = applyDefaultCommandList(t.Create, defaults.Create)
	t.Start = applyDefaultCommandList(t.Start, defaults.Start)
	t.Stop = applyDefaultCommandList(t.Stop, defaults.Stop)
	t.Delete = applyDefaultCommandList(t.Delete, defaults.Delete)
	t.Clone = applyDefaultCommandList(t.Clone, defaults.Clone)
	t.Bootstrap = applyDefaultCommandList(t.Bootstrap, defaults.Bootstrap)
	t.WorkspaceSeedPrepare = applyDefaultCommandList(t.WorkspaceSeedPrepare, defaults.WorkspaceSeedPrepare)
	t.Copy = applyDefaultCommandList(t.Copy, defaults.Copy)
	t.ShellExec = applyDefaultCommandList(t.ShellExec, defaults.ShellExec)
	t.ShellLogin = applyDefaultCommandList(t.ShellLogin, defaults.ShellLogin)
	return t
}

func (t RuntimeCommandTemplates) IsZero() bool {
	return len(t.Version) == 0 &&
		len(t.List) == 0 &&
		len(t.Create) == 0 &&
		len(t.Start) == 0 &&
		len(t.Stop) == 0 &&
		len(t.Delete) == 0 &&
		len(t.Clone) == 0 &&
		len(t.Bootstrap) == 0 &&
		len(t.WorkspaceSeedPrepare) == 0 &&
		len(t.Copy) == 0 &&
		len(t.ShellExec) == 0 &&
		len(t.ShellLogin) == 0
}

func (t RuntimeCommandTemplates) templates(kind runtimeCommandKind) []string {
	switch kind {
	case runtimeCommandVersion:
		return copyCommandList(t.Version)
	case runtimeCommandList:
		return copyCommandList(t.List)
	case runtimeCommandCreate:
		return copyCommandList(t.Create)
	case runtimeCommandStart:
		return copyCommandList(t.Start)
	case runtimeCommandStop:
		return copyCommandList(t.Stop)
	case runtimeCommandDelete:
		return copyCommandList(t.Delete)
	case runtimeCommandClone:
		return copyCommandList(t.Clone)
	case runtimeCommandBootstrap:
		return copyCommandList(t.Bootstrap)
	case runtimeCommandWorkspaceSeedPrepare:
		return copyCommandList(t.WorkspaceSeedPrepare)
	case runtimeCommandCopy:
		return copyCommandList(t.Copy)
	case runtimeCommandShellExec:
		return copyCommandList(t.ShellExec)
	case runtimeCommandShellLogin:
		return copyCommandList(t.ShellLogin)
	default:
		return nil
	}
}

func (t RuntimeCommandTemplates) orderedFields() []runtimeCommandTemplateField {
	return []runtimeCommandTemplateField{
		{key: "version", values: t.Version},
		{key: "list", values: t.List},
		{key: "create", values: t.Create},
		{key: "start", values: t.Start},
		{key: "stop", values: t.Stop},
		{key: "delete", values: t.Delete},
		{key: "clone", values: t.Clone},
		{key: "bootstrap", values: t.Bootstrap},
		{key: "workspace_seed_prepare", values: t.WorkspaceSeedPrepare},
		{key: "copy", values: t.Copy},
		{key: "shell_exec", values: t.ShellExec},
		{key: "shell_login", values: t.ShellLogin},
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

func loadOptionalRuntimeCommandsFile(path string) (RuntimeCommandTemplates, error) {
	if strings.TrimSpace(path) == "" {
		return RuntimeCommandTemplates{}, nil
	}

	return loadRuntimeCommandsFile(path)
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

func projectYAMLBytes(project Project, defaults RuntimeCommandTemplates) ([]byte, error) {
	data, err := yamlBytes(project)
	if err != nil {
		return nil, err
	}

	if project.RuntimeCommands.IsZero() {
		commentedDefaults, err := projectRuntimeCommandsCommentBlock(defaults.ApplyDefaults(defaultRuntimeCommandTemplates()))
		if err != nil {
			return nil, err
		}

		return appendCommentBlock(data, commentedDefaults), nil
	}

	return insertCommentBeforeMarker(data, "runtime_commands:", projectRuntimeCommandsComment), nil
}

func nodeYAMLBytes(node Node, defaults RuntimeCommandTemplates) ([]byte, error) {
	_ = defaults
	return yamlBytes(newNodeFileWire(node))
}

func projectRuntimeCommandsCommentBlock(defaults RuntimeCommandTemplates) ([]byte, error) {
	return runtimeCommandsCommentBlock(projectRuntimeCommandsTemplateComment, defaults)
}

func runtimeCommandsCommentBlock(header string, defaults RuntimeCommandTemplates) ([]byte, error) {
	example, err := yamlBytes(runtimeCommandsExample{RuntimeCommands: defaults})
	if err != nil {
		return nil, err
	}

	lines := []string{strings.TrimRight(header, "\n")}
	for line := range strings.SplitSeq(strings.TrimRight(string(example), "\n"), "\n") {
		lines = append(lines, "# "+line)
	}

	return []byte(strings.Join(lines, "\n") + "\n"), nil
}

func insertCommentBeforeMarker(data []byte, marker string, comment string) []byte {
	current := string(data)
	index := strings.Index(current, marker)
	if index < 0 {
		return data
	}

	return []byte(current[:index] + comment + current[index:])
}

func appendCommentBlock(data []byte, commentBlock []byte) []byte {
	current := strings.TrimRight(string(data), "\n")
	comment := strings.TrimRight(string(commentBlock), "\n")
	if current == "" {
		return []byte(comment + "\n")
	}

	return []byte(current + "\n\n" + comment + "\n")
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

func nodeFileNeedsRefresh(data []byte, node Node, defaults RuntimeCommandTemplates) bool {
	_ = defaults
	current := string(data)
	if containsUnsupportedRuntimeCommandYAML(current) {
		return true
	}
	persistedLifecycle := nodeLifecycleState(node)

	if strings.Contains(current, "\nstatus:") || strings.Contains(current, "\nlast_reconciled_at:") || strings.Contains(current, "\nlast_runtime_observation:") {
		return true
	}
	if persistedLifecycle != "" && !strings.Contains(current, "\nlifecycle_state: "+persistedLifecycle) {
		return true
	}
	if persistedLifecycle == "" && strings.Contains(current, "\nlifecycle_state:") {
		return true
	}

	return false
}

func containsUnsupportedRuntimeCommandYAML(data string) bool {
	for _, field := range legacyMSBCommandTemplates().orderedFields() {
		if field.key == "bootstrap" || field.key == "workspace_seed_prepare" {
			continue
		}
		if strings.Contains(data, "\n  "+field.key+":") {
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

func writeProjectFile(path string, project Project, defaults RuntimeCommandTemplates) error {
	data, err := projectYAMLBytes(project, defaults)
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
