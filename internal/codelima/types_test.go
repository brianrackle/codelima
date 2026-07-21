package codelima

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestNodeMarshalsRuntimeCommandsWhenConfigured(t *testing.T) {
	t.Parallel()

	node := Node{
		ID:               "node-1",
		Slug:             "root-node",
		Runtime:          RuntimeVM,
		Provider:         ProviderLima,
		SandboxName:      "root-root-node-12345678",
		Status:           NodeStatusCreated,
		AgentProfileName: "codex-cli",
		RuntimeCommands: RuntimeCommandTemplates{
			Start: []string{"{{binary}} start {{sandbox_name}} --vm-type=vz"},
			Copy:  []string{"{{binary}} copy --backend=rsync{{recursive_flag}} {{source_path}} {{copy_target}}"},
		},
	}

	yamlPayload, err := yaml.Marshal(node)
	if err != nil {
		t.Fatalf("yaml.Marshal(node) error = %v", err)
	}

	output := string(yamlPayload)
	if !strings.Contains(output, "runtime_commands:") {
		t.Fatalf("expected yaml output to include runtime_commands, got %s", output)
	}
	if !strings.Contains(output, "start:") || !strings.Contains(output, "{{binary}} start {{sandbox_name}} --vm-type=vz") {
		t.Fatalf("expected yaml output to include start override, got %s", output)
	}
	if !strings.Contains(output, "copy:") || !strings.Contains(output, "{{binary}} copy --backend=rsync{{recursive_flag}} {{source_path}} {{copy_target}}") {
		t.Fatalf("expected yaml output to include copy override, got %s", output)
	}

	node.RuntimeCommands = RuntimeCommandTemplates{}
	yamlPayload, err = yaml.Marshal(node)
	if err != nil {
		t.Fatalf("yaml.Marshal(node no overrides) error = %v", err)
	}
	if strings.Contains(string(yamlPayload), "runtime_commands:") {
		t.Fatalf("expected yaml output to omit zero-value runtime_commands, got %s", string(yamlPayload))
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
