package codelima

import "strings"

const (
	globalLimaCommandsComment          = "# Global Lima command defaults for all projects.\n# Override them per project in CODELIMA_HOME/projects/<project-id>/project.yaml or per node in CODELIMA_HOME/nodes/<node-id>/node.yaml under lima_commands.\n"
	projectLimaCommandsComment         = "# Project-specific Lima command overrides.\n# Omitted commands inherit from CODELIMA_HOME/_config/config.yaml and can still be overridden per node.\n"
	projectLimaCommandsTemplateComment = "# Project-specific Lima command overrides.\n# Uncomment any entries below to override the global defaults from CODELIMA_HOME/_config/config.yaml.\n#\n"
	nodeLimaCommandsComment            = "# Node-specific Lima command overrides.\n# Omitted commands inherit from the project metadata file, then CODELIMA_HOME/_config/config.yaml.\n"
	nodeLimaCommandsTemplateComment    = "# Node-specific Lima command overrides.\n# Uncomment any entries below to override the inherited project and global defaults.\n#\n"
)

type limaCommandsExample struct {
	LimaCommands LimaCommandTemplates `yaml:"lima_commands"`
}

type limaCommandTemplateField struct {
	key    string
	values []string
}

func defaultLimaCommandTemplates() LimaCommandTemplates {
	return LimaCommandTemplates{
		TemplateCopy: []string{
			"{{binary}} template copy --fill {{locator}} -",
		},
		Create: []string{
			"{{binary}} create -y --name {{instance_name}} --cpus=2 --memory=4 --disk=20 {{template_path}}",
		},
		Start: []string{
			"{{binary}} start -y {{instance_name}}",
		},
		Stop: []string{
			"{{binary}} stop -y {{instance_name}}",
		},
		Delete: []string{
			"{{binary}} delete -f {{instance_name}}",
		},
		Clone: []string{
			"{{binary}} clone -y {{source_instance}} {{target_instance}}",
		},
		Bootstrap: []string{},
		WorkspaceSeedPrepare: []string{
			`sudo rm -rf {{target_path}} && sudo mkdir -p {{target_parent}} && sudo chown "$(id -un)":"$(id -gn)" {{target_parent}}`,
		},
		Copy: []string{
			"{{binary}} copy{{recursive_flag}} {{source_path}} {{copy_target}}",
		},
		Shell: []string{
			"{{binary}} shell{{workdir_flag}} {{instance_name}}{{command_args}}",
		},
	}
}

func (t LimaCommandTemplates) ApplyDefaults(defaults LimaCommandTemplates) LimaCommandTemplates {
	t.TemplateCopy = applyDefaultCommandList(t.TemplateCopy, defaults.TemplateCopy)
	t.Create = applyDefaultCommandList(t.Create, defaults.Create)
	t.Start = applyDefaultCommandList(t.Start, defaults.Start)
	t.Stop = applyDefaultCommandList(t.Stop, defaults.Stop)
	t.Delete = applyDefaultCommandList(t.Delete, defaults.Delete)
	t.Clone = applyDefaultCommandList(t.Clone, defaults.Clone)
	t.Bootstrap = applyDefaultCommandList(t.Bootstrap, defaults.Bootstrap)
	t.WorkspaceSeedPrepare = applyDefaultCommandList(t.WorkspaceSeedPrepare, defaults.WorkspaceSeedPrepare)
	t.Copy = applyDefaultCommandList(t.Copy, defaults.Copy)
	t.Shell = applyDefaultCommandList(t.Shell, defaults.Shell)
	return t
}

func (t LimaCommandTemplates) IsZero() bool {
	return len(t.TemplateCopy) == 0 &&
		len(t.Create) == 0 &&
		len(t.Start) == 0 &&
		len(t.Stop) == 0 &&
		len(t.Delete) == 0 &&
		len(t.Clone) == 0 &&
		len(t.Bootstrap) == 0 &&
		len(t.WorkspaceSeedPrepare) == 0 &&
		len(t.Copy) == 0 &&
		len(t.Shell) == 0
}

func (t LimaCommandTemplates) templates(kind limaCommandKind) []string {
	switch kind {
	case limaCommandTemplateCopy:
		return copyCommandList(t.TemplateCopy)
	case limaCommandCreate:
		return copyCommandList(t.Create)
	case limaCommandStart:
		return copyCommandList(t.Start)
	case limaCommandStop:
		return copyCommandList(t.Stop)
	case limaCommandDelete:
		return copyCommandList(t.Delete)
	case limaCommandClone:
		return copyCommandList(t.Clone)
	case limaCommandBootstrap:
		return copyCommandList(t.Bootstrap)
	case limaCommandWorkspaceSeedPrepare:
		return copyCommandList(t.WorkspaceSeedPrepare)
	case limaCommandCopy:
		return copyCommandList(t.Copy)
	case limaCommandShell:
		return copyCommandList(t.Shell)
	default:
		return nil
	}
}

func (t LimaCommandTemplates) orderedFields() []limaCommandTemplateField {
	return []limaCommandTemplateField{
		{key: "template_copy", values: t.TemplateCopy},
		{key: "create", values: t.Create},
		{key: "start", values: t.Start},
		{key: "stop", values: t.Stop},
		{key: "delete", values: t.Delete},
		{key: "clone", values: t.Clone},
		{key: "bootstrap", values: t.Bootstrap},
		{key: "workspace_seed_prepare", values: t.WorkspaceSeedPrepare},
		{key: "copy", values: t.Copy},
		{key: "shell", values: t.Shell},
	}
}

func loadLimaCommandsFile(path string) (LimaCommandTemplates, error) {
	var wrapped limaCommandsExample
	if err := readYAMLFile(path, &wrapped); err == nil && !wrapped.LimaCommands.IsZero() {
		return wrapped.LimaCommands, nil
	}

	var commands LimaCommandTemplates
	if err := readYAMLFile(path, &commands); err != nil {
		return LimaCommandTemplates{}, metadataCorruption("failed to load lima command overrides", err, map[string]any{"path": path})
	}

	return commands, nil
}

func loadOptionalLimaCommandsFile(path string) (LimaCommandTemplates, error) {
	if strings.TrimSpace(path) == "" {
		return LimaCommandTemplates{}, nil
	}

	return loadLimaCommandsFile(path)
}

func configYAMLBytes(cfg Config) ([]byte, error) {
	cfg.LimaCommands = cfg.LimaCommands.ApplyDefaults(defaultLimaCommandTemplates())

	data, err := yamlBytes(cfg)
	if err != nil {
		return nil, err
	}

	return insertCommentBeforeMarker(data, "lima_commands:", globalLimaCommandsComment), nil
}

func projectYAMLBytes(project Project, defaults LimaCommandTemplates) ([]byte, error) {
	data, err := yamlBytes(project)
	if err != nil {
		return nil, err
	}

	if project.LimaCommands.IsZero() {
		commentedDefaults, err := projectLimaCommandsCommentBlock(defaults.ApplyDefaults(defaultLimaCommandTemplates()))
		if err != nil {
			return nil, err
		}

		return appendCommentBlock(data, commentedDefaults), nil
	}

	return insertCommentBeforeMarker(data, "lima_commands:", projectLimaCommandsComment), nil
}

func nodeYAMLBytes(node Node, defaults LimaCommandTemplates) ([]byte, error) {
	data, err := yamlBytes(newNodeFileWire(node))
	if err != nil {
		return nil, err
	}

	if node.LimaCommands.IsZero() {
		commentedDefaults, err := nodeLimaCommandsCommentBlock(defaults.ApplyDefaults(defaultLimaCommandTemplates()))
		if err != nil {
			return nil, err
		}

		return appendCommentBlock(data, commentedDefaults), nil
	}

	return insertCommentBeforeMarker(data, "lima_commands:", nodeLimaCommandsComment), nil
}

func projectLimaCommandsCommentBlock(defaults LimaCommandTemplates) ([]byte, error) {
	return limaCommandsCommentBlock(projectLimaCommandsTemplateComment, defaults)
}

func nodeLimaCommandsCommentBlock(defaults LimaCommandTemplates) ([]byte, error) {
	return limaCommandsCommentBlock(nodeLimaCommandsTemplateComment, defaults)
}

func limaCommandsCommentBlock(header string, defaults LimaCommandTemplates) ([]byte, error) {
	example, err := yamlBytes(limaCommandsExample{LimaCommands: defaults})
	if err != nil {
		return nil, err
	}

	lines := []string{strings.TrimRight(header, "\n")}
	for _, line := range strings.Split(strings.TrimRight(string(example), "\n"), "\n") {
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
	current := string(data)
	if !strings.Contains(current, globalLimaCommandsComment) {
		return true
	}
	if !strings.Contains(current, "\nlima_commands:\n") {
		return true
	}

	for _, field := range defaultLimaCommandTemplates().orderedFields() {
		if !strings.Contains(current, "\n  "+field.key+":") {
			return true
		}
	}

	return false
}

func projectFileNeedsRefresh(data []byte, project Project, defaults LimaCommandTemplates) bool {
	current := string(data)

	if project.LimaCommands.IsZero() {
		if !strings.Contains(current, projectLimaCommandsTemplateComment) {
			return true
		}
		if !strings.Contains(current, "\n# lima_commands:\n") {
			return true
		}
		for _, field := range defaults.ApplyDefaults(defaultLimaCommandTemplates()).orderedFields() {
			if !strings.Contains(current, "\n#   "+field.key+":") {
				return true
			}
		}
		return false
	}

	if !strings.Contains(current, projectLimaCommandsComment) {
		return true
	}

	return !strings.Contains(current, "\nlima_commands:\n")
}

func nodeFileNeedsRefresh(data []byte, node Node, defaults LimaCommandTemplates) bool {
	current := string(data)
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

	if node.LimaCommands.IsZero() {
		if !strings.Contains(current, nodeLimaCommandsTemplateComment) {
			return true
		}
		if !strings.Contains(current, "\n# lima_commands:\n") {
			return true
		}
		for _, field := range defaults.ApplyDefaults(defaultLimaCommandTemplates()).orderedFields() {
			if !strings.Contains(current, "\n#   "+field.key+":") {
				return true
			}
		}
		return false
	}

	if !strings.Contains(current, nodeLimaCommandsComment) {
		return true
	}

	return !strings.Contains(current, "\nlima_commands:\n")
}

func writeConfigFile(path string, cfg Config) error {
	data, err := configYAMLBytes(cfg)
	if err != nil {
		return err
	}

	return atomicWriteFile(path, data, 0o644)
}

func writeProjectFile(path string, project Project, defaults LimaCommandTemplates) error {
	data, err := projectYAMLBytes(project, defaults)
	if err != nil {
		return err
	}

	return atomicWriteFile(path, data, 0o644)
}

func writeNodeFile(path string, node Node, defaults LimaCommandTemplates) error {
	data, err := nodeYAMLBytes(node, defaults)
	if err != nil {
		return err
	}

	return atomicWriteFile(path, data, 0o644)
}
