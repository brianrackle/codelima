package codelima

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestProjectMarshalsEnvironmentCommandsAndReadsLegacySetupCommands(t *testing.T) {
	t.Parallel()

	project := Project{
		ID:                 "project-1",
		Slug:               "root",
		WorkspacePath:      "/workspace/root",
		EnvironmentConfigs: []string{"shared-dev", "lang-go"},
		SetupCommands:      []string{"./script/setup", "direnv allow"},
	}

	yamlPayload, err := yaml.Marshal(project)
	if err != nil {
		t.Fatalf("yaml.Marshal(project) error = %v", err)
	}

	if strings.Contains(string(yamlPayload), "setup_commands:") {
		t.Fatalf("expected yaml output to use environment_commands, got %s", string(yamlPayload))
	}

	if !strings.Contains(string(yamlPayload), "environment_commands:") {
		t.Fatalf("expected yaml output to include environment_commands, got %s", string(yamlPayload))
	}

	if !strings.Contains(string(yamlPayload), "environment_configs:") {
		t.Fatalf("expected yaml output to include environment_configs, got %s", string(yamlPayload))
	}

	jsonPayload, err := json.Marshal(project)
	if err != nil {
		t.Fatalf("json.Marshal(project) error = %v", err)
	}

	if strings.Contains(string(jsonPayload), "setup_commands") {
		t.Fatalf("expected json output to use environment_commands, got %s", string(jsonPayload))
	}

	if !strings.Contains(string(jsonPayload), "environment_commands") {
		t.Fatalf("expected json output to include environment_commands, got %s", string(jsonPayload))
	}

	if !strings.Contains(string(jsonPayload), "environment_configs") {
		t.Fatalf("expected json output to include environment_configs, got %s", string(jsonPayload))
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
`), &fromLegacyYAML); err != nil {
		t.Fatalf("yaml.Unmarshal(legacy) error = %v", err)
	}

	if got := strings.Join(fromLegacyYAML.SetupCommands, "|"); got != "./script/setup|direnv allow" {
		t.Fatalf("expected legacy yaml setup commands to load, got %q", got)
	}

	if got := strings.Join(fromLegacyYAML.EnvironmentConfigs, "|"); got != "shared-dev|lang-go" {
		t.Fatalf("expected environment config refs to load from yaml, got %q", got)
	}

	var fromLegacyJSON Project
	if err := json.Unmarshal([]byte(`{
  "id": "project-1",
  "slug": "root",
  "workspace_path": "/workspace/root",
  "environment_configs": ["shared-dev", "lang-go"],
  "setup_commands": ["./script/setup", "direnv allow"]
}`), &fromLegacyJSON); err != nil {
		t.Fatalf("json.Unmarshal(legacy) error = %v", err)
	}

	if got := strings.Join(fromLegacyJSON.SetupCommands, "|"); got != "./script/setup|direnv allow" {
		t.Fatalf("expected legacy json setup commands to load, got %q", got)
	}

	if got := strings.Join(fromLegacyJSON.EnvironmentConfigs, "|"); got != "shared-dev|lang-go" {
		t.Fatalf("expected environment config refs to load from json, got %q", got)
	}
}

func TestBootstrapStateMarshalsEnvironmentCommandsAndReadsLegacySetupCommands(t *testing.T) {
	t.Parallel()

	state := BootstrapState{
		AgentProfileName:  "codex-cli",
		InstallCommands:   []string{"mise install"},
		SetupCommands:     []string{"./script/setup"},
		ValidationCommand: "command -v sh",
		LaunchCommand:     "codex",
		Environment:       map[string]string{"CODELIMA": "1"},
	}

	yamlPayload, err := yaml.Marshal(state)
	if err != nil {
		t.Fatalf("yaml.Marshal(state) error = %v", err)
	}

	if strings.Contains(string(yamlPayload), "setup_commands:") {
		t.Fatalf("expected yaml bootstrap output to use environment_commands, got %s", string(yamlPayload))
	}

	if !strings.Contains(string(yamlPayload), "environment_commands:") {
		t.Fatalf("expected yaml bootstrap output to include environment_commands, got %s", string(yamlPayload))
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

	if got := strings.Join(legacy.SetupCommands, "|"); got != "./script/setup" {
		t.Fatalf("expected legacy bootstrap setup commands to load, got %q", got)
	}
}

func TestBootstrapCommentUsesEnvironmentCommandsLabel(t *testing.T) {
	t.Parallel()

	comment := bootstrapComment(BootstrapState{
		AgentProfileName: "codex-cli",
		SetupCommands:    []string{"./script/setup"},
	})

	if strings.Contains(comment, "setup_commands") {
		t.Fatalf("expected bootstrap comment to avoid setup_commands, got %s", comment)
	}

	if !strings.Contains(comment, "environment_commands") {
		t.Fatalf("expected bootstrap comment to include environment_commands, got %s", comment)
	}
}
