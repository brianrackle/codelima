package codelima

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestProjectMarshalsBootstrapCommandsAndReadsLegacySetupCommands(t *testing.T) {
	t.Parallel()

	project := Project{
		ID:                 "project-1",
		Slug:               "root",
		WorkspacePath:      "/workspace/root",
		EnvironmentConfigs: []string{"shared-dev", "lang-go"},
		LimaCommands: LimaCommandTemplates{
			Bootstrap: []string{"./script/setup", "direnv allow"},
			Start:     []string{"{{binary}} start {{instance_name}} --vm-type=vz"},
			Shell:     []string{"{{binary}} shell{{workdir_flag}} {{instance_name}}{{command_args}}"},
		},
	}

	yamlPayload, err := yaml.Marshal(project)
	if err != nil {
		t.Fatalf("yaml.Marshal(project) error = %v", err)
	}

	if strings.Contains(string(yamlPayload), "setup_commands:") {
		t.Fatalf("expected yaml output to avoid setup_commands, got %s", string(yamlPayload))
	}

	if strings.Contains(string(yamlPayload), "environment_commands:") {
		t.Fatalf("expected yaml output to avoid environment_commands, got %s", string(yamlPayload))
	}

	if !strings.Contains(string(yamlPayload), "environment_configs:") {
		t.Fatalf("expected yaml output to include environment_configs, got %s", string(yamlPayload))
	}

	if !strings.Contains(string(yamlPayload), "lima_commands:") {
		t.Fatalf("expected yaml output to include lima_commands, got %s", string(yamlPayload))
	}

	if !strings.Contains(string(yamlPayload), "bootstrap:") || !strings.Contains(string(yamlPayload), "./script/setup") {
		t.Fatalf("expected yaml output to include bootstrap commands, got %s", string(yamlPayload))
	}

	if !strings.Contains(string(yamlPayload), "start:") || !strings.Contains(string(yamlPayload), "{{binary}} start {{instance_name}} --vm-type=vz") {
		t.Fatalf("expected yaml output to include custom start command, got %s", string(yamlPayload))
	}

	jsonPayload, err := json.Marshal(project)
	if err != nil {
		t.Fatalf("json.Marshal(project) error = %v", err)
	}

	if strings.Contains(string(jsonPayload), "setup_commands") {
		t.Fatalf("expected json output to avoid setup_commands, got %s", string(jsonPayload))
	}

	if strings.Contains(string(jsonPayload), "environment_commands") {
		t.Fatalf("expected json output to avoid environment_commands, got %s", string(jsonPayload))
	}

	if !strings.Contains(string(jsonPayload), "bootstrap") {
		t.Fatalf("expected json output to include bootstrap commands, got %s", string(jsonPayload))
	}

	if !strings.Contains(string(jsonPayload), "environment_configs") {
		t.Fatalf("expected json output to include environment_configs, got %s", string(jsonPayload))
	}

	if !strings.Contains(string(jsonPayload), "lima_commands") {
		t.Fatalf("expected json output to include lima_commands, got %s", string(jsonPayload))
	}

	var fromLegacyYAML Project
	if err := yaml.Unmarshal([]byte(`
id: project-1
slug: root
workspace_path: /workspace/root
environment_configs:
  - shared-dev
  - lang-go
setup_commands:
  - ./script/setup
  - direnv allow
lima_commands:
  start:
    - "{{binary}} start {{instance_name}} --vm-type=vz"
  shell:
    - "{{binary}} shell{{workdir_flag}} {{instance_name}}{{command_args}}"
`), &fromLegacyYAML); err != nil {
		t.Fatalf("yaml.Unmarshal(legacy) error = %v", err)
	}

	if got := strings.Join(fromLegacyYAML.LimaCommands.Bootstrap, "|"); got != "./script/setup|direnv allow" {
		t.Fatalf("expected legacy yaml setup commands to load into bootstrap, got %q", got)
	}

	if got := strings.Join(fromLegacyYAML.EnvironmentConfigs, "|"); got != "shared-dev|lang-go" {
		t.Fatalf("expected environment config refs to load from yaml, got %q", got)
	}

	if got := strings.Join(fromLegacyYAML.LimaCommands.Start, "|"); got != "{{binary}} start {{instance_name}} --vm-type=vz" {
		t.Fatalf("expected lima start command to load from yaml, got %q", got)
	}

	var fromLegacyJSON Project
	if err := json.Unmarshal([]byte(`{
  "id": "project-1",
  "slug": "root",
  "workspace_path": "/workspace/root",
  "environment_configs": ["shared-dev", "lang-go"],
  "setup_commands": ["./script/setup", "direnv allow"],
  "lima_commands": {
    "start": ["{{binary}} start {{instance_name}} --vm-type=vz"],
    "shell": ["{{binary}} shell{{workdir_flag}} {{instance_name}}{{command_args}}"]
  }
}`), &fromLegacyJSON); err != nil {
		t.Fatalf("json.Unmarshal(legacy) error = %v", err)
	}

	if got := strings.Join(fromLegacyJSON.LimaCommands.Bootstrap, "|"); got != "./script/setup|direnv allow" {
		t.Fatalf("expected legacy json setup commands to load into bootstrap, got %q", got)
	}

	if got := strings.Join(fromLegacyJSON.EnvironmentConfigs, "|"); got != "shared-dev|lang-go" {
		t.Fatalf("expected environment config refs to load from json, got %q", got)
	}

	if got := strings.Join(fromLegacyJSON.LimaCommands.Shell, "|"); got != "{{binary}} shell{{workdir_flag}} {{instance_name}}{{command_args}}" {
		t.Fatalf("expected lima shell command to load from json, got %q", got)
	}
}

func TestNodeMarshalsLimaCommandsWhenConfigured(t *testing.T) {
	t.Parallel()

	node := Node{
		ID:               "node-1",
		Slug:             "root-node",
		ProjectID:        "project-1",
		Runtime:          RuntimeVM,
		Provider:         ProviderLima,
		LimaInstanceName: "root-root-node-12345678",
		Status:           NodeStatusCreated,
		AgentProfileName: "codex-cli",
		LimaCommands: LimaCommandTemplates{
			Start: []string{"{{binary}} start {{instance_name}} --vm-type=vz"},
			Copy:  []string{"{{binary}} copy --backend=rsync{{recursive_flag}} {{source_path}} {{copy_target}}"},
		},
	}

	yamlPayload, err := yaml.Marshal(node)
	if err != nil {
		t.Fatalf("yaml.Marshal(node) error = %v", err)
	}

	output := string(yamlPayload)
	if !strings.Contains(output, "lima_commands:") {
		t.Fatalf("expected yaml output to include lima_commands, got %s", output)
	}
	if !strings.Contains(output, "start:") || !strings.Contains(output, "{{binary}} start {{instance_name}} --vm-type=vz") {
		t.Fatalf("expected yaml output to include start override, got %s", output)
	}
	if !strings.Contains(output, "copy:") || !strings.Contains(output, "{{binary}} copy --backend=rsync{{recursive_flag}} {{source_path}} {{copy_target}}") {
		t.Fatalf("expected yaml output to include copy override, got %s", output)
	}

	node.LimaCommands = LimaCommandTemplates{}
	yamlPayload, err = yaml.Marshal(node)
	if err != nil {
		t.Fatalf("yaml.Marshal(node no overrides) error = %v", err)
	}
	if strings.Contains(string(yamlPayload), "lima_commands:") {
		t.Fatalf("expected yaml output to omit zero-value lima_commands, got %s", string(yamlPayload))
	}
}

func TestBootstrapStateMarshalsBootstrapCommandsAndReadsLegacySetupCommands(t *testing.T) {
	t.Parallel()

	state := BootstrapState{
		AgentProfileName:  "codex-cli",
		InstallCommands:   []string{"mise install"},
		BootstrapCommands: []string{"./script/setup"},
		ValidationCommand: "command -v sh",
		LaunchCommand:     "codex",
		Environment:       map[string]string{"CODELIMA": "1"},
	}

	yamlPayload, err := yaml.Marshal(state)
	if err != nil {
		t.Fatalf("yaml.Marshal(state) error = %v", err)
	}

	if strings.Contains(string(yamlPayload), "setup_commands:") {
		t.Fatalf("expected yaml bootstrap output to avoid setup_commands, got %s", string(yamlPayload))
	}

	if strings.Contains(string(yamlPayload), "environment_commands:") {
		t.Fatalf("expected yaml bootstrap output to avoid environment_commands, got %s", string(yamlPayload))
	}

	if !strings.Contains(string(yamlPayload), "bootstrap_commands:") {
		t.Fatalf("expected yaml bootstrap output to include bootstrap_commands, got %s", string(yamlPayload))
	}

	var legacy BootstrapState
	if err := yaml.Unmarshal([]byte(`
agent_profile_name: codex-cli
install_commands:
  - mise install
setup_commands:
  - ./script/setup
validation_command: command -v sh
launch_command: codex
environment:
  CODELIMA: "1"
`), &legacy); err != nil {
		t.Fatalf("yaml.Unmarshal(legacy bootstrap) error = %v", err)
	}

	if got := strings.Join(legacy.BootstrapCommands, "|"); got != "./script/setup" {
		t.Fatalf("expected legacy bootstrap setup commands to load, got %q", got)
	}
}

func TestBootstrapCommentUsesBootstrapCommandsLabel(t *testing.T) {
	t.Parallel()

	comment := bootstrapComment(BootstrapState{
		AgentProfileName:  "codex-cli",
		BootstrapCommands: []string{"./script/setup"},
	})

	if strings.Contains(comment, "setup_commands") {
		t.Fatalf("expected bootstrap comment to avoid setup_commands, got %s", comment)
	}

	if strings.Contains(comment, "environment_commands") {
		t.Fatalf("expected bootstrap comment to avoid environment_commands, got %s", comment)
	}

	if !strings.Contains(comment, "bootstrap_commands") {
		t.Fatalf("expected bootstrap comment to include bootstrap_commands, got %s", comment)
	}
}
