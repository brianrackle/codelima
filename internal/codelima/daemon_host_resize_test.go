package codelima

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"git.sr.ht/~rockorager/vaxis"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

type recordingDaemonCaller struct {
	mu      sync.Mutex
	methods []string
}

func (c *recordingDaemonCaller) Call(_ context.Context, method string, _ any, _ any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.methods = append(c.methods, method)
	return nil
}

func (c *recordingDaemonCaller) recorded() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.methods)
}

type resizeCountingDaemonTerminal struct {
	*fakeTUITerminal
	resizeCalls   int
	snapshotCalls int
}

func (t *resizeCountingDaemonTerminal) Resize(width, height int) {
	t.resizeCalls++
	t.fakeTUITerminal.Resize(width, height)
}

func (*resizeCountingDaemonTerminal) ReadVisible(ReadFormat) ReadResult { return ReadResult{} }
func (*resizeCountingDaemonTerminal) ReadRecent(ReadFormat) ReadResult  { return ReadResult{} }
func (t *resizeCountingDaemonTerminal) Snapshot() SnapshotResult {
	t.snapshotCalls++
	return SnapshotResult{}
}
func (*resizeCountingDaemonTerminal) Scroll(int)       {}
func (*resizeCountingDaemonTerminal) SendInput([]byte) {}

func TestDaemonHostResizeSkipsUnchangedGeometry(t *testing.T) {
	tmp := newDaemonTestRoot(t, "daemon-resize-")

	term := &resizeCountingDaemonTerminal{fakeTUITerminal: newFakeTUITerminal()}
	state := daemon.TerminalState{TerminalID: "term-1", Cols: 80, Rows: 24}
	broadcasts := 0
	host := &daemonHost{
		terminals: map[string]*daemonTerminalEntry{"term-1": {state: state, term: term}},
		session:   filepath.Join(tmp, "session.json"),
		broadcast: func(string, any) { broadcasts++ },
	}

	resize := func(cols, rows int) daemon.TerminalState {
		t.Helper()
		params, marshalErr := json.Marshal(map[string]any{"terminal_id": "term-1", "cols": cols, "rows": rows})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		result, handleErr := host.Handle(context.Background(), daemon.ClientContext{}, "terminal.resize", params)
		if handleErr != nil {
			t.Fatalf("terminal.resize error = %v", handleErr)
		}
		return result.(daemon.TerminalState)
	}

	if got := resize(80, 24); got.Cols != 80 || got.Rows != 24 {
		t.Fatalf("unchanged resize result = %#v", got)
	}
	if term.resizeCalls != 0 || broadcasts != 0 {
		t.Fatalf("unchanged resize performed %d terminal resizes and %d broadcasts", term.resizeCalls, broadcasts)
	}

	if got := resize(100, 30); got.Cols != 100 || got.Rows != 30 {
		t.Fatalf("changed resize result = %#v", got)
	}
	if term.resizeCalls != 1 || broadcasts != 1 {
		t.Fatalf("changed resize performed %d terminal resizes and %d broadcasts, want 1 each", term.resizeCalls, broadcasts)
	}
}

func TestDaemonHostSnapshotReadsPublishedCacheWithoutCallingTerminal(t *testing.T) {
	t.Parallel()

	term := &resizeCountingDaemonTerminal{fakeTUITerminal: newFakeTUITerminal()}
	entry := &daemonTerminalEntry{state: daemon.TerminalState{TerminalID: "term-1"}, term: term}
	entry.cache.Store(&daemonTerminalCache{snapshot: daemon.Snapshot{
		Cols:             80,
		Rows:             24,
		Generation:       9,
		SnapshotSequence: 4,
	}})
	host := &daemonHost{terminals: map[string]*daemonTerminalEntry{"term-1": entry}}
	params, err := json.Marshal(map[string]string{"terminal_id": "term-1"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := host.Handle(context.Background(), daemon.ClientContext{}, "terminal.snapshot", params)
	if err != nil {
		t.Fatalf("terminal.snapshot error = %v", err)
	}
	snapshot := result.(daemon.Snapshot)
	if snapshot.Generation != 9 || snapshot.SnapshotSequence != 4 {
		t.Fatalf("terminal.snapshot = %#v", snapshot)
	}
	if term.snapshotCalls != 0 {
		t.Fatalf("terminal.snapshot called live terminal %d times, want cached local read", term.snapshotCalls)
	}
}

// Resize is called from Draw, so it only records the latest geometry; the
// background reassert loop is what reaches the daemon. Repeats of a geometry the
// daemon already has stay off the wire.
func TestDaemonTUITerminalResizeSendsOnlyGeometryChanges(t *testing.T) {
	caller := &recordingDaemonCaller{}
	term := &daemonTUITerminal{client: caller, id: "term-1", stop: make(chan struct{})}
	t.Cleanup(term.Detach)

	term.Resize(80, 24)
	waitForCondition(t, time.Second, func() bool { return len(caller.recorded()) == 1 }, "first resize reassert")
	term.Resize(80, 24)
	term.Resize(100, 30)
	waitForCondition(t, time.Second, func() bool { return len(caller.recorded()) == 2 }, "second resize reassert")
	term.Resize(100, 30)

	methods := caller.recorded()
	if len(methods) != 2 {
		t.Fatalf("resize RPC calls = %d, want 2 for two distinct geometries", len(methods))
	}
	for _, method := range methods {
		if method != "terminal.resize" {
			t.Fatalf("resize RPC method = %q", method)
		}
	}
}

func TestDaemonTUITerminalFocusSendsOnlyFocusTransitions(t *testing.T) {
	caller := &recordingDaemonCaller{}
	term := &daemonTUITerminal{client: caller, id: "term-1", stop: make(chan struct{})}

	term.Focus()
	term.Focus()
	term.Blur()
	term.Blur()
	term.Focus()

	waitForCondition(t, time.Second, func() bool { return len(caller.recorded()) == 2 }, "focus transitions to reach the daemon")
	methods := caller.recorded()
	if len(methods) != 2 {
		t.Fatalf("focus RPC calls = %d, want 2 for two transitions into focus", len(methods))
	}
	for _, method := range methods {
		if method != "terminal.focus" {
			t.Fatalf("focus RPC method = %q", method)
		}
	}
}

func TestDaemonHostRedrawDefersDirtyNotificationUntilFreshSnapshot(t *testing.T) {
	entry := &daemonTerminalEntry{
		state:        daemon.TerminalState{TerminalID: "term-1"},
		term:         &resizeCountingDaemonTerminal{fakeTUITerminal: newFakeTUITerminal()},
		snapshotWake: make(chan struct{}, 1),
	}
	entry.cache.Store(&daemonTerminalCache{snapshot: daemon.Snapshot{Cols: 80, Rows: 24}})
	broadcasts := 0
	host := &daemonHost{
		terminals: map[string]*daemonTerminalEntry{"term-1": entry},
		broadcast: func(string, any) {
			broadcasts++
		},
	}

	host.handleTerminalEvent("term-1", vaxis.Redraw{})

	if broadcasts != 0 {
		t.Fatalf("redraw broadcasts = %d, want publication to emit the only dirty event", broadcasts)
	}
	if current := entry.cache.Load(); current == nil || !current.snapshot.Stale {
		t.Fatal("redraw did not mark the cached snapshot stale")
	}
	select {
	case <-entry.snapshotWake:
	default:
		t.Fatal("redraw did not schedule a fresh snapshot")
	}
}
