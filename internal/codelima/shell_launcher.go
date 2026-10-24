//go:build darwin || linux

package codelima

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// The placeholder is an argv splice, not shell source. This keeps metadata
// paths, session identities and executable paths literal even with spaces or
// shell metacharacters. Custom shell logic belongs in an executable wrapper.
func shellWindowArgs(template string, command []string) ([]string, error) {
	const marker = "CODELIMA_COMMAND_ARGUMENTS"
	if strings.Count(template, "{cmd}") != 1 || strings.Contains(template, marker) {
		return nil, invalidArgument("shell.window must contain exactly one {cmd} argument", nil)
	}
	args, ok := argvFromQuotedCommand(strings.Replace(template, "{cmd}", marker, 1))
	if !ok {
		return nil, invalidArgument("shell.window must be a quoted argument list; use a wrapper for shell scripts", nil)
	}
	var result []string
	for i, arg := range args {
		if strings.Contains(arg, marker) {
			if i == 0 || arg != marker {
				return nil, invalidArgument("{cmd} must be a separate argument after the window executable", nil)
			}
			result = append(result, command...)
		} else {
			result = append(result, arg)
		}
	}
	return result, nil
}

func shellLaunchArgs(cfg Config, mode, executable, terminalID, directory, label string) ([]string, error) {
	command := []string{"env", "CODELIMA_HOME=" + cfg.MetadataRoot, executable, "terminal", "attach", terminalID}
	switch mode {
	case "window":
		return shellWindowArgs(cfg.Shell.Window, command)
	case "tmux":
		return append([]string{"tmux", "new-window", "-c", directory, "-n", label, "--"}, command...), nil
	default:
		return nil, fmt.Errorf("unsupported shell launch mode %q", mode)
	}
}

func checkShellLaunch(cfg Config, mode string) error {
	if mode == "attach" {
		return nil
	}
	if mode == "tmux" && os.Getenv("TMUX") == "" {
		return errors.New("tmux mode requires a running tmux session ($TMUX)")
	}
	args, err := shellLaunchArgs(cfg, mode, "codelima", "session", ".", "node")
	if err != nil {
		return err
	}
	if _, err := exec.LookPath(args[0]); err != nil {
		return fmt.Errorf("shell launcher %q: %w; configure shell.window in System", args[0], err)
	}
	return nil
}

func launchManagedShell(ctx context.Context, cfg Config, mode, terminalID, directory, label string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	args, err := shellLaunchArgs(cfg, mode, executable, terminalID, directory, label)
	if err != nil {
		return err
	}
	if mode == "tmux" {
		output, err := exec.CommandContext(ctx, args[0], args[1:]...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("open tmux window: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return nil
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = directory
	if err := cmd.Start(); err != nil {
		return err
	}
	// The window outlives this TUI. Reap its launcher without coupling either
	// process lifetime to the frontend's context.
	go func() { _ = cmd.Wait() }()
	return nil
}
