package codelima

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestNodeLogSourcesRejectSpecialFiles(t *testing.T) {
	service, _ := newTestService(t)
	node := saveForwardingTestNode(t, service, "log-fifo")
	path := filepath.Join(service.store.nodeDir(node.ID), "bootstrap.log")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.NodeLogSource(context.Background(), node.ID, nodeLogBootstrap); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("special log file accepted: %v", err)
	}
}

func TestNodeLogSourcesReadWithoutRuntimeAndKeepStructuredEvents(t *testing.T) {
	service, _ := newTestService(t)
	node := saveForwardingTestNode(t, service, "logs-node")
	path := service.store.nodeEventsPath(node.ID)
	if err := os.WriteFile(path, []byte("{\"timestamp\":\"2026-10-24T12:00:00Z\",\"type\":\"node.start.failed\",\"message\":\"broken\",\"fields\":{\"exit_code\":23}}\ninvalid\n{\"type\":"), 0o600); err != nil {
		t.Fatal(err)
	}
	service.sandbox.(*fakeSandbox).listErr = os.ErrPermission
	snapshot, err := service.NodeLogSource(context.Background(), node.ID, nodeLogEvents)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Lines) != 1 || snapshot.Lines[0].Level != "error" || snapshot.Lines[0].Kind != "node.start.failed" || !strings.Contains(snapshot.Lines[0].Text, "23") {
		t.Fatalf("wrong event log: %+v", snapshot)
	}
	missing, err := service.NodeLogSource(context.Background(), node.ID, nodeLogBootstrap)
	if err != nil || !missing.Missing || len(missing.Lines) != 0 {
		t.Fatalf("missing log=%+v, %v", missing, err)
	}
	if _, err := service.NodeLogSource(context.Background(), node.ID, "../../settings"); err == nil {
		t.Fatal("unknown log source accepted")
	}
}

func TestNodeLogSourcesBoundTailAndSanitizeTerminalControls(t *testing.T) {
	service, _ := newTestService(t)
	node := saveForwardingTestNode(t, service, "log-tail")
	path := filepath.Join(service.store.nodeDir(node.ID), "bootstrap.log")
	text := strings.Repeat("old output\n", nodeLogLineLimit+20) + "\x1b[31mred\x1b[0m 界 👩🏽‍💻\x1b]52;c;secret\a done\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.NodeLogSource(context.Background(), node.ID, nodeLogBootstrap)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Lines) != nodeLogLineLimit || !snapshot.Truncated || snapshot.Lines[len(snapshot.Lines)-1].Text != "red 界 👩🏽‍💻 done" {
		t.Fatalf("tail/sanitization failed: %d, %v, %q", len(snapshot.Lines), snapshot.Truncated, snapshot.Lines[len(snapshot.Lines)-1].Text)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", nodeLogByteLimit+100)+"\nlast line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err = service.NodeLogSource(context.Background(), node.ID, nodeLogBootstrap)
	if err != nil || len(snapshot.Lines) != 1 || snapshot.Lines[0].Text != "last line" || !snapshot.Truncated {
		t.Fatalf("oversized tail=%+v, %v", snapshot, err)
	}
}

func TestNodeLogSourcesResolveLimaFilesWithoutStartingRuntime(t *testing.T) {
	service, _ := newTestService(t)
	node := saveForwardingTestNode(t, service, "lima-log")
	root := filepath.Join(service.cfg.MetadataRoot, "qa-lima")
	if err := os.MkdirAll(filepath.Join(root, node.SandboxName), 0o700); err != nil {
		t.Fatal(err)
	}
	service.sandbox = &LimaClient{LimaHome: root}
	for source, file := range map[nodeLogSource]string{nodeLogLima: "ha.stderr.log", nodeLogSerial: "serial.log"} {
		if err := os.WriteFile(filepath.Join(root, node.SandboxName, file), []byte("{\"level\":\"warn\",\"msg\":\"guest unavailable\"}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := service.NodeLogSource(context.Background(), node.ID, source)
		if err != nil || len(got.Lines) != 1 {
			t.Fatalf("%s: %+v, %v", source, got, err)
		}
		if source == nodeLogLima && got.Lines[0].Level != "warn" {
			t.Fatal("Lima JSON severity was lost")
		}
	}
}
