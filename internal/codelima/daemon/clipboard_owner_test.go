package daemon

import (
	"context"
	"encoding/json"
	"testing"
)

func TestClipboardEffectTargetsOnlyCurrentPhysicalInputOwner(t *testing.T) {
	server := NewServer(Config{})
	for _, id := range []string{"old-owner", "owner", "observer"} {
		client := &clientConn{id: id, clientInstanceID: id, outbound: newOutboundQueue(8, MaxMessageSize)}
		client.subscribed.Store(true)
		server.clients[id] = client
	}
	server.clients["old-owner"].clientInstanceID = "seat"
	server.clients["old-owner"].connectionID = 1
	server.clients["owner"].clientInstanceID = "seat"
	server.clients["owner"].connectionID = 2
	server.clients["observer"].connectionID = 3
	server.input = inputLease{clientInstanceID: "seat", connectionID: 2}
	server.identity.Token = "epoch"
	server.revision = 9
	if !server.EmitInputOwner(EventTerminalClipboard, TerminalClipboardEvent{TerminalID: "terminal", Text: "private clipboard"}) {
		t.Fatal("clipboard was not admitted to current owner")
	}
	for _, id := range []string{"old-owner", "observer"} {
		if depth, _, _ := server.clients[id].outbound.Stats(); depth != 0 {
			t.Fatalf("clipboard leaked to %s", id)
		}
	}
	raw, err := server.clients["owner"].outbound.Pop(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var event Event
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatal(err)
	}
	if event.Event != EventTerminalClipboard || event.StateSequence != 0 || event.DaemonEpoch != "epoch" || server.revision != 9 {
		t.Fatalf("private effect consumed shared sequence: %+v revision=%d", event, server.revision)
	}
	server.input = inputLease{}
	if server.EmitInputOwner(EventTerminalClipboard, TerminalClipboardEvent{Text: "unowned"}) {
		t.Fatal("clipboard emitted without owner")
	}
	server.input = inputLease{clientInstanceID: "seat", connectionID: 2}
	server.clients["owner"].subscribed.Store(false)
	if server.EmitInputOwner(EventTerminalClipboard, TerminalClipboardEvent{Text: "unsubscribed"}) {
		t.Fatal("clipboard queued for a non-frontend owner")
	}
}
