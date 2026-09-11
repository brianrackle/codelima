//go:build integration

package tests

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima"
	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
)

func TestDaemonOSC52ClipboardReachesOwningFrontend(t *testing.T) {
	h := newHarness(t)
	nodeID := h.createNode()
	h.run(true, "daemon", "start")
	var terminal daemon.TerminalState
	if err := json.Unmarshal(h.json("terminal", "open", "node:"+nodeID, "--kind", "node-host-shell"), &terminal); err != nil {
		t.Fatal(err)
	}
	request, err := daemonclient.Dial(context.Background(), daemonclient.Options{
		Home: h.home, Version: codelima.Version, WantInput: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = request.Close() })
	if !request.HelloSnapshot().InputOwner {
		t.Fatal("frontend request connection did not acquire input")
	}
	// This host shell exercises the same PTY, native renderer and daemon path
	// as guest output, without requiring Lima or touching a desktop clipboard.
	for _, text := range []string{"clipboard before reconnect 界", "clipboard after reconnect 界"} {
		func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			events, err := daemonclient.Dial(ctx, daemonclient.Options{
				Home: h.home, Version: codelima.Version, Events: true,
				ClientInstanceID: request.HelloSnapshot().ClientID,
			})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = events.Close() }()
			sync, err := events.SubscribeSync(ctx, []string{"terminal"})
			if err != nil {
				t.Fatal(err)
			}
			if events.HelloSnapshot().ConnectionID == request.HelloSnapshot().ConnectionID {
				t.Fatal("test requires separate request and event connections")
			}
			encoded := base64.StdEncoding.EncodeToString([]byte(text))
			command := fmt.Sprintf("printf '\\033]52;c;%%s\\007' '%s'\r", encoded)
			h.run(true, "terminal", "send", terminal.TerminalID, "--text", command)
			for {
				event, err := events.NextEvent(ctx)
				if err != nil {
					t.Fatalf("waiting for OSC 52 clipboard %q: %v", text, err)
				}
				if event.Event != daemon.EventTerminalClipboard {
					continue
				}
				clip, ok := daemon.DecodeEventData[daemon.TerminalClipboardEvent](event.Data)
				if !ok || clip.Text != text || clip.TerminalID != terminal.TerminalID || clip.TabID != terminal.TabID {
					t.Fatalf("clipboard event = %#v, want %q from %q", clip, text, terminal.TerminalID)
				}
				if event.DaemonEpoch != sync.DaemonEpoch || event.StateSequence != 0 {
					t.Fatalf("clipboard was not an ephemeral effect in the current epoch: %#v", event)
				}
				break
			}
		}()
	}
	h.run(true, "terminal", "close", terminal.TerminalID)
}
