package codelima

import (
	"bytes"
	"encoding/json"

	"gopkg.in/yaml.v3"
)

type commandList []string

func copyCommandList(commands []string) []string {
	if commands == nil {
		return nil
	}

	return append([]string(nil), commands...)
}

func applyDefaultCommandList(commands, defaults []string) []string {
	if len(commands) > 0 {
		return copyCommandList(commands)
	}

	return copyCommandList(defaults)
}

func (c *commandList) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*c = nil
		return nil
	}

	if trimmed[0] == '[' {
		var commands []string
		if err := json.Unmarshal(trimmed, &commands); err != nil {
			return err
		}
		*c = commandList(commands)
		return nil
	}

	var command string
	if err := json.Unmarshal(trimmed, &command); err != nil {
		return err
	}
	*c = commandList{command}
	return nil
}

func (c commandList) MarshalJSON() ([]byte, error) {
	return json.Marshal([]string(c))
}

func (c *commandList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case 0:
		*c = nil
		return nil
	case yaml.ScalarNode:
		var command string
		if err := node.Decode(&command); err != nil {
			return err
		}
		*c = commandList{command}
		return nil
	case yaml.SequenceNode:
		var commands []string
		if err := node.Decode(&commands); err != nil {
			return err
		}
		*c = commandList(commands)
		return nil
	default:
		var commands []string
		if err := node.Decode(&commands); err != nil {
			return err
		}
		*c = commandList(commands)
		return nil
	}
}

func (c commandList) MarshalYAML() (any, error) {
	return []string(c), nil
}
