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

func TestSDKSandboxClientSSHHelperTransport(t *testing.T) {
	directory := t.TempDir()
	capture := filepath.Join(directory, "calls")
	helper := filepath.Join(directory, "codelima-test")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$CODELIMA_TEST_CAPTURE\"\nexec cat\n"
	if err := os.WriteFile(helper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	publicKey := filepath.Join(directory, "id.pub")
	if err := os.WriteFile(publicKey, []byte("ssh-ed25519 test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODELIMA_TEST_CAPTURE", capture)
	client := &SDKSandboxClient{executable: helper}
	if err := client.AuthorizeSSHKey(context.Background(), publicKey); err != nil {
		t.Fatalf("AuthorizeSSHKey() error = %v", err)
	}
	transport, err := client.OpenSSHTransport(context.Background(), "test-node")
	if err != nil {
		t.Fatalf("OpenSSHTransport() error = %v", err)
	}
	message := []byte("transport-data")
	if _, err := transport.Write(message); err != nil {
		t.Fatalf("transport.Write() error = %v", err)
	}
	got := make([]byte, len(message))
	if _, err := io.ReadFull(transport, got); err != nil {
		t.Fatalf("transport.Read() error = %v", err)
	}
	if string(got) != string(message) {
		t.Fatalf("transport echoed %q", got)
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("transport.Close() error = %v", err)
	}
	data, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	want := sdkSSHServeCommand + " --sandbox test-node --authorized-keys " + publicKey
	if got := strings.TrimSpace(string(data)); got != want {
		t.Fatalf("helper invocation = %q, want %q", got, want)
	}
}

func TestSDKSandboxClientSSHTransportRequiresPreparedKey(t *testing.T) {
	client := &SDKSandboxClient{executable: filepath.Join(t.TempDir(), "unused")}
	_, err := client.OpenSSHTransport(context.Background(), "test-node")
	if err == nil || !strings.Contains(err.Error(), "has not been prepared") {
		t.Fatalf("OpenSSHTransport() error = %v", err)
	}
}

func TestSDKSSHServeHiddenCommandValidatesWithoutWritingStdout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := Run(context.Background(), []string{sdkSSHServeCommand, "--sandbox", "test-node"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != ExitInvalidArgument {
		t.Fatalf("Run() exit = %d, want %d", exitCode, ExitInvalidArgument)
	}
	if stdout.Len() != 0 {
		t.Fatalf("Run() stdout = %q, want empty SSH stream", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--authorized-keys is required") {
		t.Fatalf("Run() stderr = %q", stderr.String())
	}
}
