package codelima

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

type ShellStreams struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

type LimaClient interface {
	BaseTemplate(ctx context.Context, project Project, nodeCommands LimaCommandTemplates, locator string) ([]byte, error)
	List(ctx context.Context) ([]RuntimeObservation, error)
	Create(ctx context.Context, project Project, node Node, templatePath string) error
	Start(ctx context.Context, project Project, node Node) error
	Stop(ctx context.Context, project Project, node Node) error
	Delete(ctx context.Context, project Project, node Node) error
	Clone(ctx context.Context, project Project, sourceNode, targetNode Node) error
	CopyToGuest(ctx context.Context, project Project, node Node, sourcePath, targetPath string, recursive bool) error
	Shell(ctx context.Context, project Project, node Node, command []string, workdir string, interactive bool, streams ShellStreams) error
}

type ExecLimaClient struct {
	Binary       string
	LimaCommands LimaCommandTemplates
	Stdout       io.Writer
	Stderr       io.Writer
}

func NewExecLimaClient() *ExecLimaClient {
	return &ExecLimaClient{
		Binary:       "limactl",
		LimaCommands: defaultLimaCommandTemplates(),
	}
}

type limaCommandKind string

const (
	limaCommandTemplateCopy         limaCommandKind = "template_copy"
	limaCommandCreate               limaCommandKind = "create"
	limaCommandStart                limaCommandKind = "start"
	limaCommandStop                 limaCommandKind = "stop"
	limaCommandDelete               limaCommandKind = "delete"
	limaCommandClone                limaCommandKind = "clone"
	limaCommandBootstrap            limaCommandKind = "bootstrap"
	limaCommandWorkspaceSeedPrepare limaCommandKind = "workspace_seed_prepare"
	limaCommandCopy                 limaCommandKind = "copy"
	limaCommandShell                limaCommandKind = "shell"
)

var unresolvedLimaPlaceholderPattern = regexp.MustCompile(`\{\{[^{}]+\}\}`)

func supportedLimaCommandKind(kind limaCommandKind) bool {
	switch kind {
	case limaCommandTemplateCopy,
		limaCommandCreate,
		limaCommandStart,
		limaCommandStop,
		limaCommandDelete,
		limaCommandClone,
		limaCommandBootstrap,
		limaCommandWorkspaceSeedPrepare,
		limaCommandCopy,
		limaCommandShell:
		return true
	default:
		return false
	}
}

func resolveConfiguredLimaCommands(binary string, global LimaCommandTemplates, project Project, nodeCommands LimaCommandTemplates, kind limaCommandKind, values map[string]string) ([]string, error) {
	if !supportedLimaCommandKind(kind) {
		return nil, invalidArgument("unsupported lima command kind", map[string]any{"kind": string(kind)})
	}

	if values == nil {
		values = map[string]string{}
	}

	values = cloneMap(values)
	values["binary"] = shellQuote(binary)

	templates := defaultProjectLimaCommandTemplates(kind)
	globalTemplates := global.templates(kind)
	projectTemplates := project.LimaCommands.templates(kind)
	nodeTemplates := nodeCommands.templates(kind)

	templates = applyDefaultCommandList(globalTemplates, templates)
	templates = applyDefaultCommandList(projectTemplates, templates)
	templates = applyDefaultCommandList(nodeTemplates, templates)

	resolved := make([]string, 0, len(templates))
	for _, template := range templates {
		command := template
		for key, value := range values {
			command = strings.ReplaceAll(command, "{{"+key+"}}", value)
		}

		if unresolved := unresolvedLimaPlaceholderPattern.FindString(command); unresolved != "" {
			return nil, invalidArgument("lima command template contains an unknown placeholder", map[string]any{"placeholder": unresolved, "command": template})
		}

		command = strings.TrimSpace(command)
		if command == "" {
			return nil, invalidArgument("lima command template must not resolve to an empty command", map[string]any{"kind": string(kind)})
		}

		resolved = append(resolved, command)
	}

	return resolved, nil
}

func defaultProjectLimaCommandTemplates(kind limaCommandKind) []string {
	return defaultLimaCommandTemplates().templates(kind)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func shellArgsFragment(args []string) string {
	if len(args) == 0 {
		return ""
	}

	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}

	return strings.Join(quoted, " ")
}

func prefixedShellFragment(flag string, args ...string) string {
	parts := make([]string, 0, 1+len(args))
	if strings.TrimSpace(flag) != "" {
		parts = append(parts, flag)
	}

	for _, arg := range args {
		if strings.TrimSpace(arg) == "" {
			continue
		}
		parts = append(parts, arg)
	}

	if len(parts) == 0 {
		return ""
	}

	return " " + strings.Join(parts, " ")
}

func shellFlagFragment(flag string, enabled bool) string {
	if !enabled {
		return ""
	}

	return " " + flag
}

func shellCommandArgsFragment(args []string) string {
	if len(args) == 0 {
		return ""
	}

	return " -- " + shellArgsFragment(args)
}

func (c *ExecLimaClient) BaseTemplate(ctx context.Context, project Project, nodeCommands LimaCommandTemplates, locator string) ([]byte, error) {
	commands, err := resolveConfiguredLimaCommands(c.Binary, c.LimaCommands, project, nodeCommands, limaCommandTemplateCopy, map[string]string{
		"locator": shellQuote(locator),
	})
	if err != nil {
		return nil, err
	}

	var stdout []byte
	for _, command := range commands {
		commandStdout, stderr, runErr := c.runCommandString(ctx, 15*time.Second, command, c.Stdout, c.Stderr)
		if runErr != nil {
			return nil, externalCommandFailed(
				"limactl template copy failed",
				fmt.Errorf("%w: %s", runErr, strings.TrimSpace(string(stderr))),
				map[string]any{"locator": locator, "command": command},
			)
		}
		stdout = commandStdout
	}

	return stdout, nil
}

func (c *ExecLimaClient) List(ctx context.Context) ([]RuntimeObservation, error) {
	stdout, stderr, err := c.run(ctx, 15*time.Second, "list", "--json")
	if err != nil {
		return nil, dependencyUnavailable(
			"limactl list --json failed",
			fmt.Errorf("%w: %s", err, strings.TrimSpace(string(stderr))),
			nil,
		)
	}

	lines := strings.Split(strings.TrimSpace(string(stdout)), "\n")
	observations := make([]RuntimeObservation, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}

		payload := map[string]any{}
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			return nil, metadataCorruption("failed to parse limactl list output", err, map[string]any{"line": line})
		}

		observation := RuntimeObservation{
			Exists: true,
		}

		if name, ok := payload["name"].(string); ok {
			observation.Name = name
		}

		if status, ok := payload["status"].(string); ok {
			observation.Status = strings.ToLower(status)
		}

		if dir, ok := payload["dir"].(string); ok {
			observation.Dir = dir
		}

		if hostname, ok := payload["hostname"].(string); ok {
			observation.Hostname = hostname
		}

		observations = append(observations, observation)
	}

	return observations, nil
}

func (c *ExecLimaClient) Create(ctx context.Context, project Project, node Node, templatePath string) error {
	commands, err := resolveConfiguredLimaCommands(c.Binary, c.LimaCommands, project, node.LimaCommands, limaCommandCreate, map[string]string{
		"instance_name": shellQuote(node.LimaInstanceName),
		"template_path": shellQuote(templatePath),
	})
	if err != nil {
		return err
	}

	for _, command := range commands {
		_, stderr, runErr := c.runCommandString(ctx, 15*time.Minute, command, c.Stdout, c.Stderr)
		if runErr != nil {
			return externalCommandFailed(
				"limactl create failed",
				fmt.Errorf("%w: %s", runErr, strings.TrimSpace(string(stderr))),
				map[string]any{"instance_name": node.LimaInstanceName, "template_path": templatePath, "command": command},
			)
		}
	}

	return nil
}

func (c *ExecLimaClient) Start(ctx context.Context, project Project, node Node) error {
	commands, err := resolveConfiguredLimaCommands(c.Binary, c.LimaCommands, project, node.LimaCommands, limaCommandStart, map[string]string{
		"instance_name": shellQuote(node.LimaInstanceName),
	})
	if err != nil {
		return err
	}

	for _, command := range commands {
		_, stderr, runErr := c.runCommandString(ctx, 15*time.Minute, command, c.Stdout, c.Stderr)
		if runErr != nil {
			return externalCommandFailed(
				"limactl start failed",
				fmt.Errorf("%w: %s", runErr, strings.TrimSpace(string(stderr))),
				map[string]any{"instance_name": node.LimaInstanceName, "command": command},
			)
		}
	}

	return nil
}

func (c *ExecLimaClient) Stop(ctx context.Context, project Project, node Node) error {
	commands, err := resolveConfiguredLimaCommands(c.Binary, c.LimaCommands, project, node.LimaCommands, limaCommandStop, map[string]string{
		"instance_name": shellQuote(node.LimaInstanceName),
	})
	if err != nil {
		return err
	}

	for _, command := range commands {
		_, stderr, runErr := c.runCommandString(ctx, 10*time.Minute, command, c.Stdout, c.Stderr)
		if runErr != nil {
			return externalCommandFailed(
				"limactl stop failed",
				fmt.Errorf("%w: %s", runErr, strings.TrimSpace(string(stderr))),
				map[string]any{"instance_name": node.LimaInstanceName, "command": command},
			)
		}
	}

	return nil
}

func (c *ExecLimaClient) Delete(ctx context.Context, project Project, node Node) error {
	commands, err := resolveConfiguredLimaCommands(c.Binary, c.LimaCommands, project, node.LimaCommands, limaCommandDelete, map[string]string{
		"instance_name": shellQuote(node.LimaInstanceName),
	})
	if err != nil {
		return err
	}

	for _, command := range commands {
		_, stderr, runErr := c.runCommandString(ctx, 10*time.Minute, command, c.Stdout, c.Stderr)
		if runErr != nil {
			return externalCommandFailed(
				"limactl delete failed",
				fmt.Errorf("%w: %s", runErr, strings.TrimSpace(string(stderr))),
				map[string]any{"instance_name": node.LimaInstanceName, "command": command},
			)
		}
	}

	return nil
}

func (c *ExecLimaClient) Clone(ctx context.Context, project Project, sourceNode, targetNode Node) error {
	commands, err := resolveConfiguredLimaCommands(c.Binary, c.LimaCommands, project, targetNode.LimaCommands, limaCommandClone, map[string]string{
		"source_instance": shellQuote(sourceNode.LimaInstanceName),
		"target_instance": shellQuote(targetNode.LimaInstanceName),
	})
	if err != nil {
		return err
	}

	for _, command := range commands {
		_, stderr, runErr := c.runCommandString(ctx, 20*time.Minute, command, c.Stdout, c.Stderr)
		if runErr != nil {
			return externalCommandFailed(
				"limactl clone failed",
				fmt.Errorf("%w: %s", runErr, strings.TrimSpace(string(stderr))),
				map[string]any{"source_instance": sourceNode.LimaInstanceName, "target_instance": targetNode.LimaInstanceName, "command": command},
			)
		}
	}

	return nil
}

func (c *ExecLimaClient) CopyToGuest(ctx context.Context, project Project, node Node, sourcePath, targetPath string, recursive bool) error {
	commands, err := resolveConfiguredLimaCommands(c.Binary, c.LimaCommands, project, node.LimaCommands, limaCommandCopy, map[string]string{
		"source_path":    shellQuote(sourcePath),
		"target_path":    shellQuote(targetPath),
		"instance_name":  shellQuote(node.LimaInstanceName),
		"copy_target":    shellQuote(fmt.Sprintf("%s:%s", node.LimaInstanceName, targetPath)),
		"recursive_flag": shellFlagFragment("-r", recursive),
	})
	if err != nil {
		return err
	}

	for _, command := range commands {
		_, stderr, runErr := c.runCommandString(ctx, 20*time.Minute, command, c.Stdout, c.Stderr)
		if runErr != nil {
			return externalCommandFailed(
				"limactl copy failed",
				fmt.Errorf("%w: %s", runErr, strings.TrimSpace(string(stderr))),
				map[string]any{"instance_name": node.LimaInstanceName, "source_path": sourcePath, "target_path": targetPath, "command": command},
			)
		}
	}

	return nil
}

func (c *ExecLimaClient) Shell(ctx context.Context, project Project, node Node, command []string, workdir string, interactive bool, streams ShellStreams) error {
	workdirFlag := ""
	if workdir != "" {
		workdirFlag = prefixedShellFragment("--workdir", shellQuote(workdir))
	}

	resolvedCommands, err := resolveConfiguredLimaCommands(c.Binary, c.LimaCommands, project, node.LimaCommands, limaCommandShell, map[string]string{
		"instance_name": shellQuote(node.LimaInstanceName),
		"workdir":       shellQuote(workdir),
		"workdir_flag":  workdirFlag,
		"command_args":  shellCommandArgsFragment(command),
	})
	if err != nil {
		return err
	}

	if interactive {
		for _, preCommand := range resolvedCommands[:len(resolvedCommands)-1] {
			_, stderr, runErr := c.runCommandString(ctx, 10*time.Minute, preCommand, multiWriter(streams.Stdout, c.Stdout), multiWriter(streams.Stderr, c.Stderr))
			if runErr != nil {
				return externalCommandFailed(
					"limactl shell failed",
					fmt.Errorf("%w: %s", runErr, strings.TrimSpace(string(stderr))),
					map[string]any{"instance_name": node.LimaInstanceName, "command": command, "resolved_command": preCommand},
				)
			}
		}

		cmd := exec.CommandContext(ctx, "sh", "-lc", resolvedCommands[len(resolvedCommands)-1])
		cmd.Stdin = streams.Stdin
		cmd.Stdout = streams.Stdout
		cmd.Stderr = streams.Stderr
		if err := cmd.Run(); err != nil {
			return externalCommandFailed(
				"limactl shell failed",
				err,
				map[string]any{"instance_name": node.LimaInstanceName, "command": command, "resolved_command": resolvedCommands[len(resolvedCommands)-1]},
			)
		}
		return nil
	}

	for _, resolvedCommand := range resolvedCommands {
		_, stderr, runErr := c.runCommandString(ctx, 10*time.Minute, resolvedCommand, multiWriter(streams.Stdout, c.Stdout), multiWriter(streams.Stderr, c.Stderr))
		if runErr != nil {
			return externalCommandFailed(
				"limactl shell failed",
				fmt.Errorf("%w: %s", runErr, strings.TrimSpace(string(stderr))),
				map[string]any{"instance_name": node.LimaInstanceName, "command": command, "resolved_command": resolvedCommand},
			)
		}
	}

	return nil
}

func (c *ExecLimaClient) run(ctx context.Context, timeout time.Duration, args ...string) ([]byte, []byte, error) {
	return c.runWithOutputs(ctx, timeout, c.Stdout, c.Stderr, args...)
}

func (c *ExecLimaClient) runCommandString(ctx context.Context, timeout time.Duration, command string, stdoutWriter, stderrWriter io.Writer) ([]byte, []byte, error) {
	runCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	cmd := exec.CommandContext(runCtx, "sh", "-lc", command)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = multiWriter(&stdout, stdoutWriter)
	cmd.Stderr = multiWriter(&stderr, stderrWriter)
	err := cmd.Run()

	return stdout.Bytes(), stderr.Bytes(), err
}

func (c *ExecLimaClient) runWithOutputs(ctx context.Context, timeout time.Duration, stdoutWriter io.Writer, stderrWriter io.Writer, args ...string) ([]byte, []byte, error) {
	runCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	cmd := exec.CommandContext(runCtx, c.Binary, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = multiWriter(&stdout, stdoutWriter)
	cmd.Stderr = multiWriter(&stderr, stderrWriter)
	err := cmd.Run()

	return stdout.Bytes(), stderr.Bytes(), err
}

func multiWriter(writers ...io.Writer) io.Writer {
	filtered := make([]io.Writer, 0, len(writers))
	for _, writer := range writers {
		if writer == nil {
			continue
		}
		filtered = append(filtered, writer)
	}

	switch len(filtered) {
	case 0:
		return nil
	case 1:
		return filtered[0]
	default:
		return io.MultiWriter(filtered...)
	}
}
