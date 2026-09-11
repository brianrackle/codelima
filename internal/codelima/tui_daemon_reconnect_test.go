package codelima

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"go.rockorager.dev/vaxis"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"github.com/brianrackle/codelima/internal/testutil"
)

type reconnectTestHandler struct {
	terminal daemon.TerminalState
}

func (h reconnectTestHandler) Handle(_ context.Context, _ daemon.ClientContext, method string, _ json.RawMessage) (any, error) {
	switch method {
	case "terminal.list":
		return []daemon.TerminalState{h.terminal}, nil
	case "terminal.snapshot":
		return daemon.Snapshot{}, nil
	default:
		return map[string]bool{"ok": true}, nil
	}
}

func (h reconnectTestHandler) Snapshot(context.Context) (any, error) {
	return map[string]any{
		"session": daemon.Session{
			Version:   daemon.SessionVersion,
			Terminals: []daemon.TerminalState{h.terminal},
		},
	}, nil
}

func (reconnectTestHandler) TerminalCount() int { return 1 }
func (reconnectTestHandler) Close() error       { return nil }

func TestTUISessionReconnectsAndRetainsDaemonTerminalAndClipboard(t *testing.T) {
	home := testutil.TempDir(t, "tui-reconnect-")
	state := daemon.TerminalState{
		TerminalID: "term-1",
		TabID:      "node:node-1#term-1",
		Target:     "node:node-1",
		Kind:       "node-shell",
		Label:      "node-1",
		CreatedAt:  time.Now(),
		Cols:       80,
		Rows:       24,
	}
	server := daemon.NewServer(daemon.Config{
		Home:    home,
		Version: Version,
		Handler: reconnectTestHandler{terminal: state},
	})
	ctx, cancel := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-serverDone; err != nil {
			t.Errorf("Server.Run() error = %v", err)
		}
	})
	waitForCondition(t, time.Second, func() bool {
		_, err := daemonclient.Ping(context.Background(), home, Version)
		return err == nil
	}, "daemon startup")

	requestClient, err := daemonclient.Dial(context.Background(), daemonclient.Options{
		Home:      home,
		Version:   Version,
		WantInput: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(DefaultConfig(home), newFakeSandbox(), nil, ioDiscard{}, ioDiscard{})
	service.daemonClient = requestClient
	events := make(chan vaxis.Event, 32)
	store := newTUISessionStore(ctx, service, func(event vaxis.Event) { events <- event })
	t.Cleanup(func() {
		store.Close()
		_ = requestClient.Close()
	})

	var firstSync tuiDaemonSynchronizedEvent
	select {
	case event := <-events:
		var ok bool
		firstSync, ok = event.(tuiDaemonSynchronizedEvent)
		if !ok {
			t.Fatalf("first supervisor event = %T, want tuiDaemonSynchronizedEvent", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for initial synchronization")
	}
	if err := store.applyDaemonSynchronization(firstSync.Snapshot); err != nil {
		firstSync.complete(err)
		t.Fatal(err)
	}
	firstSync.complete(nil)
	assertClipboard := func(text string) {
		t.Helper()
		if !server.EmitInputOwner(daemon.EventTerminalClipboard, daemon.TerminalClipboardEvent{
			TerminalID: state.TerminalID, TabID: state.TabID, Text: text,
		}) {
			t.Fatal("clipboard was dropped between the input connection and the TUI event connection")
		}
		select {
		case event := <-events:
			clip, ok := event.(tuiClipboardEvent)
			if !ok || clip.TargetKey != state.TabID || clip.Text != text {
				t.Fatalf("clipboard delivery = %#v, want %q for %q", event, text, state.TabID)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for clipboard on the TUI event connection")
		}
	}
	assertClipboard("before reconnect")
	before, ok := store.Session(state.TabID)
	if !ok || before.terminalID != "term-1" {
		t.Fatalf("session before disconnect = %#v, %v", before, ok)
	}

	store.eventMu.Lock()
	connectionID := store.events.HelloSnapshot().ConnectionID
	store.eventMu.Unlock()
	if connectionID == requestClient.HelloSnapshot().ConnectionID {
		t.Fatal("test requires separate request and event connections")
	}
	if !server.DisconnectClient(connectionID, daemon.CloseAdministrative) {
		t.Fatalf("event connection %d was not registered", connectionID)
	}

	sawDisconnect := false
	var secondSync tuiDaemonSynchronizedEvent
	deadline := time.After(3 * time.Second)
	for secondSync.Snapshot.DaemonEpoch == "" {
		select {
		case event := <-events:
			switch value := event.(type) {
			case tuiDaemonDisconnectedEvent:
				sawDisconnect = true
			case tuiDaemonSynchronizedEvent:
				secondSync = value
			}
		case <-deadline:
			t.Fatal("timed out waiting for disconnect and resynchronization")
		}
	}
	if !sawDisconnect {
		t.Fatal("physical disconnect did not surface reconnecting status")
	}
	if store.daemonReady.Load() {
		t.Fatal("store became ready before the authoritative synchronization was installed")
	}
	if err := store.applyDaemonSynchronization(secondSync.Snapshot); err != nil {
		secondSync.complete(err)
		t.Fatal(err)
	}
	secondSync.complete(nil)
	assertClipboard("after reconnect")
	after, ok := store.Session(state.TabID)
	if !ok || after.terminalID != before.terminalID {
		t.Fatalf("session after reconnect = %#v, want terminal ID %q", after, before.terminalID)
	}
	if !store.daemonReady.Load() {
		t.Fatal("store did not return to ready after authoritative synchronization")
	}
}
