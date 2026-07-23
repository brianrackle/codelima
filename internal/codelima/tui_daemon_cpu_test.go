package codelima

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"git.sr.ht/~rockorager/vaxis"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/terminal"
)

type countingDaemonSnapshotCaller struct {
	mu     sync.Mutex
	calls  int
	called chan struct{}
}

func (c *countingDaemonSnapshotCaller) Call(_ context.Context, method string, _ any, result any) error {
	if method != "terminal.snapshot" {
		return nil
	}
	c.mu.Lock()
	c.calls++
	calls := c.calls
	c.mu.Unlock()
	if snapshot, ok := result.(*daemon.Snapshot); ok {
		*snapshot = daemon.Snapshot{Cols: 80, Rows: 24, Generation: uint64(calls)}
	}
	select {
	case c.called <- struct{}{}:
	default:
	}
	return nil
}

func (c *countingDaemonSnapshotCaller) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func TestDaemonTUITerminalIdleDoesNotPollSnapshots(t *testing.T) {
	t.Parallel()

	caller := &countingDaemonSnapshotCaller{called: make(chan struct{}, 8)}
	term := newDaemonTUITerminal(caller, "term-1", func(vaxis.Event) {})
	t.Cleanup(term.Detach)

	term.requestSnapshot()
	select {
	case <-caller.called:
	case <-time.After(time.Second):
		t.Fatal("initial terminal snapshot was not requested")
	}

	// The former 50 ms ticker made at least two more full-grid RPCs during
	// this idle window for every open tab in every TUI process.
	time.Sleep(175 * time.Millisecond)
	if got := caller.count(); got != 1 {
		t.Fatalf("idle terminal made %d snapshot RPCs, want exactly the requested one", got)
	}
}

type fakeDaemonSnapshotView struct {
	*fakeTUITerminal
	dirty   int
	refresh int
}

func (t *fakeDaemonSnapshotView) markSnapshotDirty() { t.dirty++ }
func (t *fakeDaemonSnapshotView) requestSnapshot()   { t.refresh++ }

func TestTUISessionStoreRefreshesOnlyTheActiveDirtyDaemonTerminal(t *testing.T) {
	t.Parallel()

	active := &fakeDaemonSnapshotView{fakeTUITerminal: newFakeTUITerminal()}
	hidden := &fakeDaemonSnapshotView{fakeTUITerminal: newFakeTUITerminal()}
	store := &tuiSessionStore{
		sessions: map[string]*tuiSession{
			"node:one#1": {key: "node:one#1", terminalID: terminal.TerminalID("term-active")},
			"node:one#2": {key: "node:one#2", terminalID: terminal.TerminalID("term-hidden")},
		},
		registry: terminal.NewTerminalRuntimeRegistry[tuiTerminal](),
	}
	store.registry.Register("term-active", active)
	store.registry.Register("term-hidden", hidden)

	store.markDaemonTerminalDirty("term-hidden", "node:one#1")
	if hidden.dirty != 1 || hidden.refresh != 0 {
		t.Fatalf("hidden dirty terminal state = (%d dirty, %d refresh), want (1, 0)", hidden.dirty, hidden.refresh)
	}
	store.markDaemonTerminalDirty("term-active", "node:one#1")
	if active.dirty != 1 || active.refresh != 1 {
		t.Fatalf("active dirty terminal state = (%d dirty, %d refresh), want (1, 1)", active.dirty, active.refresh)
	}
}

func TestDaemonDirtyEventRequestsSnapshotOnTheUIEventLoop(t *testing.T) {
	t.Parallel()

	events := make(chan vaxis.Event, 1)
	store := &tuiSessionStore{postEvent: func(event vaxis.Event) { events <- event }}
	store.handleDaemonEvent(daemon.Event{
		Event: "terminal.dirty",
		Data:  map[string]any{"terminal_id": "term-1"},
	})

	select {
	case event := <-events:
		dirty, ok := event.(tuiDaemonTerminalDirtyEvent)
		if !ok {
			t.Fatalf("posted event = %T, want tuiDaemonTerminalDirtyEvent", event)
		}
		if dirty.TerminalID != "term-1" {
			t.Fatalf("dirty terminal id = %q, want term-1", dirty.TerminalID)
		}
	case <-time.After(time.Second):
		t.Fatal("dirty terminal event was not posted")
	}
}

func TestDaemonLifecycleEventsMarkTheTUIConnectionDisconnected(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		event     string
		wantError string
	}{
		{name: "shutdown", event: "daemon.shutdown", wantError: "codelima daemon stopped"},
		{name: "update", event: "daemon.update_committed", wantError: "codelima daemon updated; quit and reopen CodeLima to reconnect"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			events := make(chan vaxis.Event, 1)
			store := &tuiSessionStore{postEvent: func(event vaxis.Event) { events <- event }}
			store.handleDaemonEvent(daemon.Event{Event: test.event})

			select {
			case event := <-events:
				disconnected, ok := event.(tuiDaemonDisconnectedEvent)
				if !ok {
					t.Fatalf("posted event = %T, want tuiDaemonDisconnectedEvent", event)
				}
				if disconnected.Err == nil || disconnected.Err.Error() != test.wantError {
					t.Fatalf("disconnect error = %v, want %q", disconnected.Err, test.wantError)
				}
			case <-time.After(time.Second):
				t.Fatal("daemon disconnect event was not posted")
			}
		})
	}
}

type failingDaemonEventReader struct {
	mu    sync.Mutex
	calls int
}

func (r *failingDaemonEventReader) NextEvent(context.Context) (daemon.Event, error) {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	return daemon.Event{}, io.EOF
}

func (r *failingDaemonEventReader) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func TestDaemonEventLoopStopsAfterPermanentReadFailure(t *testing.T) {
	t.Parallel()

	reader := &failingDaemonEventReader{}
	var reported error
	runDaemonEventLoop(context.Background(), reader, func(daemon.Event) {}, func(err error) {
		reported = err
	})

	if got := reader.count(); got != 1 {
		t.Fatalf("event reads after permanent failure = %d, want 1", got)
	}
	if reported == nil || !errors.Is(reported, io.EOF) {
		t.Fatalf("reported event-stream error = %v, want EOF", reported)
	}
}

type timeoutThenShutdownEventReader struct {
	calls int
}

func (r *timeoutThenShutdownEventReader) NextEvent(context.Context) (daemon.Event, error) {
	r.calls++
	if r.calls == 1 {
		return daemon.Event{}, &net.DNSError{IsTimeout: true, Err: "idle event stream"}
	}
	return daemon.Event{Event: "daemon.shutdown"}, nil
}

func TestDaemonEventLoopKeepsWaitingAfterIdleReadTimeout(t *testing.T) {
	t.Parallel()

	reader := &timeoutThenShutdownEventReader{}
	handled := 0
	reported := 0
	runDaemonEventLoop(context.Background(), reader, func(daemon.Event) {
		handled++
	}, func(error) {
		reported++
	})

	if reader.calls != 2 || handled != 1 || reported != 0 {
		t.Fatalf("timeout event loop = (%d reads, %d handled, %d errors), want (2, 1, 0)", reader.calls, handled, reported)
	}
}
