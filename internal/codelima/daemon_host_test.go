//go:build cgo && (darwin || linux)

package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"github.com/brianrackle/codelima/internal/codelima/terminal"
	"github.com/brianrackle/codelima/internal/testutil"
)

func TestDaemonTerminalListPreservesCreationOrder(t *testing.T) {
	createdAt := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	host := &daemonHost{terminals: map[string]*daemonTerminalEntry{}}
	want := []string{"term-0", "term-1", "term-2", "term-3", "term-4", "term-5", "term-6", "term-7", "term-8"}
	for index := len(want) - 1; index >= 0; index-- {
		id := want[index]
		createdOffset := index
		if id == "term-1" {
			createdOffset = 0
		}
		host.terminals[id] = &daemonTerminalEntry{state: daemon.TerminalState{
			TerminalID: id,
			CreatedAt:  createdAt.Add(time.Duration(createdOffset) * time.Nanosecond),
		}}
	}

	for range 64 {
		states := host.list()
		if len(states) != len(want) {
			t.Fatalf("terminal.list returned %d tabs, want %d", len(states), len(want))
		}
		for index, state := range states {
			if state.TerminalID != want[index] {
				t.Fatalf("terminal.list order = %v, want creation order %v", terminalStateIDs(states), want)
			}
		}
	}
}

func TestDaemonTerminalMovePersistsTargetTabOrder(t *testing.T) {
	createdAt := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	sessionPath := filepath.Join(testutil.TempDir(t, "dm-"), "session.json")
	host := &daemonHost{
		session: sessionPath,
		terminals: map[string]*daemonTerminalEntry{
			"a-1": {state: daemon.TerminalState{TerminalID: "a-1", Target: "node:a", CreatedAt: createdAt}},
			"b-1": {state: daemon.TerminalState{TerminalID: "b-1", Target: "node:b", CreatedAt: createdAt.Add(time.Nanosecond)}},
			"a-2": {state: daemon.TerminalState{TerminalID: "a-2", Target: "node:a", CreatedAt: createdAt.Add(2 * time.Nanosecond)}},
			"a-3": {state: daemon.TerminalState{TerminalID: "a-3", Target: "node:a", CreatedAt: createdAt.Add(3 * time.Nanosecond)}},
		},
		broadcast: func(string, any) {},
	}

	move := func(terminalID string, delta int) error {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"terminal_id": terminalID, "delta": delta})
		if err != nil {
			t.Fatal(err)
		}
		_, err = host.Handle(context.Background(), daemon.ClientContext{}, "terminal.move", raw)
		return err
	}

	if err := move("a-3", -1); err != nil {
		t.Fatalf("move a-3 left: %v", err)
	}
	if got, want := terminalStateIDs(host.list()), []string{"a-1", "b-1", "a-3", "a-2"}; !slices.Equal(got, want) {
		t.Fatalf("terminal.list after move = %v, want %v", got, want)
	}
	assertPersistedTerminalOrder(t, sessionPath, []string{"a-1", "b-1", "a-3", "a-2"})
	if err := move("a-3", 1); err != nil {
		t.Fatalf("move a-3 right: %v", err)
	}
	if got, want := terminalStateIDs(host.list()), []string{"a-1", "b-1", "a-2", "a-3"}; !slices.Equal(got, want) {
		t.Fatalf("terminal.list after reverse move = %v, want %v", got, want)
	}

	// A boundary move is successful but keeps the tab in place.
	if err := move("a-1", -1); err != nil {
		t.Fatalf("move first tab left: %v", err)
	}
	if got, want := terminalStateIDs(host.list()), []string{"a-1", "b-1", "a-2", "a-3"}; !slices.Equal(got, want) {
		t.Fatalf("terminal.list after boundary move = %v, want %v", got, want)
	}

	assertPersistedTerminalOrder(t, sessionPath, []string{"a-1", "b-1", "a-2", "a-3"})

	if err := move("missing", 1); err == nil {
		t.Fatalf("move missing terminal succeeded, want error")
	}
	if err := move("a-2", 2); err == nil {
		t.Fatalf("move with delta 2 succeeded, want error")
	}
}

func assertPersistedTerminalOrder(t *testing.T, sessionPath string, want []string) {
	t.Helper()
	data, err := os.ReadFile(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	var session daemon.Session
	if err := json.Unmarshal(data, &session); err != nil {
		t.Fatal(err)
	}
	if got := terminalStateIDs(session.Terminals); !slices.Equal(got, want) {
		t.Fatalf("persisted terminal order = %v, want %v", got, want)
	}
}

func terminalStateIDs(states []daemon.TerminalState) []string {
	ids := make([]string, len(states))
	for index, state := range states {
		ids[index] = state.TerminalID
	}
	return ids
}

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
	host.terminalFactory = newTUITerminal
	state, err := host.open(context.Background(), terminalOpenParams{
		Target: "node:" + node.ID,
		Kind:   terminal.NodeHostShell.String(),
	}, "")
	if err != nil {
		t.Fatalf("open(node host shell) error = %v", err)
	}
	t.Cleanup(func() { _ = host.close(context.Background(), state.TerminalID) })
	if state.CWD != workspace {
		t.Fatalf("node host terminal cwd = %q, want %q", state.CWD, workspace)
	}
	if fake.listCalls != 0 {
		t.Fatalf("node host terminal queried runtime %d times", fake.listCalls)
	}
}

func TestDaemonNodeListIncludesLiveResourceUsage(t *testing.T) {
	t.Parallel()

	service, _ := newTestService(t)
	node := saveForwardingTestNode(t, service, "cpu-node-list")
	sampledAt := time.Now().UTC()
	host := newDaemonHost(service)
	host.forwarder = &dynamicForwarder{
		cpu: map[string]nodeCPUUsageSample{
			node.ID: {Percent: 42.5, SampledAt: sampledAt},
		},
		resources: map[string]nodeResourceUsageSample{
			node.ID: {
				Memory:    guestResourceUsage{UsedBytes: 3 << 30, TotalBytes: 4 << 30},
				Disk:      guestResourceUsage{UsedBytes: 9 << 30, TotalBytes: 32 << 30},
				SampledAt: sampledAt,
			},
		},
	}

	result, err := host.Handle(context.Background(), daemon.ClientContext{}, "node.list", json.RawMessage(`{"include_deleted":false}`))
	if err != nil {
		t.Fatalf("node.list error = %v", err)
	}
	nodes, ok := result.([]Node)
	if !ok || len(nodes) != 1 || nodes[0].LastRuntimeObservation == nil {
		t.Fatalf("node.list result = %#v, want one observed node", result)
	}
	if usage := nodes[0].LastRuntimeObservation.CPUUsagePercent; usage == nil || *usage != 42.5 {
		t.Fatalf("node.list CPU usage = %v, want 42.5", usage)
	}
	observation := nodes[0].LastRuntimeObservation
	if observation.MemoryUsedBytes == nil || *observation.MemoryUsedBytes != 3<<30 ||
		observation.MemoryTotalBytes == nil || *observation.MemoryTotalBytes != 4<<30 {
		t.Fatalf("node.list memory usage = %v/%v, want 3 GiB/4 GiB", observation.MemoryUsedBytes, observation.MemoryTotalBytes)
	}
	if observation.DiskUsedBytes == nil || *observation.DiskUsedBytes != 9<<30 ||
		observation.DiskTotalBytes == nil || *observation.DiskTotalBytes != 32<<30 {
		t.Fatalf("node.list disk usage = %v/%v, want 9 GiB/32 GiB", observation.DiskUsedBytes, observation.DiskTotalBytes)
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
	node := Node{
		ID:            newID(),
		Slug:          "daemon-test",
		SandboxName:   "daemon-test",
		DirectoryPath: workspace,
		Status:        NodeStatusStopped,
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}
	if err := service.store.SaveNode(node, BootstrapState{}); err != nil {
		t.Fatalf("SaveNode() error = %v", err)
	}
	host := newDaemonHost(service)
	host.terminalFactory = newTUITerminal
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
	err = owner.Call(context.Background(), "terminal.open", map[string]any{"target": "node:" + node.ID, "kind": "node-host-shell", "cols": 80, "rows": 24}, &state)
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
