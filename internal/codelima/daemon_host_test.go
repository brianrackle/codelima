//go:build cgo && (darwin || linux)

package codelima

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/test_lima/internal/codelima/daemon"
	"github.com/brianrackle/test_lima/internal/codelima/daemonclient"
	"github.com/brianrackle/test_lima/internal/codelima/terminal"
)

func TestDaemonNodeHostTerminalUsesStoredDirectoryWithoutRuntimeObservation(t *testing.T) {
	service, workspace := newTestService(t)
	fake := service.sandbox.(*fakeSandbox)
	fake.listErr = errors.New("runtime observation unavailable")
	node := Node{
		ID:            newID(),
		Slug:          "host-only",
		SandboxName:   "host-only",
		DirectoryPath: workspace,
		Status:        NodeStatusStopped,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := service.store.SaveNode(node, BootstrapState{}); err != nil {
		t.Fatalf("SaveNode() error = %v", err)
	}

	host := newDaemonHost(service)
	state, err := host.open(context.Background(), terminalOpenParams{
		Target: "node:" + node.ID,
		Kind:   terminal.NodeHostShell.String(),
	}, "")
	if err != nil {
		t.Fatalf("open(node host shell) error = %v", err)
	}
	t.Cleanup(func() { _ = host.close(state.TerminalID) })
	if state.CWD != workspace {
		t.Fatalf("node host terminal cwd = %q, want %q", state.CWD, workspace)
	}
	if fake.listCalls != 0 {
		t.Fatalf("node host terminal queried runtime %d times", fake.listCalls)
	}
}

func TestDaemonTerminalSurvivesClientDetachAndEnforcesInputOwnership(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(cwd, "..", "..", "tmp", "d-"+newID()[:8])
	workspace := filepath.Join(cwd, "..", "..", "tmp", "w-"+newID()[:8])
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home); _ = os.RemoveAll(workspace) })
	cfg := DefaultConfig(home)
	service := NewService(cfg, newFakeSandbox(), strings.NewReader(""), ioDiscard{}, ioDiscard{})
	service.localTerminals = true
	if err := service.ensureDirectories(); err != nil {
		t.Fatal(err)
	}
	project, err := service.ProjectCreate(context.Background(), ProjectCreateInput{Slug: "daemon-test", WorkspacePath: workspace})
	if err != nil {
		t.Fatalf("ProjectCreate() error = %v", err)
	}
	host := newDaemonHost(service)
	server := daemon.NewServer(daemon.Config{Home: service.cfg.MetadataRoot, Version: Version, Handler: host})
	host.broadcast = server.Broadcast
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	waitForDaemonPing(t, service.cfg.MetadataRoot)

	owner, err := daemonclient.Dial(context.Background(), daemonclient.Options{Home: service.cfg.MetadataRoot, Version: Version, WantInput: true})
	if err != nil {
		t.Fatal(err)
	}
	var state daemon.TerminalState
	err = owner.Call(context.Background(), "terminal.open", map[string]any{"target": "project:" + project.ID, "kind": "project-host-shell", "cols": 80, "rows": 24}, &state)
	if err != nil {
		_ = owner.Close()
		cancel()
		<-done
		t.Skipf("daemon terminal unavailable: %v", err)
	}
	if err := owner.Call(context.Background(), "terminal.send_text", map[string]any{"terminal_id": state.TerminalID, "text": "printf daemon-detach-ok\\n\r"}, nil); err != nil {
		t.Fatalf("send_text error = %v", err)
	}

	observer, err := daemonclient.Dial(context.Background(), daemonclient.Options{Home: service.cfg.MetadataRoot, Version: Version, WantInput: true})
	if err != nil {
		t.Fatal(err)
	}
	if observer.Hello.InputOwner {
		t.Fatal("second client unexpectedly became input owner")
	}
	if err := observer.Call(context.Background(), "terminal.send_text", map[string]any{"terminal_id": state.TerminalID, "text": "blocked"}, nil); err == nil {
		t.Fatal("observe-only client unexpectedly sent input")
	}
	_ = observer.Close()
	_ = owner.Close()

	reconnected, err := daemonclient.Dial(context.Background(), daemonclient.Options{Home: service.cfg.MetadataRoot, Version: Version, WantInput: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reconnected.Close() }()
	if !reconnected.Hello.InputOwner {
		if err := reconnected.Call(context.Background(), "input.takeover", nil, nil); err != nil {
			t.Fatalf("input.takeover error = %v", err)
		}
	}
	var states []daemon.TerminalState
	if err := reconnected.Call(context.Background(), "terminal.list", nil, &states); err != nil || len(states) != 1 {
		t.Fatalf("terminal.list = %#v, %v", states, err)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		var result ReadResult
		if readErr := reconnected.Call(context.Background(), "terminal.read", map[string]any{"terminal_id": state.TerminalID, "source": "recent", "format": "text"}, &result); readErr != nil {
			return false
		}
		return strings.Contains(result.Text, "daemon-detach-ok")
	}, "detached daemon terminal output")
	if err := reconnected.Call(context.Background(), "terminal.close", map[string]string{"terminal_id": state.TerminalID}, nil); err != nil {
		t.Fatalf("terminal.close error = %v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("server.Run() error = %v", err)
	}
}

func waitForDaemonPing(t *testing.T, home string) {
	t.Helper()
	waitForCondition(t, 5*time.Second, func() bool {
		_, err := daemonclient.Ping(context.Background(), home, Version)
		return err == nil
	}, "daemon ping")
}
