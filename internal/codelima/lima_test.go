package codelima

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecLimaClientCreateStreamsConfiguredOutput(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(t.TempDir(), "fake-limactl")
	script := "#!/usr/bin/env sh\n" +
		"printf 'stdout:%s\\n' \"$*\"\n" +
		"printf 'stderr:%s\\n' \"$*\" >&2\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake limactl) error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	client := &ExecLimaClient{
		Binary: scriptPath,
		Stdout: &stdout,
		Stderr: &stderr,
	}

	if err := client.Create(context.Background(), Project{}, Node{LimaInstanceName: "demo-node"}, "/tmp/template.yaml"); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if !strings.Contains(stdout.String(), "stdout:create -y --name demo-node --cpus=2 --memory=4 --disk=20 /tmp/template.yaml") {
		t.Fatalf("expected stdout stream, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "stderr:create -y --name demo-node --cpus=2 --memory=4 --disk=20 /tmp/template.yaml") {
		t.Fatalf("expected stderr stream, got %q", stderr.String())
	}
}

func TestExecLimaClientStartUsesProjectScopedCommandTemplate(t *testing.T) {
	t.Parallel()

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "limactl")
	script := "#!/usr/bin/env sh\n" +
		"printf 'stdout:%s\\n' \"$*\"\n" +
		"printf 'stderr:%s\\n' \"$*\" >&2\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake limactl) error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	client := &ExecLimaClient{
		Binary: scriptPath,
		LimaCommands: LimaCommandTemplates{
			Start: []string{"{{binary}} start {{instance_name}} --vm-type=vz"},
		},
		Stdout: &stdout,
		Stderr: &stderr,
	}

	project := Project{
		LimaCommands: LimaCommandTemplates{
			Start: []string{"{{binary}} start {{instance_name}} --set '.nestedVirtualization=true'"},
		},
	}

	if err := client.Start(context.Background(), project, Node{LimaInstanceName: "demo-node"}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if !strings.Contains(stdout.String(), "stdout:start demo-node --set .nestedVirtualization=true") {
		t.Fatalf("expected stdout stream to include custom start command, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "stderr:start demo-node --set .nestedVirtualization=true") {
		t.Fatalf("expected stderr stream to include custom start command, got %q", stderr.String())
	}
}

func TestExecLimaClientStartUsesGlobalCommandTemplateWhenProjectOverrideMissing(t *testing.T) {
	t.Parallel()

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "limactl")
	script := "#!/usr/bin/env sh\n" +
		"printf 'stdout:%s\\n' \"$*\"\n" +
		"printf 'stderr:%s\\n' \"$*\" >&2\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake limactl) error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	client := &ExecLimaClient{
		Binary: scriptPath,
		LimaCommands: LimaCommandTemplates{
			Start: []string{"{{binary}} start {{instance_name}} --vm-type=vz"},
		},
		Stdout: &stdout,
		Stderr: &stderr,
	}

	if err := client.Start(context.Background(), Project{}, Node{LimaInstanceName: "demo-node"}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if !strings.Contains(stdout.String(), "stdout:start demo-node --vm-type=vz") {
		t.Fatalf("expected stdout stream to include global start command, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "stderr:start demo-node --vm-type=vz") {
		t.Fatalf("expected stderr stream to include global start command, got %q", stderr.String())
	}
}

func TestExecLimaClientStartUsesNodeScopedCommandTemplate(t *testing.T) {
	t.Parallel()

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "limactl")
	script := "#!/usr/bin/env sh\n" +
		"printf 'stdout:%s\\n' \"$*\"\n" +
		"printf 'stderr:%s\\n' \"$*\" >&2\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake limactl) error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	client := &ExecLimaClient{
		Binary: scriptPath,
		LimaCommands: LimaCommandTemplates{
			Start: []string{"{{binary}} start {{instance_name}} --vm-type=vz"},
		},
		Stdout: &stdout,
		Stderr: &stderr,
	}

	project := Project{
		LimaCommands: LimaCommandTemplates{
			Start: []string{"{{binary}} start {{instance_name}} --set '.nestedVirtualization=true'"},
		},
	}
	node := Node{
		LimaInstanceName: "demo-node",
		LimaCommands: LimaCommandTemplates{
			Start: []string{"{{binary}} start {{instance_name}} --tty=false"},
		},
	}

	if err := client.Start(context.Background(), project, node); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if !strings.Contains(stdout.String(), "stdout:start demo-node --tty=false") {
		t.Fatalf("expected stdout stream to include node-specific start command, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "stderr:start demo-node --tty=false") {
		t.Fatalf("expected stderr stream to include node-specific start command, got %q", stderr.String())
	}
}

func TestExecLimaClientStartRunsMultipleConfiguredCommands(t *testing.T) {
	t.Parallel()

	scriptDir := t.TempDir()
	logPath := filepath.Join(scriptDir, "commands.log")
	scriptPath := filepath.Join(scriptDir, "limactl")
	script := "#!/usr/bin/env sh\n" +
		"printf '%s\\n' \"$*\" >>" + shellQuote(logPath) + "\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake limactl) error = %v", err)
	}

	client := &ExecLimaClient{
		Binary: scriptPath,
		LimaCommands: LimaCommandTemplates{
			Start: []string{
				"{{binary}} start {{instance_name}} --vm-type=vz",
				"{{binary}} start {{instance_name}} --tty=false",
			},
		},
	}

	if err := client.Start(context.Background(), Project{}, Node{LimaInstanceName: "demo-node"}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(commands log) error = %v", err)
	}

	if got := strings.TrimSpace(string(logData)); got != "start demo-node --vm-type=vz\nstart demo-node --tty=false" {
		t.Fatalf("expected both commands to run in order, got %q", got)
	}
}

func TestExecLimaClientShellDoesNotDuplicateOutputWhenStreamsReuseClientWriter(t *testing.T) {
	t.Parallel()

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "limactl")
	script := "#!/usr/bin/env sh\n" +
		"printf 'workspace-path\\n'\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake limactl) error = %v", err)
	}

	var stdout bytes.Buffer
	client := &ExecLimaClient{
		Binary: scriptPath,
		Stdout: &stdout,
	}

	if err := client.Shell(
		context.Background(),
		Project{},
		Node{LimaInstanceName: "demo-node"},
		[]string{"pwd"},
		"/workspace",
		false,
		ShellStreams{Stdout: &stdout},
	); err != nil {
		t.Fatalf("Shell() error = %v", err)
	}

	if got := strings.TrimSpace(stdout.String()); got != "workspace-path" {
		t.Fatalf("expected one shell output line, got %q", got)
	}
}

func TestExecLimaClientCopyFromGuestStreamsConfiguredOutput(t *testing.T) {
	t.Parallel()

	scriptPath := filepath.Join(t.TempDir(), "fake-limactl")
	script := "#!/usr/bin/env sh\n" +
		"printf 'stdout:%s\\n' \"$*\"\n" +
		"printf 'stderr:%s\\n' \"$*\" >&2\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake limactl) error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	client := &ExecLimaClient{
		Binary: scriptPath,
		Stdout: &stdout,
		Stderr: &stderr,
	}

	if err := client.CopyFromGuest(context.Background(), Project{}, Node{LimaInstanceName: "demo-node"}, "/workspace/demo", "/tmp/export", true); err != nil {
		t.Fatalf("CopyFromGuest() error = %v", err)
	}

	if !strings.Contains(stdout.String(), "stdout:copy -r demo-node:/workspace/demo/. /tmp/export") {
		t.Fatalf("expected stdout stream, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "stderr:copy -r demo-node:/workspace/demo/. /tmp/export") {
		t.Fatalf("expected stderr stream, got %q", stderr.String())
	}
}
