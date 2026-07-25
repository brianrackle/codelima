package codelima

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

type recordingDaemonCaller struct {
	methods []string
}

func (c *recordingDaemonCaller) Call(_ context.Context, method string, _ any, _ any) error {
	c.methods = append(c.methods, method)
	return nil
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
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(cwd, "..", "..", "tmp", "daemon-resize-"+newID()[:8])
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })

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

func TestDaemonTUITerminalResizeSendsOnlyGeometryChanges(t *testing.T) {
	caller := &recordingDaemonCaller{}
	term := &daemonTUITerminal{client: caller, id: "term-1", stop: make(chan struct{})}

	term.Resize(80, 24)
	term.Resize(80, 24)
	term.Resize(100, 30)
	term.Resize(100, 30)

	if got := len(caller.methods); got != 2 {
		t.Fatalf("resize RPC calls = %d, want 2 for two distinct geometries", got)
	}
	for _, method := range caller.methods {
		if method != "terminal.resize" {
			t.Fatalf("resize RPC method = %q", method)
		}
	}
}
