package codelima

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brianrackle/test_lima/internal/codelima/daemon"
	"github.com/brianrackle/test_lima/internal/codelima/daemonclient"
)

func TestConnectTUIDaemonTakesInputFromExistingClient(t *testing.T) {
	t.Parallel()

	root := filepath.Clean(filepath.Join("..", "..", "tmp"))
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	home, err := os.MkdirTemp(root, "tui-input-owner-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })

	const readTimeout = 25 * time.Millisecond
	server := daemon.NewServer(daemon.Config{Home: home, Version: Version, Handler: tuiInputOwnershipTestHandler{}, ReadTimeout: readTimeout})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if runErr := <-done; runErr != nil {
			t.Errorf("Server.Run() error = %v", runErr)
		}
	})
	waitForCondition(t, time.Second, func() bool {
		_, pingErr := daemonclient.Ping(context.Background(), home, Version)
		return pingErr == nil
	}, "daemon startup")

	existing, err := daemonclient.Dial(context.Background(), daemonclient.Options{Home: home, Version: Version, WantInput: true})
	if err != nil {
		t.Fatalf("Dial(existing owner) error = %v", err)
	}
	defer func() { _ = existing.Close() }()
	if !existing.Hello.InputOwner {
		t.Fatal("expected first client to own daemon input")
	}

	service := NewService(DefaultConfig(home), newFakeSandbox(), nil, ioDiscard{}, ioDiscard{})
	service.cfg.Daemon.Autostart = false
	tuiClient, err := service.connectTUIDaemon(context.Background())
	if err != nil {
		t.Fatalf("connectTUIDaemon() error = %v", err)
	}
	defer func() { _ = tuiClient.Close() }()
	if !tuiClient.Hello.InputOwner {
		t.Fatalf("expected TUI connection to take input ownership, hello = %#v", tuiClient.Hello)
	}
	if err := existing.Call(context.Background(), "terminal.open", map[string]string{"target": "node:old-owner"}, nil); err == nil {
		t.Fatal("expected the previous client to become observe-only after TUI takeover")
	}
	time.Sleep(3 * readTimeout)
	if err := tuiClient.Call(context.Background(), "terminal.open", map[string]string{"target": "node:test"}, nil); err != nil {
		t.Fatalf("terminal.open after TUI takeover and idle interval = %v", err)
	}
}

type tuiInputOwnershipTestHandler struct{}

func (tuiInputOwnershipTestHandler) Handle(context.Context, daemon.ClientContext, string, json.RawMessage) (any, error) {
	return map[string]bool{"ok": true}, nil
}

func (tuiInputOwnershipTestHandler) Snapshot(context.Context) (any, error) { return nil, nil }
func (tuiInputOwnershipTestHandler) TerminalCount() int                    { return 0 }
func (tuiInputOwnershipTestHandler) Close() error                          { return nil }
