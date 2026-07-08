package codelima

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mirrorWriter forwards writes to an underlying sink. Two distinct
// *mirrorWriter values that share a sink are equivalent (they land in the
// same place) but are NOT pointer-identical, which is exactly the case the
// old sameWriter pointer/type de-dup failed to collapse.
type mirrorWriter struct {
	sink io.Writer
}

func (w *mirrorWriter) Write(p []byte) (int, error) {
	return w.sink.Write(p)
}

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

func TestExecLimaClientShellDoesNotDuplicateOutputWhenStreamsAndClientWriteToSameSink(t *testing.T) {
	t.Parallel()

	// Mirror Service.Shell's real wiring: streams.Stdout and the client's
	// Stdout both ultimately land on the same process stdout, but they are
	// not the same pointer. This is the scenario that doubled `codelima
	// shell <node> -- <cmd>` output (TODO #6).
	sink := &bytes.Buffer{}
	client := &ExecLimaClient{
		Binary: "unused-binary",
		LimaCommands: LimaCommandTemplates{
			Shell: []string{
				"printf 'PRECOMMAND_LINE\\n'",
				"printf 'MAIN_LINE\\n'",
			},
		},
		Stdout: &mirrorWriter{sink: sink},
		Stderr: &mirrorWriter{sink: sink},
	}

	err := client.Shell(
		context.Background(),
		Project{},
		Node{LimaInstanceName: "demo-node"},
		[]string{"pwd"},
		"/workspace",
		false,
		ShellStreams{
			Stdout: &mirrorWriter{sink: sink},
			Stderr: &mirrorWriter{sink: sink},
		},
	)
	if err != nil {
		t.Fatalf("Shell() error = %v", err)
	}

	if got := strings.Count(sink.String(), "PRECOMMAND_LINE"); got != 1 {
		t.Fatalf("expected pre-command line exactly once, got %d in %q", got, sink.String())
	}
	if got := strings.Count(sink.String(), "MAIN_LINE"); got != 1 {
		t.Fatalf("expected main command line exactly once, got %d in %q", got, sink.String())
	}
}

func TestExecLimaClientListDoesNotStreamProbeOutput(t *testing.T) {
	t.Parallel()

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "limactl")
	script := "#!/usr/bin/env sh\n" +
		"printf '{\"name\":\"demo-node\",\"status\":\"Running\",\"dir\":\"/fake/demo-node\"}\\n'\n" +
		"printf 'unexpected-stderr\\n' >&2\n"
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

	observations, err := client.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if stdout.Len() != 0 {
		t.Fatalf("expected List() to avoid streaming stdout, got %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected List() to avoid streaming stderr, got %q", stderr.String())
	}
	if len(observations) != 1 {
		t.Fatalf("expected one observation, got %#v", observations)
	}
	if got := observations[0].Status; got != "running" {
		t.Fatalf("expected normalized running status, got %q", got)
	}
}
