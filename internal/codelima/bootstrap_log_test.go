package codelima

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type bootstrapOutputSandbox struct {
	SandboxClient
	err error
}

func (b bootstrapOutputSandbox) Shell(_ context.Context, _ Node, _ guestIdentity, _ []string, _ string, _ bool, streams ShellStreams) error {
	if streams.Stdout != nil {
		_, _ = io.WriteString(streams.Stdout, "captured stdout\n")
	}
	if streams.Stderr != nil {
		_, _ = io.WriteString(streams.Stderr, "captured stderr\n")
	}
	return b.err
}

func TestBootstrapLogCapturesBothStreamsAndPreservesFailure(t *testing.T) {
	service, _ := newTestService(t)
	node := saveForwardingTestNode(t, service, "bootstrap-log")
	failure := errors.New("step failed")
	service.sandbox = bootstrapOutputSandbox{SandboxClient: service.sandbox, err: failure}
	if err := service.runLoggedGuestCommand(context.Background(), node, "do-work", "bootstrap"); !errors.Is(err, failure) {
		t.Fatalf("lost command failure: %v", err)
	}
	path := filepath.Join(service.store.nodeDir(node.ID), "bootstrap.log")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"$ do-work", "captured stdout", "captured stderr", "failed: step failed"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing %q in output: %s", want, data)
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("log permissions: %v, %v", info, err)
	}
}

func TestBootstrapLogNodeStartRetainsFailureAndOutput(t *testing.T) {
	service, workspace := newTestService(t)
	node, err := service.NodeCreate(context.Background(), NodeCreateInput{Directory: workspace, Slug: "failed-bootstrap-output"})
	if err != nil {
		t.Fatal(err)
	}
	node.AuthImportCompleted, node.WorkspaceSeeded = true, true
	bootstrap := BootstrapState{BootstrapCommands: []string{"print-markers-and-fail"}, ValidationCommand: "validation-must-not-run"}
	if err = service.store.SaveNode(node, bootstrap); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("install failed with status 23")
	service.sandbox = bootstrapOutputSandbox{SandboxClient: service.sandbox, err: failure}
	if _, err = service.NodeStart(context.Background(), node.ID); !errors.Is(err, failure) {
		t.Fatalf("start failure=%v", err)
	}
	stored, err := service.store.NodeByID(node.ID)
	if err != nil || stored.Status != NodeStatusFailed {
		t.Fatalf("failed state=%v, %v", stored.Status, err)
	}
	output, err := service.NodeLogSource(context.Background(), node.ID, nodeLogBootstrap)
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	for _, line := range output.Lines {
		text.WriteString(line.Text)
		text.WriteByte('\n')
	}
	for _, want := range []string{"print-markers-and-fail", "captured stdout", "captured stderr", "status 23"} {
		if !strings.Contains(text.String(), want) {
			t.Fatalf("missing %q in %s", want, text.String())
		}
	}
	if strings.Contains(text.String(), "validation-must-not-run") {
		t.Fatal("validation ran after failed bootstrap")
	}
	events, err := service.NodeLogSource(context.Background(), node.ID, nodeLogEvents)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, line := range events.Lines {
		if line.Kind == "node.start.failed" && line.Level == "error" {
			found = true
		}
	}
	if !found {
		t.Fatal("failure lost its structured event")
	}
}

func TestBootstrapLogBoundsEvenOneLargeWrite(t *testing.T) {
	service, _ := newTestService(t)
	path := filepath.Join(service.cfg.MetadataRoot, "bounded-bootstrap.log")
	writer, err := newRotatingLogWriterMode(path, 128, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	output := bootstrapLogWriter{writer: writer}
	if n, err := output.Write([]byte(strings.Repeat("x", 1024))); err != nil || n != 1024 {
		t.Fatalf("write=%d, %v", n, err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{path, path + ".1"} {
		info, err := os.Stat(file)
		if err != nil || info.Size() > 128 || info.Mode().Perm() != 0o600 {
			t.Fatalf("unbounded or public log: %v, %v", info, err)
		}
	}
}

func TestBootstrapLogRotationFailureKeepsCap(t *testing.T) {
	service, _ := newTestService(t)
	path := filepath.Join(service.cfg.MetadataRoot, "rotation-failure.log")
	writer, err := newRotatingLogWriterMode(path, 16, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	output := bootstrapLogWriter{writer: writer}
	if _, err = output.Write([]byte(strings.Repeat("x", 16))); err != nil {
		t.Fatal(err)
	}
	// A nonempty directory cannot be replaced by the rotated generation.
	if err = os.Mkdir(path+".1", 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(path+".1", "occupied"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = output.Write([]byte("overflow")); err == nil {
		t.Fatal("rotation failure ignored")
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() != 16 {
		t.Fatalf("cap violated: %v, %v", info, err)
	}
}
