package codelima

import (
	"bytes"
	"os"
	"slices"

	"gopkg.in/yaml.v3"
)

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
			"{{binary}} start -y {{sandbox_name}}{{nested_virtualization_flag}}",
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

// effectiveRuntimeCommandTemplates is the single definition of runtime command
// precedence — built-in defaults, then global settings, then node metadata.
// resolveConfiguredRuntimeCommands renders these; runtimeCommandsAreBuiltIn
// asks which definition won without rendering placeholders first.
func effectiveRuntimeCommandTemplates(global, nodeCommands RuntimeCommandTemplates, kind runtimeCommandKind) []string {
	templates := defaultRuntimeCommandTemplates().templates(kind)
	templates = applyDefaultCommandList(global.templates(kind), templates)
	return applyDefaultCommandList(nodeCommands.templates(kind), templates)
}

// runtimeCommandsAreBuiltIn reports whether a kind still resolves to the
// command definitions CodeLima ships. Built-in commands execute as argv, which
// is what keeps a host shell profile from prepending output to machine-parsed
// limactl stdout; customized commands keep shell semantics because their
// templates are written against a shell. The check compares against the
// defaults rather than asking whether an override was supplied because the
// loaded settings always carry the defaults merged in.
func runtimeCommandsAreBuiltIn(global, nodeCommands RuntimeCommandTemplates, kind runtimeCommandKind) bool {
	return slices.Equal(effectiveRuntimeCommandTemplates(global, nodeCommands, kind), defaultRuntimeCommandTemplates().templates(kind))
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

// configYAMLBytes renders settings.yaml on top of whatever is already there.
// The reader (LoadConfig) unmarshals the whole Config, so users legitimately
// hand-edit keys the writer does not model; a writer that serialized only its
// own struct silently wiped those keys every time a new daemon key shipped.
// The document is therefore round-tripped as a yaml.Node — comments, key order,
// and unknown/future keys survive — and only the `daemon` key is replaced.
//
// Inside `daemon` the writer is authoritative: the block is rewritten
// wholesale, which is how a retired key is dropped. That is the migration path
// for virtiofs_reclaim_threshold_percent, which no longer has any effect.
func configYAMLBytes(cfg Config, existing []byte) ([]byte, error) {
	daemonSettings := struct {
		Autostart       bool   `yaml:"autostart"`
		Restore         string `yaml:"restore"`
		VirtioFSReclaim bool   `yaml:"virtiofs_reclaim"`
	}{
		Autostart:       cfg.Daemon.Autostart,
		Restore:         cfg.Daemon.Restore,
		VirtioFSReclaim: cfg.Daemon.VirtioFSReclaim,
	}

	daemonNode := &yaml.Node{}
	if err := daemonNode.Encode(daemonSettings); err != nil {
		return nil, err
	}

	settings := settingsMapping(existing)
	setMappingValue(settings, "daemon", daemonNode)
	return yamlBytes(settings)
}

// settingsMapping parses prior settings into the mapping the writer edits. A
// missing, empty, or unparseable file yields a fresh mapping: an operator can
// always recover hand-edited preferences from their editor, but a daemon that
// refuses to write the keys it owns because the file is malformed would be
// unusable.
func settingsMapping(data []byte) *yaml.Node {
	var document yaml.Node
	if len(bytes.TrimSpace(data)) > 0 && yaml.Unmarshal(data, &document) == nil {
		if len(document.Content) == 1 && document.Content[0].Kind == yaml.MappingNode {
			return document.Content[0]
		}
	}
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
}

func setMappingValue(mapping *yaml.Node, key string, value *yaml.Node) {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			mapping.Content[index+1] = value
			return
		}
	}
	mapping.Content = append(
		mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value,
	)
}

func nodeYAMLBytes(node Node, defaults RuntimeCommandTemplates) ([]byte, error) {
	_ = defaults
	return yamlBytes(newNodeFileWire(node))
}

// configFileNeedsRefresh reports whether settings.yaml is missing a daemon key
// this build owns, or still carries one it retired. Retired keys are listed so
// the one-time rewrite that removes them is self-limiting: once the key is
// gone the file stops being refreshed.
func configFileNeedsRefresh(data []byte) bool {
	var stored struct {
		Daemon struct {
			Autostart                       *bool  `yaml:"autostart"`
			Restore                         string `yaml:"restore"`
			VirtioFSReclaim                 *bool  `yaml:"virtiofs_reclaim"`
			VirtioFSReclaimThresholdPercent *int   `yaml:"virtiofs_reclaim_threshold_percent"`
		} `yaml:"daemon"`
	}
	return yaml.Unmarshal(data, &stored) != nil ||
		stored.Daemon.Autostart == nil ||
		stored.Daemon.Restore == "" ||
		stored.Daemon.VirtioFSReclaim == nil ||
		stored.Daemon.VirtioFSReclaimThresholdPercent != nil
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
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return metadataCorruption("failed to read settings.yaml", err, map[string]any{"path": path})
	}

	data, err := configYAMLBytes(cfg, existing)
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
