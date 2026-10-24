package codelima

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type ShellSettings struct {
	Mode      string `json:"mode" yaml:"mode"`
	Window    string `json:"window" yaml:"window"`
	DetachKey string `json:"detach_key" yaml:"detach_key"`
}

// SettingsRead overlays the latest file onto this service's defaults without
// mutating shared configuration. Long-lived daemons observe new-node defaults
// at creation, while each frontend owns its presentation preferences.
func (s *Service) SettingsRead() (Config, error) {
	cfg := s.cfg
	if err := readYAMLFile(s.store.configPath(), &cfg); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, metadataCorruption("failed to load settings.yaml", err, nil)
	}
	cfg.MetadataRoot = s.cfg.MetadataRoot
	return cfg, nil
}

// SettingsUpdate edits one supported leaf under a cross-process lock. It
// preserves comments, ordering and unknown keys at every mapping level and
// refuses malformed documents instead of replacing them with defaults.
func (s *Service) SettingsUpdate(ctx context.Context, key, value string) (Config, error) {
	parsed, err := parseManagerSetting(key, value)
	if err != nil {
		return Config{}, err
	}
	// Preferences do not need template seeding. In particular, a seed repair
	// must not replace a malformed authored file before we can reject it.
	if err := s.ensureDirectories(); err != nil {
		return Config{}, err
	}
	var result Config
	err = s.withLocks(ctx, []lockKey{lockSettings}, nil, func() error {
		path := s.store.configPath()
		data, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		if len(strings.TrimSpace(string(data))) > 0 {
			var document yaml.Node
			if err := yaml.Unmarshal(data, &document); err != nil {
				return metadataCorruption("failed to load settings.yaml", err, nil)
			}
			if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
				return invalidArgument("settings.yaml must be a mapping", nil)
			}
			root = document.Content[0]
		}
		if err := patchSetting(root, strings.Split(key, "."), parsed); err != nil {
			return err
		}
		result = s.cfg
		if err := root.Decode(&result); err != nil {
			return metadataCorruption("invalid settings.yaml", err, nil)
		}
		result.MetadataRoot = s.cfg.MetadataRoot
		encoded, err := yamlBytes(root)
		if err != nil {
			return err
		}
		return atomicWriteFile(path, encoded, 0o600)
	})
	return result, err
}

func parseManagerSetting(key, value string) (any, error) {
	var allowed []string
	switch key {
	case "shell.mode":
		allowed = []string{"attach", "window", "tmux", "embedded"}
	case "color":
		allowed = []string{"auto", "ansi", "truecolor"}
	case "confirm_stop":
		allowed = []string{"busy", "always", "never"}
	case "import_host_auth", "group_by_directory":
		value, err := strconv.ParseBool(value)
		if err != nil {
			return nil, invalidArgument(key+" must be true or false", nil)
		}
		return value, nil
	case "shell.window":
		if _, err := shellWindowArgs(value, []string{"codelima", "shell", "node"}); err != nil {
			return nil, err
		}
		return value, nil
	default:
		return nil, invalidArgument("setting is read-only or unknown: "+key, nil)
	}
	if !slices.Contains(allowed, value) {
		return nil, invalidArgument(fmt.Sprintf("%s must be one of %s", key, strings.Join(allowed, ", ")), nil)
	}
	return value, nil
}

func patchSetting(mapping *yaml.Node, keys []string, value any) error {
	if mapping.Kind != yaml.MappingNode {
		return invalidArgument("setting parent must be a mapping", nil)
	}
	var child *yaml.Node
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == keys[0] {
			child = mapping.Content[i+1]
			break
		}
	}
	if child == nil {
		child = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMappingValue(mapping, keys[0], child)
	}
	if len(keys) > 1 {
		return patchSetting(child, keys[1:], value)
	}
	var replacement yaml.Node
	if err := replacement.Encode(value); err != nil {
		return err
	}
	replacement.HeadComment, replacement.LineComment, replacement.FootComment = child.HeadComment, child.LineComment, child.FootComment
	if child.Kind == yaml.ScalarNode {
		replacement.Style = child.Style
	}
	*child = replacement
	return nil
}
