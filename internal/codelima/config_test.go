package codelima

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfigYAMLIncludesGlobalLimaCommandsWithComment(t *testing.T) {
	t.Parallel()

	data, err := defaultConfigYAML(DefaultConfig("/tmp/codelima-test"))
	if err != nil {
		t.Fatalf("defaultConfigYAML() error = %v", err)
	}

	output := string(data)
	if !strings.Contains(output, globalLimaCommandsComment) {
		t.Fatalf("expected config yaml to include the global lima command comment, got %s", output)
	}
	if !strings.Contains(output, "lima_commands:") {
		t.Fatalf("expected config yaml to include lima_commands, got %s", output)
	}
	if !strings.Contains(output, "create:") || !strings.Contains(output, "{{binary}} create -y --name {{instance_name}} --cpus=2 --memory=4 --disk=20 {{template_path}}") {
		t.Fatalf("expected config yaml to include default create command, got %s", output)
	}
	if !strings.Contains(output, "workspace_seed_prepare:") || !strings.Contains(output, `sudo rm -rf {{target_path}} && sudo mkdir -p {{target_parent}} && sudo chown "$(id -un)":"$(id -gn)" {{target_parent}}`) {
		t.Fatalf("expected config yaml to include default workspace seed prepare command, got %s", output)
	}
	if !strings.Contains(output, "start:") || !strings.Contains(output, "{{binary}} start -y {{instance_name}}") {
		t.Fatalf("expected config yaml to include default start command, got %s", output)
	}
	if !strings.Contains(output, "bootstrap: []") {
		t.Fatalf("expected config yaml to include empty bootstrap command list, got %s", output)
	}
}

func TestLoadConfigAppliesDefaultLimaCommandsWhenFileOmitsThem(t *testing.T) {
	t.Parallel()

	home := filepath.Join(t.TempDir(), ".codelima")
	configPath := filepath.Join(home, "_config", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(config dir) error = %v", err)
	}

	legacyConfig := `metadata_root: /tmp/legacy
lima_home: ~/.lima
default_agent_profile: codex-cli
default_template: template:default
default_resources:
  cpus: 2
  memory_gib: 4
  disk_gib: 20
snapshot:
  excludes:
    - .codelima
    - .git
agent_profiles_dir: /tmp/legacy/_config/agent-profiles
`
	if err := os.WriteFile(configPath, []byte(legacyConfig), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	cfg, err := LoadConfig(home)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if got := strings.Join(cfg.LimaCommands.Start, "|"); got != "{{binary}} start -y {{instance_name}}" {
		t.Fatalf("expected default start command, got %q", got)
	}
	if got := strings.Join(cfg.LimaCommands.Create, "|"); got != "{{binary}} create -y --name {{instance_name}} --cpus=2 --memory=4 --disk=20 {{template_path}}" {
		t.Fatalf("expected default create command, got %q", got)
	}
	if got := strings.Join(cfg.LimaCommands.WorkspaceSeedPrepare, "|"); got != `sudo rm -rf {{target_path}} && sudo mkdir -p {{target_parent}} && sudo chown "$(id -un)":"$(id -gn)" {{target_parent}}` {
		t.Fatalf("expected default workspace seed prepare command, got %q", got)
	}
	if got := strings.Join(cfg.LimaCommands.Clone, "|"); got != "{{binary}} clone -y {{source_instance}} {{target_instance}}" {
		t.Fatalf("expected default clone command, got %q", got)
	}
	if len(cfg.LimaCommands.Bootstrap) != 0 {
		t.Fatalf("expected default bootstrap commands to be empty, got %v", cfg.LimaCommands.Bootstrap)
	}
}

func TestEnsureLayoutBackfillsConfigWithGlobalLimaCommands(t *testing.T) {
	t.Parallel()

	home := filepath.Join(t.TempDir(), ".codelima")
	configPath := filepath.Join(home, "_config", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(config dir) error = %v", err)
	}
	if err := os.WriteFile(configPath, []byte("default_agent_profile: codex-cli\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}

	cfg, err := LoadConfig(home)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	store := NewStore(cfg)
	if err := store.EnsureLayout(); err != nil {
		t.Fatalf("EnsureLayout() error = %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(config) error = %v", err)
	}

	output := string(data)
	if !strings.Contains(output, globalLimaCommandsComment) {
		t.Fatalf("expected rewritten config to include the global lima command comment, got %s", output)
	}
	if !strings.Contains(output, "\nlima_commands:\n") {
		t.Fatalf("expected rewritten config to include lima_commands, got %s", output)
	}
}
