package codelima

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

type ShellStreams struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

type CloneOptions struct {
	MountPath string
	Resources Resources
}

type LimaClient interface {
	BaseTemplate(ctx context.Context, locator string) ([]byte, error)
	List(ctx context.Context) ([]RuntimeObservation, error)
	Create(ctx context.Context, instanceName, templatePath string) error
	Start(ctx context.Context, instanceName string) error
	Stop(ctx context.Context, instanceName string) error
	Delete(ctx context.Context, instanceName string) error
	Clone(ctx context.Context, sourceInstance, targetInstance string, options CloneOptions) error
	Shell(ctx context.Context, instanceName string, command []string, workdir string, interactive bool, streams ShellStreams) error
}

type ExecLimaClient struct {
	Binary string
}

func NewExecLimaClient() *ExecLimaClient {
	return &ExecLimaClient{Binary: "limactl"}
}

func (c *ExecLimaClient) BaseTemplate(ctx context.Context, locator string) ([]byte, error) {
	stdout, stderr, err := c.run(ctx, 15*time.Second, "template", "copy", "--fill", locator, "-")
	if err != nil {
		return nil, externalCommandFailed(
			"limactl template copy failed",
			fmt.Errorf("%w: %s", err, strings.TrimSpace(string(stderr))),
			map[string]any{"locator": locator},
		)
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

func (c *ExecLimaClient) Create(ctx context.Context, instanceName, templatePath string) error {
	_, stderr, err := c.run(ctx, 15*time.Minute, "create", "-y", "--name", instanceName, templatePath)
	if err != nil {
		return externalCommandFailed(
			"limactl create failed",
			fmt.Errorf("%w: %s", err, strings.TrimSpace(string(stderr))),
			map[string]any{"instance_name": instanceName, "template_path": templatePath},
		)
	}

	return nil
}

func (c *ExecLimaClient) Start(ctx context.Context, instanceName string) error {
	_, stderr, err := c.run(ctx, 15*time.Minute, "start", "-y", instanceName)
	if err != nil {
		return externalCommandFailed(
			"limactl start failed",
			fmt.Errorf("%w: %s", err, strings.TrimSpace(string(stderr))),
			map[string]any{"instance_name": instanceName},
		)
	}

	return nil
}

func (c *ExecLimaClient) Stop(ctx context.Context, instanceName string) error {
	_, stderr, err := c.run(ctx, 10*time.Minute, "stop", "-y", instanceName)
	if err != nil {
		return externalCommandFailed(
			"limactl stop failed",
			fmt.Errorf("%w: %s", err, strings.TrimSpace(string(stderr))),
			map[string]any{"instance_name": instanceName},
		)
	}

	return nil
}

func (c *ExecLimaClient) Delete(ctx context.Context, instanceName string) error {
	_, stderr, err := c.run(ctx, 10*time.Minute, "delete", "-f", instanceName)
	if err != nil {
		return externalCommandFailed(
			"limactl delete failed",
			fmt.Errorf("%w: %s", err, strings.TrimSpace(string(stderr))),
			map[string]any{"instance_name": instanceName},
		)
	}

	return nil
}

func (c *ExecLimaClient) Clone(ctx context.Context, sourceInstance, targetInstance string, options CloneOptions) error {
	args := []string{"clone", "-y", sourceInstance, targetInstance}
	if options.Resources.CPUs > 0 {
		args = append(args, "--cpus", fmt.Sprintf("%d", options.Resources.CPUs))
	}

	if options.Resources.MemoryGiB > 0 {
		args = append(args, "--memory", fmt.Sprintf("%d", options.Resources.MemoryGiB))
	}

	if options.Resources.DiskGiB > 0 {
		args = append(args, "--disk", fmt.Sprintf("%d", options.Resources.DiskGiB))
	}

	if options.MountPath != "" {
		args = append(args, "--mount-only", options.MountPath+":w")
	}

	_, stderr, err := c.run(ctx, 20*time.Minute, args...)
	if err != nil {
		return externalCommandFailed(
			"limactl clone failed",
			fmt.Errorf("%w: %s", err, strings.TrimSpace(string(stderr))),
			map[string]any{"source_instance": sourceInstance, "target_instance": targetInstance},
		)
	}

	return nil
}

func (c *ExecLimaClient) Shell(ctx context.Context, instanceName string, command []string, workdir string, interactive bool, streams ShellStreams) error {
	args := []string{"shell"}
	if workdir != "" {
		args = append(args, "--workdir", workdir)
	}
	args = append(args, instanceName)
	if len(command) > 0 {
		args = append(args, "--")
		args = append(args, command...)
	}

	if interactive {
		cmd := exec.CommandContext(ctx, c.Binary, args...)
		cmd.Stdin = streams.Stdin
		cmd.Stdout = streams.Stdout
		cmd.Stderr = streams.Stderr
		if err := cmd.Run(); err != nil {
			return externalCommandFailed(
				"limactl shell failed",
				err,
				map[string]any{"instance_name": instanceName, "command": command},
			)
		}

		return nil
	}

	stdout, stderr, err := c.run(ctx, 10*time.Minute, args...)
	if streams.Stdout != nil {
		_, _ = streams.Stdout.Write(stdout)
	}

	if streams.Stderr != nil && len(stderr) > 0 {
		_, _ = streams.Stderr.Write(stderr)
	}

	if err != nil {
		return externalCommandFailed(
			"limactl shell failed",
			fmt.Errorf("%w: %s", err, strings.TrimSpace(string(stderr))),
			map[string]any{"instance_name": instanceName, "command": command},
		)
	}

	return nil
}

func (c *ExecLimaClient) run(ctx context.Context, timeout time.Duration, args ...string) ([]byte, []byte, error) {
	runCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	cmd := exec.CommandContext(runCtx, c.Binary, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	return stdout.Bytes(), stderr.Bytes(), err
}
