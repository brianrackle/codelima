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

	if err := client.Create(context.Background(), "demo-node", "/tmp/template.yaml"); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if !strings.Contains(stdout.String(), "stdout:create -y --name demo-node /tmp/template.yaml") {
		t.Fatalf("expected stdout stream, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "stderr:create -y --name demo-node /tmp/template.yaml") {
		t.Fatalf("expected stderr stream, got %q", stderr.String())
	}
}
