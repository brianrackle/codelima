package codelima

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

func editRecipeCommand(root, editor, command string, run func([]string) error) (result string, err error) {
	args, ok := argvFromQuotedCommand(coalesce(strings.TrimSpace(editor), "vi"))
	if !ok || len(args) == 0 {
		return "", fmt.Errorf("EDITOR must be an executable and quoted arguments; use a wrapper for shell scripts")
	}
	base := filepath.Join(root, "_tmp")
	if err := os.MkdirAll(base, 0700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(base, "edit-command-")
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(dir)) }()
	path := filepath.Join(dir, "command.sh")
	if err := os.WriteFile(path, []byte(command), 0600); err != nil {
		return "", err
	}
	if err := run(append(args, path)); err != nil {
		return "", err
	}
	data, _, clipped, err := readNodeLogTail(path, nodeSpecYAMLLimit)
	if err != nil {
		return "", err
	}
	if clipped {
		return "", fmt.Errorf("edited command exceeds 2 MiB")
	}
	return string(data), nil
}
func (a *vaxisTUIApp) editEnvironmentCommandExternally(env EnvironmentConfig, index int) error {
	if index < 0 || index >= len(env.BootstrapCommands) {
		return fmt.Errorf("select a command to edit")
	}
	if a.service == nil {
		return fmt.Errorf("environment service unavailable")
	}
	command, err := editRecipeCommand(a.service.cfg.MetadataRoot, os.Getenv("EDITOR"), env.BootstrapCommands[index], a.runInteractiveProcess)
	if err != nil {
		return err
	}
	commands := slices.Clone(env.BootstrapCommands)
	commands[index] = command
	e := a.openEnvironmentEditor(env, false)
	e.setCommands(commands)
	return nil
}
