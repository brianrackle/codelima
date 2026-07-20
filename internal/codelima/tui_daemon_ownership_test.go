package codelima

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"git.sr.ht/~rockorager/vaxis"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
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

func TestTUIWindowFocusReclaimsInputFromNewerWindow(t *testing.T) {
	t.Parallel()

	root := filepath.Clean(filepath.Join("..", "..", "tmp"))
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	home, err := os.MkdirTemp(root, "tui-focus-owner-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })

	server := daemon.NewServer(daemon.Config{Home: home, Version: Version, Handler: tuiInputOwnershipTestHandler{}})
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

	firstService := NewService(DefaultConfig(home), newFakeSandbox(), nil, ioDiscard{}, ioDiscard{})
	firstService.cfg.Daemon.Autostart = false
	firstClient, err := firstService.connectTUIDaemon(context.Background())
	if err != nil {
		t.Fatalf("connect first TUI: %v", err)
	}
	defer func() { _ = firstClient.Close() }()
	firstService.daemonClient = firstClient

	secondService := NewService(DefaultConfig(home), newFakeSandbox(), nil, ioDiscard{}, ioDiscard{})
	secondService.cfg.Daemon.Autostart = false
	secondClient, err := secondService.connectTUIDaemon(context.Background())
	if err != nil {
		t.Fatalf("connect second TUI: %v", err)
	}
	defer func() { _ = secondClient.Close() }()
	secondService.daemonClient = secondClient

	if err := firstClient.Call(context.Background(), "terminal.open", map[string]string{"target": "node:first-before-focus"}, nil); err == nil {
		t.Fatal("expected first TUI to be observe-only after second TUI connected")
	}

	app := &vaxisTUIApp{ctx: context.Background(), service: firstService}
	if quit, focusErr := app.handleEvent(vaxis.FocusIn{}); focusErr != nil || quit {
		t.Fatalf("handleEvent(FocusIn) = (%v, %v), want (false, nil)", quit, focusErr)
	}
	if err := firstClient.Call(context.Background(), "terminal.open", map[string]string{"target": "node:first-after-focus"}, nil); err != nil {
		t.Fatalf("first TUI terminal.open after focus = %v", err)
	}
	if err := secondClient.Call(context.Background(), "terminal.open", map[string]string{"target": "node:second-after-focus"}, nil); err == nil {
		t.Fatal("expected second TUI to become observe-only after first TUI regained focus")
	}

	secondApp := &vaxisTUIApp{ctx: context.Background(), service: secondService}
	if quit, focusErr := secondApp.handleEvent(vaxis.FocusIn{}); focusErr != nil || quit {
		t.Fatalf("second handleEvent(FocusIn) = (%v, %v), want (false, nil)", quit, focusErr)
	}
	if err := secondClient.Call(context.Background(), "terminal.open", map[string]string{"target": "node:second-after-refocus"}, nil); err != nil {
		t.Fatalf("second TUI terminal.open after refocus = %v", err)
	}
	if err := firstClient.Call(context.Background(), "terminal.open", map[string]string{"target": "node:first-after-second-refocus"}, nil); err == nil {
		t.Fatal("expected first TUI to become observe-only after second TUI regained focus")
	}

	if quit, focusErr := app.handleEvent(vaxis.FocusIn{}); focusErr != nil || quit {
		t.Fatalf("repeat handleEvent(FocusIn) = (%v, %v), want (false, nil)", quit, focusErr)
	}
	if err := firstClient.Call(context.Background(), "terminal.open", map[string]string{"target": "node:first-after-repeat-focus"}, nil); err != nil {
		t.Fatalf("first TUI terminal.open after repeat focus = %v", err)
	}
}

type tuiInputOwnershipTestHandler struct{}

func (tuiInputOwnershipTestHandler) Handle(context.Context, daemon.ClientContext, string, json.RawMessage) (any, error) {
	return map[string]bool{"ok": true}, nil
}

func (tuiInputOwnershipTestHandler) Snapshot(context.Context) (any, error) { return nil, nil }
func (tuiInputOwnershipTestHandler) TerminalCount() int                    { return 0 }
func (tuiInputOwnershipTestHandler) Close() error                          { return nil }
