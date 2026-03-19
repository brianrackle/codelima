package codelima

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	MetadataRoot        string    `json:"metadata_root" yaml:"metadata_root"`
	LimaHome            string    `json:"lima_home" yaml:"lima_home"`
	DefaultAgentProfile string    `json:"default_agent_profile" yaml:"default_agent_profile"`
	DefaultTemplate     string    `json:"default_template" yaml:"default_template"`
	DefaultResources    Resources `json:"default_resources" yaml:"default_resources"`
	Snapshot            struct {
		Excludes []string `json:"excludes" yaml:"excludes"`
	} `json:"snapshot" yaml:"snapshot"`
	AgentProfilesDir string `json:"agent_profiles_dir" yaml:"agent_profiles_dir"`
}

func DefaultConfig(home string) Config {
	cfg := Config{
		MetadataRoot:        home,
		LimaHome:            expandHome("~/.lima"),
		DefaultAgentProfile: "codex-cli",
		DefaultTemplate:     "template:default",
		DefaultResources: Resources{
			CPUs:      2,
			MemoryGiB: 4,
			DiskGiB:   20,
		},
	}
	cfg.Snapshot.Excludes = []string{".codelima", ".git"}
	cfg.AgentProfilesDir = filepath.Join(home, "_config", "agent-profiles")

	return cfg
}

func LoadConfig(homeOverride string) (Config, error) {
	home := homeOverride
	if home == "" {
		home = os.Getenv("CODELIMA_HOME")
	}

	if home == "" {
		home = expandHome("~/.codelima")
	}

	var err error
	home, err = canonicalPath(home)
	if err != nil {
		return Config{}, invalidArgument("failed to resolve CODELIMA_HOME", map[string]any{"path": home})
	}

	cfg := DefaultConfig(home)
	configPath := filepath.Join(home, "_config", "config.yaml")
	if exists(configPath) {
		if err := readYAMLFile(configPath, &cfg); err != nil {
			return Config{}, metadataCorruption("failed to load config.yaml", err, map[string]any{"path": configPath})
		}
	}

	cfg.MetadataRoot = home
	if cfg.AgentProfilesDir == "" {
		cfg.AgentProfilesDir = filepath.Join(home, "_config", "agent-profiles")
	}

	if limaHome := os.Getenv("LIMA_HOME"); limaHome != "" {
		cfg.LimaHome = expandHome(limaHome)
	}

	cfg.LimaHome = expandHome(cfg.LimaHome)

	return cfg, nil
}

func (c Config) Summary() map[string]any {
	return map[string]any{
		"metadata_root":         c.MetadataRoot,
		"lima_home":             c.LimaHome,
		"default_agent_profile": c.DefaultAgentProfile,
		"default_template":      c.DefaultTemplate,
		"default_resources":     c.DefaultResources,
		"snapshot_excludes":     c.Snapshot.Excludes,
		"agent_profiles_dir":    c.AgentProfilesDir,
	}
}

func expandHome(input string) string {
	if input == "" {
		return input
	}

	if !strings.HasPrefix(input, "~") {
		return input
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return input
	}

	if input == "~" {
		return home
	}

	return filepath.Join(home, strings.TrimPrefix(input, "~/"))
}

func validateConfig(cfg Config) error {
	if cfg.MetadataRoot == "" {
		return invalidArgument("metadata root is required", nil)
	}

	if len(cfg.MetadataRoot) > 160 {
		return preconditionFailed("CODELIMA_HOME path is too long", map[string]any{"path": cfg.MetadataRoot})
	}

	if filepath.Clean(cfg.MetadataRoot) == filepath.Clean("/") {
		return invalidArgument("metadata root must not be /", nil)
	}

	if cfg.DefaultTemplate == "" {
		return invalidArgument("default template is required", nil)
	}

	if cfg.DefaultResources.CPUs <= 0 || cfg.DefaultResources.MemoryGiB <= 0 || cfg.DefaultResources.DiskGiB <= 0 {
		return invalidArgument("default resources must be positive", map[string]any{"resources": cfg.DefaultResources})
	}

	return nil
}

func defaultConfigYAML(cfg Config) ([]byte, error) {
	return yamlBytes(cfg)
}

func builtInProfiles() map[string]AgentProfile {
	return map[string]AgentProfile{
		"codex-cli": {
			Name:              "codex-cli",
			InstallCommands:   []string{},
			ValidationCommand: "command -v sh >/dev/null 2>&1",
			LaunchCommand:     "codex",
			Environment:       map[string]string{},
		},
		"claude-code": {
			Name:              "claude-code",
			InstallCommands:   []string{},
			ValidationCommand: "command -v sh >/dev/null 2>&1",
			LaunchCommand:     "claude",
			Environment:       map[string]string{},
		},
	}
}

type builtInEnvironmentConfigSpec struct {
	Slug     string
	Commands []string
}

func builtInEnvironmentConfigs() []builtInEnvironmentConfigSpec {
	return []builtInEnvironmentConfigSpec{
		{
			Slug: "codex",
			Commands: []string{
				"sudo snap install node --classic",
				"sudo npm install -g @openai/codex",
			},
		},
		{
			Slug: "claude-code",
			Commands: []string{
				"curl -fsSL https://claude.ai/install.sh | bash",
			},
		},
	}
}

func bootstrapComment(state BootstrapState) string {
	lines := []string{
		"",
		"# codelima.bootstrap:",
		fmt.Sprintf("#   agent_profile_name: %s", state.AgentProfileName),
		"#   install_commands:",
	}

	for _, command := range state.InstallCommands {
		lines = append(lines, fmt.Sprintf("#     - %s", command))
	}

	lines = append(lines, "#   environment_commands:")
	for _, command := range state.SetupCommands {
		lines = append(lines, fmt.Sprintf("#     - %s", command))
	}

	lines = append(lines, fmt.Sprintf("#   validation_command: %s", state.ValidationCommand))
	lines = append(lines, fmt.Sprintf("#   launch_command: %s", state.LaunchCommand))

	return strings.Join(lines, "\n") + "\n"
}
