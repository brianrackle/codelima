package codelima

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	defaultAgentValidationCommand        = "command -v sh >/dev/null 2>&1"
	defaultNodeJSPrerequisitesCommand    = `apt-get update && apt-get install -y ca-certificates curl git && node_major="$(node -p 'process.versions.node.split(".")[0]' 2>/dev/null || printf '0')" && if [ "$node_major" -lt 22 ] || ! command -v npm >/dev/null 2>&1; then nodesource_script="$(mktemp)" && trap 'rm -f "$nodesource_script"' 0 && curl -fsSL https://deb.nodesource.com/setup_22.x -o "$nodesource_script" && bash "$nodesource_script" && apt-get install -y nodejs; fi`
	defaultNPMUserPrefixCommand          = `guest_user="${SUDO_USER:-$(id -un)}"; guest_home="$(getent passwd "$guest_user" | cut -d: -f6)"; test -n "$guest_home" && sudo -u "$guest_user" -H env HOME="$guest_home" sh -c 'mkdir -p "$HOME/.local/bin" && npm config set prefix "$HOME/.local"'`
	defaultCodexValidationCommand        = `command -v codex >/dev/null 2>&1`
	defaultClaudeCodeValidationCommand   = `command -v claude >/dev/null 2>&1`
	legacyCodexPrerequisitesCommand      = "apt-get update && apt-get install -y ca-certificates curl git"
	legacyCodexStandaloneInstallCommand  = `curl -fsSL https://chatgpt.com/codex/install.sh | CODEX_INSTALL_DIR=/usr/local/bin CODEX_NON_INTERACTIVE=1 sh`
	legacyClaudeCodeNativeInstallCommand = "curl -fsSL https://claude.ai/install.sh | bash"
	legacyCodexSnapNodeInstallCommand    = "sudo snap install node --classic"
	legacyCodexAptNodeInstallCommand     = "apt-get update && apt-get install -y ca-certificates curl git nodejs npm"
	legacyCodexNPMBinCommand             = `mkdir -p "$HOME/.local/bin"`
	legacyCodexNPMPrefixCommand          = `npm config set prefix "$HOME/.local"`
	legacyCodexPathCommand               = `for profile in "$HOME/.profile" "$HOME/.bashrc"; do grep -qxF 'export PATH="$HOME/.local/bin:$PATH"' "$profile" 2>/dev/null || printf '\nexport PATH="$HOME/.local/bin:$PATH"\n' >> "$profile"; done`
	legacyCodexUserNPMInstallCommand     = `PATH="$HOME/.local/bin:$PATH" npm install -g @openai/codex`
	legacyCodexGlobalNPMInstallCommand   = "sudo npm install -g @openai/codex"
)

var legacyGuessedDefaultPorts = []string{"3000:3000", "5173:5173", "8000:8000", "8080:8080"}

type Config struct {
	MetadataRoot        string                  `json:"metadata_root" yaml:"metadata_root"`
	DefaultAgentProfile string                  `json:"default_agent_profile" yaml:"default_agent_profile"`
	DefaultImage        string                  `json:"default_image" yaml:"default_image"`
	DefaultPorts        []string                `json:"default_ports" yaml:"default_ports"`
	RuntimeCommands     RuntimeCommandTemplates `json:"runtime_commands" yaml:"runtime_commands"`
	AgentProfilesDir    string                  `json:"agent_profiles_dir" yaml:"agent_profiles_dir"`
	Daemon              struct {
		Autostart                       bool   `json:"autostart" yaml:"autostart"`
		Restore                         string `json:"restore" yaml:"restore"`
		VirtioFSReclaim                 bool   `json:"virtiofs_reclaim" yaml:"virtiofs_reclaim"`
		VirtioFSReclaimThresholdPercent int    `json:"virtiofs_reclaim_threshold_percent" yaml:"virtiofs_reclaim_threshold_percent"`
	} `json:"daemon" yaml:"daemon"`
}

func DefaultConfig(home string) Config {
	cfg := Config{
		MetadataRoot:        home,
		DefaultAgentProfile: "codex-cli",
		DefaultImage:        "template:ubuntu",
		DefaultPorts:        []string{},
		RuntimeCommands:     defaultRuntimeCommandTemplates(),
	}
	cfg.AgentProfilesDir = filepath.Join(home, "_config", "agent-profiles")
	cfg.Daemon.Autostart = true
	cfg.Daemon.Restore = "respawn"
	cfg.Daemon.VirtioFSReclaim = true
	cfg.Daemon.VirtioFSReclaimThresholdPercent = defaultVirtioFSPressureThresholdPercent

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
	configPath := filepath.Join(home, "_config", "settings.yaml")
	if exists(configPath) {
		if err := readYAMLFile(configPath, &cfg); err != nil {
			return Config{}, metadataCorruption("failed to load config.yaml", err, map[string]any{"path": configPath})
		}
		if slices.Equal(cfg.DefaultPorts, legacyGuessedDefaultPorts) {
			cfg.DefaultPorts = []string{}
		}
	}

	cfg.MetadataRoot = home
	if cfg.AgentProfilesDir == "" {
		cfg.AgentProfilesDir = filepath.Join(home, "_config", "agent-profiles")
	}

	cfg.RuntimeCommands = cfg.RuntimeCommands.ApplyDefaults(defaultRuntimeCommandTemplates())
	if cfg.Daemon.Restore == "" {
		cfg.Daemon.Restore = "respawn"
	}

	return cfg, nil
}

func (c Config) Summary() map[string]any {
	return map[string]any{
		"metadata_root": c.MetadataRoot,
		"daemon": map[string]any{
			"autostart":                          c.Daemon.Autostart,
			"restore":                            c.Daemon.Restore,
			"virtiofs_reclaim":                   c.Daemon.VirtioFSReclaim,
			"virtiofs_reclaim_threshold_percent": c.Daemon.VirtioFSReclaimThresholdPercent,
		},
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

	if cfg.DefaultImage == "" {
		return invalidArgument("default image is required", nil)
	}

	if _, err := validatePorts(cfg.DefaultPorts); err != nil {
		return err
	}

	if cfg.Daemon.Restore != "respawn" && cfg.Daemon.Restore != "forget" {
		return invalidArgument("daemon.restore must be respawn or forget", map[string]any{"restore": cfg.Daemon.Restore})
	}
	if cfg.Daemon.VirtioFSReclaimThresholdPercent < 1 || cfg.Daemon.VirtioFSReclaimThresholdPercent > 95 {
		return invalidArgument("daemon.virtiofs_reclaim_threshold_percent must be between 1 and 95", map[string]any{"threshold_percent": cfg.Daemon.VirtioFSReclaimThresholdPercent})
	}

	return nil
}

func builtInProfiles() map[string]AgentProfile {
	return map[string]AgentProfile{
		"codex-cli": {
			Name:              "codex-cli",
			InstallCommands:   []string{},
			ValidationCommand: defaultAgentValidationCommand,
			LaunchCommand:     "codex",
			Environment:       map[string]string{},
		},
		"claude-code": {
			Name:              "claude-code",
			InstallCommands:   []string{},
			ValidationCommand: defaultAgentValidationCommand,
			LaunchCommand:     "claude",
			Environment:       map[string]string{},
		},
	}
}

type builtInEnvironmentConfigSpec struct {
	Slug              string
	BootstrapCommands []string
}

func builtInEnvironmentConfigs() []builtInEnvironmentConfigSpec {
	return []builtInEnvironmentConfigSpec{
		{
			Slug: "codex",
			BootstrapCommands: []string{
				defaultNodeJSPrerequisitesCommand,
				defaultNPMUserPrefixCommand,
				defaultNPMInstallCommand("@openai/codex", "codex"),
				defaultCodexValidationCommand,
			},
		},
		{
			Slug: "claude-code",
			BootstrapCommands: []string{
				defaultNodeJSPrerequisitesCommand,
				defaultNPMUserPrefixCommand,
				defaultNPMInstallCommand("@anthropic-ai/claude-code", "claude"),
				defaultClaudeCodeValidationCommand,
			},
		},
	}
}

func defaultNPMInstallCommand(packageName, executable string) string {
	return fmt.Sprintf(
		`guest_user="${SUDO_USER:-$(id -un)}"; guest_home="$(getent passwd "$guest_user" | cut -d: -f6)"; test -n "$guest_home" && sudo -u "$guest_user" -H env HOME="$guest_home" PATH="$guest_home/.local/bin:$PATH" npm install -g %s && ln -sfn "$guest_home/.local/bin/%s" /usr/local/bin/%s`,
		packageName,
		executable,
		executable,
	)
}

func legacyBuiltInEnvironmentConfigs() map[string][]builtInEnvironmentConfigSpec {
	return map[string][]builtInEnvironmentConfigSpec{
		"codex": {
			{Slug: "codex", BootstrapCommands: []string{legacyCodexPrerequisitesCommand, legacyCodexStandaloneInstallCommand, defaultCodexValidationCommand}},
			{Slug: "codex", BootstrapCommands: []string{legacyCodexSnapNodeInstallCommand, legacyCodexGlobalNPMInstallCommand}},
			{Slug: "codex", BootstrapCommands: []string{legacyCodexSnapNodeInstallCommand, legacyCodexNPMBinCommand, legacyCodexNPMPrefixCommand, legacyCodexPathCommand, legacyCodexUserNPMInstallCommand}},
			{Slug: "codex", BootstrapCommands: []string{legacyCodexAptNodeInstallCommand, legacyCodexNPMBinCommand, legacyCodexNPMPrefixCommand, legacyCodexPathCommand, legacyCodexUserNPMInstallCommand}},
		},
		"claude-code": {
			{Slug: "claude-code", BootstrapCommands: []string{legacyClaudeCodeNativeInstallCommand}},
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

	lines = append(lines, "#   bootstrap_commands:")
	for _, command := range state.BootstrapCommands {
		lines = append(lines, fmt.Sprintf("#     - %s", command))
	}

	lines = append(lines, fmt.Sprintf("#   validation_command: %s", state.ValidationCommand))
	lines = append(lines, fmt.Sprintf("#   launch_command: %s", state.LaunchCommand))

	return strings.Join(lines, "\n") + "\n"
}
