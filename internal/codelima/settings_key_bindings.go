package codelima

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// UnmarshalYAML separates invalid frontend key preferences from configuration
// errors that would prevent daemon or lifecycle commands from starting. The
// authored file is retained for inspection and recovery; only the TUI compiler
// chooses an effective fallback. Other fields keep their normal YAML decoding.
func (c *Config) UnmarshalYAML(node *yaml.Node) error {
	type plainConfig Config
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("settings.yaml must be a mapping")
	}
	copyNode := *node
	copyNode.Content = slices.Clone(node.Content)
	var keys *yaml.Node
	for i := 0; i+1 < len(copyNode.Content); i += 2 {
		if copyNode.Content[i].Value == "keys" {
			keys = copyNode.Content[i+1]
			copyNode.Content[i+1] = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		}
	}
	value := plainConfig(*c)
	value.Keys, value.keyBindingDecodeError = nil, ""
	if err := copyNode.Decode(&value); err != nil {
		return err
	}
	if keys != nil {
		var err error
		value.Keys, err = decodeKeyBindingOverrides(keys)
		if err != nil {
			value.keyBindingDecodeError = err.Error()
		}
	}
	*c = Config(value)
	return nil
}

func decodeKeyBindingOverrides(node *yaml.Node) (KeyBindingConfig, error) {
	if node.Kind != yaml.MappingNode {
		return nil, invalidArgument("keys must be a mapping of action IDs to key lists", nil)
	}
	config := make(KeyBindingConfig, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return nil, invalidArgument("keys action IDs must be strings", nil)
		}
		if _, duplicate := config[key.Value]; duplicate {
			return nil, invalidArgument("keys."+key.Value+": duplicate action", nil)
		}
		bindings, err := decodeKeyBindingList(value)
		if err != nil {
			return nil, invalidArgument("keys."+key.Value+": "+err.Error(), nil)
		}
		config[key.Value] = bindings
	}
	return config, nil
}

func decodeKeyBindingList(node *yaml.Node) ([]string, error) {
	if node.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("expected a YAML list of key strings, for example [ctrl+d]")
	}
	values := make([]string, 0, len(node.Content))
	for _, value := range node.Content {
		if value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
			return nil, fmt.Errorf("each binding must be a string; quote numeric keys")
		}
		values = append(values, value.Value)
	}
	return values, nil
}

func parseKeyBindingSetting(key, value string) ([]string, error) {
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(value), &document); err != nil {
		return nil, invalidArgument(key+": "+err.Error(), nil)
	}
	if len(document.Content) != 1 {
		return nil, invalidArgument(key+": expected a YAML list of keys", nil)
	}
	bindings, err := decodeKeyBindingList(document.Content[0])
	if err != nil {
		return nil, invalidArgument(key+": "+err.Error(), nil)
	}
	return bindings, nil
}

func compileConfiguredKeyBindings(cfg Config) (*compiledKeyMap, error) {
	if cfg.keyBindingDecodeError != "" {
		return nil, invalidArgument(cfg.keyBindingDecodeError, nil)
	}
	return compileKeyBindings(cfg.Keys, tuiKeyActionDefinitions())
}

// KeyBindingInspection reports what a fresh frontend would use. An existing
// frontend retains its last valid snapshot when a later edit is invalid.
type KeyBindingInspection struct {
	Bindings        []KeyBindingInfo `json:"bindings" yaml:"bindings"`
	ValidationError string           `json:"validation_error,omitempty" yaml:"validation_error,omitempty"`
}

func (s *Service) SettingsKeys() (KeyBindingInspection, error) {
	cfg, err := s.SettingsRead()
	if err != nil {
		return KeyBindingInspection{}, err
	}
	m, err := compileConfiguredKeyBindings(cfg)
	result := KeyBindingInspection{}
	if err != nil {
		result.ValidationError = err.Error()
		m = defaultTUIKeyMap()
	}
	result.Bindings = m.Describe()
	return result, nil
}

// SettingsResetKeys deliberately permits removing unknown or invalid overrides.
// It still refuses malformed YAML and never rewrites unrelated settings.
func (s *Service) SettingsResetKeys(ctx context.Context, action string) (Config, error) {
	return s.editSettings(ctx, false, func(root *yaml.Node) error {
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value != "keys" {
				continue
			}
			if action == "" {
				root.Content = slices.Delete(root.Content, i, i+2)
				return nil
			}
			mapping := root.Content[i+1]
			if mapping.Kind != yaml.MappingNode {
				return invalidArgument("keys must be a mapping; use settings reset keys to remove invalid overrides", nil)
			}
			for j := 0; j+1 < len(mapping.Content); j += 2 {
				if mapping.Content[j].Value == action {
					mapping.Content = slices.Delete(mapping.Content, j, j+2)
					return nil
				}
			}
		}
		return nil
	})
}

func managerSettingPath(key string) []string {
	if action, ok := strings.CutPrefix(key, "keys."); ok {
		return []string{"keys", action}
	}
	return strings.Split(key, ".")
}
