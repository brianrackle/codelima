package codelima

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"github.com/brianrackle/codelima/internal/codelima/terminal"
	"go.rockorager.dev/vaxis"
)

type shellChooserRPCHandler struct {
	tuiInputOwnershipTestHandler
	sessions []daemon.TerminalState
	opened   chan terminalOpenParams
}

func nextShellChooserEvent[T any](t *testing.T, events <-chan vaxis.Event) T {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-events:
			if wanted, ok := event.(T); ok {
				return wanted
			}
		case <-timer.C:
			t.Fatal("chooser operation did not complete")
			var zero T
			return zero
		}
	}
}

func (h shellChooserRPCHandler) Handle(_ context.Context, _ daemon.ClientContext, method string, raw json.RawMessage) (any, error) {
	if method == "terminal.list" {
		return h.sessions, nil
	}
	if method == "terminal.open" {
		var params terminalOpenParams
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		h.opened <- params
		return daemon.TerminalState{TerminalID: "new-choice", TabID: params.Target + "#new", Target: params.Target, Kind: params.Kind}, nil
	}
	return nil, nil
}

func TestTUIManagerShellChooserSavedDefaultRetainsNodeAndCreatesFreshSession(t *testing.T) {
	for _, removed := range []bool{false, true} {
		t.Run(fmt.Sprintf("removed=%v", removed), func(t *testing.T) {
			a, _ := newTestManager(t, 80, 24)
			wanted, other := a.state.nodes[0], a.state.nodes[1]
			a.service, _ = newTestService(t)
			cfg, err := a.service.SettingsUpdate(context.Background(), "shell.mode", "window")
			if err != nil {
				t.Fatal(err)
			}
			a.preferences = &cfg
			handler := shellChooserRPCHandler{opened: make(chan terminalOpenParams, 4), sessions: []daemon.TerminalState{
				{TerminalID: "existing", Target: nodeTargetKey(wanted.ID), Kind: terminal.NodeShell.String()},
			}}
			home := newDaemonTestRoot(t, "chooser-")
			server := daemon.NewServer(daemon.Config{Home: home, Version: Version, Handler: handler})
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- server.Run(ctx) }()
			t.Cleanup(func() { cancel(); <-done })
			waitForDaemonPing(t, home)
			client, err := daemonclient.Dial(ctx, daemonclient.Options{Home: home, Version: Version})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = client.Close() }()
			a.service = a.service.withIO(io.Discard, io.Discard)
			a.service.daemonClient = client
			events := make(chan vaxis.Event, 8)
			a.postEvent = func(event vaxis.Event) { events <- event }
			a.openShellChooser()
			h := a.overlay.(*tuiShellChooser)
			if _, err := h.Update(vaxis.Key{Keycode: vaxis.KeyEnter, Modifiers: vaxis.ModShift}); err != nil || h.err != "" {
				t.Fatal(err, h.err)
			}
			a.fleet.selectID(other.ID)
			a.selectFleetNode()
			completion := nextShellChooserEvent[tuiOperationCompleteEvent](t, events)
			if completion.Err != nil {
				t.Fatal(completion.Err)
			}
			if removed {
				a.state.nodes = slices.DeleteFunc(a.state.nodes, func(n Node) bool { return n.ID == wanted.ID })
				if err := completion.Result.Apply(a); err == nil {
					t.Fatal("removed node accepted the deferred launch")
				}
				if a.rawShellPending {
					t.Fatal("removed node started a shell request")
				}
				select {
				case params := <-handler.opened:
					t.Fatal("removed node opened a shell", params)
				default:
				}
				return
			}
			if err := completion.Result.Apply(a); err != nil {
				t.Fatal(err)
			}
			select {
			case params := <-handler.opened:
				if params.Target != nodeTargetKey(wanted.ID) || params.Kind != terminal.NodeShell.String() {
					t.Fatal("asynchronous save redirected the fresh shell", params)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("New Session reused the existing shell")
			}
			ready := nextShellChooserEvent[tuiRawShellReadyEvent](t, events)
			if ready.Err != nil || ready.State.Target != nodeTargetKey(wanted.ID) {
				t.Fatalf("shell completion = %#v", ready)
			}
			stored, err := a.service.SettingsRead()
			if err != nil || stored.Shell.Mode != "attach" || a.state.selectedEntry().node.ID != other.ID {
				t.Fatal("default was not persisted or launch changed navigation", stored.Shell.Mode, err)
			}
			// Existing-session requests must never create a replacement after closure.
			if _, err := resolveRawShell(ctx, client, wanted, false, false, "ended"); err == nil {
				t.Fatal("ended session was replaced")
			}
			select {
			case params := <-handler.opened:
				t.Fatal("ended attachment created a session", params)
			default:
			}
		})
	}
}
