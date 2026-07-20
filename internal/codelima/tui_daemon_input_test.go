package codelima

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"git.sr.ht/~rockorager/vaxis"
)

type blockingDaemonInputCaller struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *blockingDaemonInputCaller) Call(context.Context, string, any, any) error {
	c.once.Do(func() { close(c.started) })
	<-c.release
	return nil
}

func TestDaemonTUITerminalKeyInputDoesNotBlockOnDaemonRPC(t *testing.T) {
	t.Parallel()

	caller := &blockingDaemonInputCaller{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	term := &daemonTUITerminal{client: caller, id: "term-1", stop: make(chan struct{})}
	updateDone := make(chan struct{})
	go func() {
		term.Update(vaxis.Key{Text: "a", Keycode: 'a'})
		close(updateDone)
	}()

	select {
	case <-caller.started:
	case <-time.After(time.Second):
		t.Fatal("terminal input RPC did not start")
	}

	select {
	case <-updateDone:
		close(caller.release)
		term.Detach()
	case <-time.After(500 * time.Millisecond):
		close(caller.release)
		<-updateDone
		t.Fatal("terminal Update blocked the TUI event loop on the daemon RPC")
	}
}

type orderedDaemonInputCaller struct {
	mu     sync.Mutex
	calls  []daemonPasteCall
	called chan struct{}
}

func (c *orderedDaemonInputCaller) Call(_ context.Context, method string, params any, _ any) error {
	values, _ := params.(map[string]any)
	copyValues := make(map[string]any, len(values))
	maps.Copy(copyValues, values)
	c.mu.Lock()
	c.calls = append(c.calls, daemonPasteCall{method: method, params: copyValues})
	c.mu.Unlock()
	select {
	case c.called <- struct{}{}:
	default:
	}
	return nil
}

func (c *orderedDaemonInputCaller) snapshot() []daemonPasteCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.calls)
}

func TestDaemonTUITerminalDeliversQueuedKeysInOrder(t *testing.T) {
	t.Parallel()

	caller := &orderedDaemonInputCaller{called: make(chan struct{}, 3)}
	term := &daemonTUITerminal{client: caller, id: "term-1", stop: make(chan struct{})}
	term.Update(vaxis.Key{Text: "a", Keycode: 'a'})
	term.Update(vaxis.Key{Text: "b", Keycode: 'b'})
	term.Update(vaxis.Key{Keycode: vaxis.KeyEnter})

	for range 3 {
		select {
		case <-caller.called:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for queued terminal input")
		}
	}
	term.Detach()

	calls := caller.snapshot()
	if len(calls) != 3 {
		t.Fatalf("terminal made %d input calls, want 3", len(calls))
	}
	want := []rune{'a', 'b', vaxis.KeyEnter}
	for index, call := range calls {
		if call.method != "terminal.send_event" {
			t.Fatalf("call %d method = %q, want terminal.send_event", index, call.method)
		}
		got, ok := call.params["keycode"].(rune)
		if !ok {
			t.Fatalf("call %d keycode type = %T, want rune", index, call.params["keycode"])
		}
		if got != want[index] {
			t.Fatalf("call %d keycode = %q, want %q", index, got, want[index])
		}
	}
}

type failingDaemonInputCaller struct{ err error }

func (c failingDaemonInputCaller) Call(context.Context, string, any, any) error { return c.err }

func TestDaemonTUITerminalSurfacesAsynchronousInputFailure(t *testing.T) {
	t.Parallel()

	events := make(chan vaxis.Event, 1)
	term := &daemonTUITerminal{
		client:    failingDaemonInputCaller{err: errors.New("stale daemon connection")},
		id:        "term-1",
		postEvent: func(event vaxis.Event) { events <- event },
		stop:      make(chan struct{}),
	}
	term.Update(vaxis.Key{Text: "a", Keycode: 'a'})

	select {
	case event := <-events:
		failure, ok := event.(tuiTerminalErrorEvent)
		if !ok {
			t.Fatalf("posted event = %T, want tuiTerminalErrorEvent", event)
		}
		if failure.Err == nil || !strings.Contains(failure.Err.Error(), "stale daemon connection") {
			t.Fatalf("posted input error = %v", failure.Err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for asynchronous terminal input error")
	}
	term.Detach()
}
