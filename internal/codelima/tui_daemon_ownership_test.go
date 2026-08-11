package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	// The seat gate covers replaceable state only, so the probe for "who
	// holds the seat" is terminal.resize; the previous holder's control and
	// input requests keep dispatching (ADR 130).
	if err := existing.Call(context.Background(), "terminal.resize", seatProbeParams("old-owner"), nil); err == nil {
		t.Fatal("expected the previous client to lose the seat after TUI takeover")
	}
	if err := existing.Call(context.Background(), "terminal.open", map[string]string{"target": "node:old-owner"}, nil); err != nil {
		t.Fatalf("control from the previous client after TUI takeover = %v, want dispatch", err)
	}
	time.Sleep(3 * readTimeout)
	if err := tuiClient.Call(context.Background(), "terminal.resize", seatProbeParams("test"), nil); err != nil {
		t.Fatalf("terminal.resize after TUI takeover and idle interval = %v", err)
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

	if err := firstClient.Call(context.Background(), "terminal.resize", seatProbeParams("first-before-focus"), nil); err == nil {
		t.Fatal("expected first TUI to lose the seat after second TUI connected")
	}

	app := &vaxisTUIApp{ctx: context.Background(), service: firstService}
	if quit, focusErr := app.handleEvent(vaxis.FocusIn{}); focusErr != nil || quit {
		t.Fatalf("handleEvent(FocusIn) = (%v, %v), want (false, nil)", quit, focusErr)
	}
	if err := firstClient.Call(context.Background(), "terminal.resize", seatProbeParams("first-after-focus"), nil); err != nil {
		t.Fatalf("first TUI terminal.resize after focus = %v", err)
	}
	if err := secondClient.Call(context.Background(), "terminal.resize", seatProbeParams("second-after-focus"), nil); err == nil {
		t.Fatal("expected second TUI to lose the seat after first TUI regained focus")
	}

	secondApp := &vaxisTUIApp{ctx: context.Background(), service: secondService}
	if quit, focusErr := secondApp.handleEvent(vaxis.FocusIn{}); focusErr != nil || quit {
		t.Fatalf("second handleEvent(FocusIn) = (%v, %v), want (false, nil)", quit, focusErr)
	}
	if err := secondClient.Call(context.Background(), "terminal.resize", seatProbeParams("second-after-refocus"), nil); err != nil {
		t.Fatalf("second TUI terminal.resize after refocus = %v", err)
	}
	if err := firstClient.Call(context.Background(), "terminal.resize", seatProbeParams("first-after-second-refocus"), nil); err == nil {
		t.Fatal("expected first TUI to lose the seat after second TUI regained focus")
	}

	if quit, focusErr := app.handleEvent(vaxis.FocusIn{}); focusErr != nil || quit {
		t.Fatalf("repeat handleEvent(FocusIn) = (%v, %v), want (false, nil)", quit, focusErr)
	}
	if err := firstClient.Call(context.Background(), "terminal.resize", seatProbeParams("first-after-repeat-focus"), nil); err != nil {
		t.Fatalf("first TUI terminal.resize after repeat focus = %v", err)
	}
}

// seatProbeParams builds terminal.resize params for the seat probes above. The
// stub handler accepts any terminal id, so the id doubles as the probe label.
func seatProbeParams(label string) map[string]any {
	return map[string]any{"terminal_id": label, "cols": 80, "rows": 24}
}

func TestTUIWindowFocusPreservesDaemonDisconnectGuidance(t *testing.T) {
	t.Parallel()

	client := &daemonclient.Client{Hello: daemon.HelloResult{ClientID: "disconnected-client"}}
	app := &vaxisTUIApp{
		ctx:      context.Background(),
		service:  &Service{daemonClient: client},
		messages: newTUIMessageLog(10),
	}
	disconnectErr := errors.New("daemon connection lost; reconnecting")

	if quit, err := app.handleEvent(tuiDaemonDisconnectedEvent{Err: disconnectErr}); err != nil || quit {
		t.Fatalf("handleEvent(tuiDaemonDisconnectedEvent) = (%v, %v), want (false, nil)", quit, err)
	}
	if quit, err := app.handleEvent(vaxis.FocusIn{}); err != nil || quit {
		t.Fatalf("handleEvent(FocusIn) = (%v, %v), want (false, nil)", quit, err)
	}
	if quit, err := app.handleEvent(tuiTerminalErrorEvent{Err: errors.New("send terminal input: EOF")}); err != nil || quit {
		t.Fatalf("handleEvent(tuiTerminalErrorEvent) = (%v, %v), want (false, nil)", quit, err)
	}
	if app.status != disconnectErr.Error() {
		t.Fatalf("status after focus = %q, want disconnect guidance %q", app.status, disconnectErr)
	}
	if strings.Contains(app.status, "reclaim terminal input ownership") || strings.Contains(app.status, "broken pipe") {
		t.Fatalf("focus overwrote disconnect guidance with dead-socket error: %q", app.status)
	}
}

func TestTUIWindowFocusReportsReconnectAfterFailedDaemonRequest(t *testing.T) {
	t.Parallel()

	app := &vaxisTUIApp{
		ctx:      context.Background(),
		service:  &Service{daemonClient: &daemonclient.Client{}},
		messages: newTUIMessageLog(10),
	}

	if quit, err := app.handleEvent(vaxis.FocusIn{}); err != nil || quit {
		t.Fatalf("handleEvent(FocusIn) = (%v, %v), want (false, nil)", quit, err)
	}
	const want = "daemon connection lost; reconnecting"
	if app.status != want {
		t.Fatalf("status after failed takeover = %q, want %q", app.status, want)
	}
	if !app.daemonDisconnected {
		t.Fatal("failed takeover did not latch the daemon disconnect")
	}

	// A second focus event must not probe the known-dead request connection or
	// replace the one actionable recovery message.
	if quit, err := app.handleEvent(vaxis.FocusIn{}); err != nil || quit {
		t.Fatalf("second handleEvent(FocusIn) = (%v, %v), want (false, nil)", quit, err)
	}
	if app.status != want || app.messages.Len() != 1 {
		t.Fatalf("repeated focus status/messages = (%q, %d), want (%q, 1)", app.status, app.messages.Len(), want)
	}
}

type tuiInputOwnershipTestHandler struct{}

func (tuiInputOwnershipTestHandler) Handle(context.Context, daemon.ClientContext, string, json.RawMessage) (any, error) {
	return map[string]bool{"ok": true}, nil
}

func (tuiInputOwnershipTestHandler) Snapshot(context.Context) (any, error) { return nil, nil }
func (tuiInputOwnershipTestHandler) TerminalCount() int                    { return 0 }
func (tuiInputOwnershipTestHandler) Close() error                          { return nil }

// The seat moves only on local user signals. A background window's
// resynchronization — event-stream read timeout, sequence gap, epoch change —
// is not one, so it must leave the seat where the user put it; the same
// resynchronization from the focused window reclaims it (ADR 130). This pins
// the reported failure: an idle SSH-side TUI whose event stream hiccuped
// silently stole input from the window being typed in.
func TestResynchronizationReclaimsSeatOnlyWhenFocused(t *testing.T) {
	t.Parallel()

	root := filepath.Clean(filepath.Join("..", "..", "tmp"))
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	home, err := os.MkdirTemp(root, "tui-sync-seat-")
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

	// The background window connects first and holds the seat; the foreground
	// window then takes it with the explicit focus signal.
	backgroundService := NewService(DefaultConfig(home), newFakeSandbox(), nil, ioDiscard{}, ioDiscard{})
	backgroundService.cfg.Daemon.Autostart = false
	backgroundClient, err := backgroundService.connectTUIDaemon(context.Background())
	if err != nil {
		t.Fatalf("connect background TUI: %v", err)
	}
	defer func() { _ = backgroundClient.Close() }()
	backgroundService.daemonClient = backgroundClient

	foreground, err := daemonclient.Dial(context.Background(), daemonclient.Options{Home: home, Version: Version, WantInput: true})
	if err != nil {
		t.Fatalf("Dial(foreground) error = %v", err)
	}
	defer func() { _ = foreground.Close() }()
	if err := foreground.Call(context.Background(), "input.takeover", nil, nil); err != nil {
		t.Fatalf("foreground takeover error = %v", err)
	}

	store := &tuiSessionStore{
		service:          backgroundService,
		syncApplyTimeout: time.Second,
		postEvent: func(event vaxis.Event) {
			if sync, ok := event.(tuiDaemonSynchronizedEvent); ok {
				sync.complete(nil)
			}
		},
	}
	snapshot := daemon.SyncSnapshot{DaemonEpoch: backgroundClient.HelloSnapshot().DaemonEpoch}

	// Unfocused resynchronization: the zero-value focus flag stands in for a
	// window whose terminal delivered FocusOut. The seat must not move.
	if err := store.prepareDaemonSynchronization(context.Background(), snapshot); err != nil {
		t.Fatalf("unfocused prepareDaemonSynchronization error = %v", err)
	}
	if err := foreground.Call(context.Background(), "terminal.resize", seatProbeParams("foreground-keeps-seat"), nil); err != nil {
		t.Fatalf("foreground seat probe after background resync = %v, want the seat kept", err)
	}
	if err := backgroundClient.Call(context.Background(), "terminal.resize", seatProbeParams("background-unfocused"), nil); err == nil {
		t.Fatal("an unfocused resynchronization stole the seat")
	}

	// The same resynchronization from the focused window is a user signal and
	// reclaims the seat.
	store.setWindowFocused(true)
	if err := store.prepareDaemonSynchronization(context.Background(), snapshot); err != nil {
		t.Fatalf("focused prepareDaemonSynchronization error = %v", err)
	}
	if err := backgroundClient.Call(context.Background(), "terminal.resize", seatProbeParams("background-focused"), nil); err != nil {
		t.Fatalf("focused resynchronization did not reclaim the seat: %v", err)
	}
	if err := foreground.Call(context.Background(), "terminal.resize", seatProbeParams("foreground-after-reclaim"), nil); err == nil {
		t.Fatal("expected the foreground client to lose the seat after the focused reclaim")
	}

	// Holding the seat already, a repeat resynchronization issues no takeover
	// and the seat stays put.
	if err := store.prepareDaemonSynchronization(context.Background(), snapshot); err != nil {
		t.Fatalf("seat-holding prepareDaemonSynchronization error = %v", err)
	}
	if err := backgroundClient.Call(context.Background(), "terminal.resize", seatProbeParams("background-still-seated"), nil); err != nil {
		t.Fatalf("seat lost across an already-seated resynchronization: %v", err)
	}
}
