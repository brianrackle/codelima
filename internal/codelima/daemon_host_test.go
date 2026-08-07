//go:build cgo && (darwin || linux)

package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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
	t.Cleanup(func() { _ = host.close(state.TerminalID) })
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
	root := newDaemonTestRoot(t, "d-")
	home := filepath.Join(root, "home")
	workspace := filepath.Join(root, "work")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
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

// restartRecordingDaemonTerminal is a daemon terminal whose renderer restarts
// and read-variant renderings are both countable, so the manual restart RPC and
// the publisher's cost can be asserted without a renderer worker process.
type restartRecordingDaemonTerminal struct {
	*fakeTUITerminal

	mu          sync.Mutex
	visibleText string
	restarts    int
	restartErr  error
	snapshots   int
	visible     map[ReadFormat]int
	recent      map[ReadFormat]int
}

func newRestartRecordingDaemonTerminal(visibleText string) *restartRecordingDaemonTerminal {
	return &restartRecordingDaemonTerminal{
		fakeTUITerminal: newFakeTUITerminal(),
		visibleText:     visibleText,
		visible:         map[ReadFormat]int{},
		recent:          map[ReadFormat]int{},
	}
}

func (t *restartRecordingDaemonTerminal) RestartRenderer() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.restarts++
	return t.restartErr
}

func (t *restartRecordingDaemonTerminal) ReadVisible(format ReadFormat) ReadResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.visible[format]++
	return ReadResult{Text: "visible-" + readFormatTestName(format)}
}

func (t *restartRecordingDaemonTerminal) ReadRecent(format ReadFormat) ReadResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recent[format]++
	return ReadResult{Text: "recent-" + readFormatTestName(format)}
}

func (t *restartRecordingDaemonTerminal) Snapshot() SnapshotResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.snapshots++
	cells := make([]SnapshotCell, 0, len(t.visibleText))
	for _, grapheme := range t.visibleText {
		cells = append(cells, SnapshotCell{Grapheme: string(grapheme), Width: 1})
	}
	return SnapshotResult{Snapshot: TerminalSnapshot{Cols: len(cells), Rows: 1, Cells: cells, Generation: 7}}
}

func (*restartRecordingDaemonTerminal) Scroll(int)       {}
func (*restartRecordingDaemonTerminal) SendInput([]byte) {}

func (t *restartRecordingDaemonTerminal) counts() (restarts, snapshots int, visible, recent map[ReadFormat]int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.restarts, t.snapshots, maps.Clone(t.visible), maps.Clone(t.recent)
}

func (t *restartRecordingDaemonTerminal) restartCount() int {
	restarts, _, _, _ := t.counts()
	return restarts
}

func (t *restartRecordingDaemonTerminal) variantRenders() int {
	_, _, visible, recent := t.counts()
	total := 0
	for _, count := range visible {
		total += count
	}
	for _, count := range recent {
		total += count
	}
	return total
}

func readFormatTestName(format ReadFormat) string {
	if format == ReadANSI {
		return "ansi"
	}
	return "text"
}

// TestDaemonRestartRendererRPCReachesTheTerminalAndRequiresInputOwnership drives
// terminal.restart_renderer over a real daemon server, so the method's delivery
// class, the server's ownership gate and the client call are all exercised on
// the path a TUI or CLI would take.
func TestDaemonRestartRendererRPCReachesTheTerminalAndRequiresInputOwnership(t *testing.T) {
	root := newDaemonTestRoot(t, "drr-")
	cfg := DefaultConfig(filepath.Join(root, "home"))
	service := NewService(cfg, newFakeSandbox(), strings.NewReader(""), ioDiscard{}, ioDiscard{})
	service.localTerminals = true
	if err := service.ensureDirectories(); err != nil {
		t.Fatal(err)
	}

	term := newRestartRecordingDaemonTerminal("restartable")
	host := newDaemonHost(service)
	host.terminals["term-1"] = &daemonTerminalEntry{state: daemon.TerminalState{TerminalID: "term-1"}, term: term}
	server := daemon.NewServer(daemon.Config{Home: service.cfg.MetadataRoot, Version: Version, Handler: host})
	host.wireServerLinks(server)
	host.markServing()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("server.Run() error = %v", err)
		}
	}()
	waitForDaemonPing(t, service.cfg.MetadataRoot)

	owner, err := daemonclient.Dial(context.Background(), daemonclient.Options{
		Home: service.cfg.MetadataRoot, Version: Version, WantInput: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Close() }()
	if !owner.Hello.InputOwner {
		t.Fatal("first client did not take the input lease")
	}
	if err := owner.RestartRenderer(context.Background(), "term-1"); err != nil {
		t.Fatalf("RestartRenderer() error = %v", err)
	}
	if got := term.restartCount(); got != 1 {
		t.Fatalf("renderer restarts = %d, want 1", got)
	}

	// The lease holder is the only client allowed to restart a renderer: it
	// replaces a process every other viewer is watching.
	observer, err := daemonclient.Dial(context.Background(), daemonclient.Options{
		Home: service.cfg.MetadataRoot, Version: Version, WantInput: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = observer.Close() }()
	if observer.Hello.InputOwner {
		t.Fatal("second client unexpectedly became input owner")
	}
	restartErr := observer.RestartRenderer(context.Background(), "term-1")
	if restartErr == nil {
		t.Fatal("observe-only client restarted a renderer without the input lease")
	}
	if !strings.Contains(restartErr.Error(), "observe-only") {
		t.Fatalf("observe-only restart error = %v, want the ownership gate", restartErr)
	}
	if got := term.restartCount(); got != 1 {
		t.Fatalf("renderer restarts after a rejected request = %d, want 1", got)
	}

	var rpcErr *daemon.RPCError
	missingErr := owner.RestartRenderer(context.Background(), "term-missing")
	if !errors.As(missingErr, &rpcErr) || rpcErr.Category != "NotFound" {
		t.Fatalf("restart of an unknown terminal = %v, want NotFound", missingErr)
	}
}

// A terminal runtime with no renderer process has nothing to restart, and must
// say so rather than silently succeeding.
func TestDaemonRestartRendererReportsRuntimesWithoutARenderer(t *testing.T) {
	t.Parallel()

	host := &daemonHost{terminals: map[string]*daemonTerminalEntry{
		"term-1": {
			state: daemon.TerminalState{TerminalID: "term-1"},
			term:  &resizeCountingDaemonTerminal{fakeTUITerminal: newFakeTUITerminal()},
		},
	}}
	params, err := json.Marshal(map[string]string{"terminal_id": "term-1"})
	if err != nil {
		t.Fatal(err)
	}

	_, handleErr := host.Handle(context.Background(), daemon.ClientContext{}, "terminal.restart_renderer", params)
	var rpcErr *daemon.RPCError
	if !errors.As(handleErr, &rpcErr) || rpcErr.Category != string(CategoryUnsupportedFeature) {
		t.Fatalf("restart on a rendererless runtime = %v, want UnsupportedFeature", handleErr)
	}
}

// A renderer restart and a live handoff are two ways of replacing the same
// renderer, so restart_renderer takes the update barrier like every other
// terminal mutation: it waits for an update in flight, and once one commits it
// is rejected as retryable against the successor daemon.
func TestDaemonRestartRendererWaitsForALiveUpdateAndIsRetryableAfterIt(t *testing.T) {
	t.Parallel()

	term := newRestartRecordingDaemonTerminal("barrier")
	host := &daemonHost{terminals: map[string]*daemonTerminalEntry{
		"term-1": {state: daemon.TerminalState{TerminalID: "term-1"}, term: term},
	}}

	host.updateGate.Lock()
	restarted := make(chan error, 1)
	go func() { restarted <- host.restartRenderer("term-1") }()
	select {
	case err := <-restarted:
		t.Fatalf("restart_renderer ran while a live update held the barrier: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if got := term.restartCount(); got != 0 {
		t.Fatalf("renderer restarts during a live update = %d, want 0", got)
	}

	host.replaced.Store(true)
	host.updateGate.Unlock()
	var err error
	select {
	case err = <-restarted:
	case <-time.After(5 * time.Second):
		t.Fatal("restart_renderer never resolved after the live update finished")
	}
	var rpcErr *daemon.RPCError
	if !errors.As(err, &rpcErr) || rpcErr.Category != "PreconditionFailed" {
		t.Fatalf("restart after a committed update = %v, want a retryable PreconditionFailed", err)
	}
	if replaced, _ := rpcErr.Fields["daemon_replaced"].(bool); !replaced {
		t.Fatalf("restart error fields = %v, want daemon_replaced", rpcErr.Fields)
	}
	if got := term.restartCount(); got != 0 {
		t.Fatalf("renderer restarts after a committed update = %d, want 0", got)
	}
}

// Publication used to re-render the ANSI viewport and both scrollback variants
// on every wake, up to 20 times a second per terminal, whether or not anything
// ever called terminal.read. Only the plain visible text rides along with the
// published screen now; the rest are pulled by terminal.read itself.
func TestDaemonSnapshotPublicationDoesNotRenderReadVariants(t *testing.T) {
	t.Parallel()

	term := newRestartRecordingDaemonTerminal("published")
	host := &daemonHost{terminals: map[string]*daemonTerminalEntry{}, broadcast: func(string, any) {}}
	entry := host.newTerminalEntry(daemon.TerminalState{TerminalID: "term-1"}, term)
	t.Cleanup(entry.stopSnapshotPublisher)
	host.terminals["term-1"] = entry

	waitForCondition(t, 5*time.Second, func() bool { return entry.cache.Load() != nil }, "first published screen")
	for range 32 {
		entry.requestSnapshot()
	}
	waitForCondition(t, 5*time.Second, func() bool {
		_, snapshots, _, _ := term.counts()
		return snapshots >= 2
	}, "repeated publication")
	if got := term.variantRenders(); got != 0 {
		t.Fatalf("publication rendered %d read variants with no terminal.read issued, want 0", got)
	}

	read := func(source, format string) ReadResult {
		t.Helper()
		params, err := json.Marshal(map[string]string{"terminal_id": "term-1", "source": source, "format": format})
		if err != nil {
			t.Fatal(err)
		}
		result, handleErr := host.Handle(context.Background(), daemon.ClientContext{}, "terminal.read", params)
		if handleErr != nil {
			t.Fatalf("terminal.read(%s,%s) error = %v", source, format, handleErr)
		}
		return result.(ReadResult)
	}

	// The default variant is the published screen's own text and still costs the
	// terminal nothing.
	if got := read("visible", "text"); got.Text != "published" {
		t.Fatalf("terminal.read(visible,text) = %q, want the published screen text", got.Text)
	}
	if got := term.variantRenders(); got != 0 {
		t.Fatalf("plain visible text cost %d renderings, want 0", got)
	}

	for _, testCase := range []struct{ source, format, want string }{
		{"recent", "text", "recent-text"},
		{"recent", "ansi", "recent-ansi"},
		{"visible", "ansi", "visible-ansi"},
	} {
		if got := read(testCase.source, testCase.format); got.Text != testCase.want {
			t.Fatalf("terminal.read(%s,%s) = %q, want %q", testCase.source, testCase.format, got.Text, testCase.want)
		}
	}
	_, _, visible, recent := term.counts()
	if visible[ReadText] != 0 {
		t.Fatalf("terminal.read pulled the plain visible text %d times, want it served from the published frame", visible[ReadText])
	}
	if visible[ReadANSI] != 1 || recent[ReadText] != 1 || recent[ReadANSI] != 1 {
		t.Fatalf("variant renderings = visible%v recent%v, want exactly one per requested variant", visible, recent)
	}
}
