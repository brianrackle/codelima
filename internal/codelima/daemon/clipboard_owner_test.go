package daemon

import (
	"context"
	"encoding/json"
	"testing"
)

func TestClipboardEffectTargetsOnlyCurrentInputOwnersEventConnection(t *testing.T) {
	server := NewServer(Config{})
	addClient := func(id, instance string, connectionID uint64) *clientConn {
		client := &clientConn{id: id, clientInstanceID: instance, connectionID: connectionID, outbound: newOutboundQueue(8, MaxMessageSize)}
		server.clients[id] = client
		return client
	}
	subscribe := func(client *clientConn) {
		t.Helper()
		server.enqueueSyncLocked(client, 1, nil, 0)
		if _, err := client.outbound.Pop(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	addClient("request", "seat", 1) // The input connection never subscribes.
	oldEvents := addClient("old-events", "seat", 2)
	events := addClient("events", "seat", 3)
	observer := addClient("observer", "observer", 4)
	subscribe(oldEvents)
	subscribe(events)
	subscribe(observer)
	subscribe(oldEvents) // Late old subscriptions must not supersede the successor.
	server.input = inputLease{clientInstanceID: "seat", connectionID: 1}
	server.identity.Token = "epoch"
	server.revision = 9
	if !server.EmitInputOwner(EventTerminalClipboard, TerminalClipboardEvent{TerminalID: "terminal", Text: "private clipboard"}) {
		t.Fatal("clipboard was not admitted to current owner")
	}
	for _, id := range []string{"request", "old-events", "observer"} {
		if depth, _, _ := server.clients[id].outbound.Stats(); depth != 0 {
			t.Fatalf("clipboard leaked to %s", id)
		}
	}
	raw, err := events.outbound.Pop(context.Background())
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
	addClient("observer-request", "observer", 5)
	server.input = inputLease{clientInstanceID: "observer", connectionID: 5}
	if !server.EmitInputOwner(EventTerminalClipboard, TerminalClipboardEvent{Text: "after takeover"}) {
		t.Fatal("clipboard did not follow the new input owner")
	}
	if depth, _, _ := observer.outbound.Stats(); depth != 1 {
		t.Fatalf("new owner's clipboard queue depth = %d, want 1", depth)
	}
	for _, client := range []*clientConn{events, oldEvents} {
		if depth, _, _ := client.outbound.Stats(); depth != 0 {
			t.Fatal("clipboard leaked to the previous input owner")
		}
	}
	server.input = inputLease{}
	if server.EmitInputOwner(EventTerminalClipboard, TerminalClipboardEvent{Text: "unowned"}) {
		t.Fatal("clipboard emitted without owner")
	}
	server.input = inputLease{clientInstanceID: "seat", connectionID: 1}
	events.subscribed.Store(false)
	if server.EmitInputOwner(EventTerminalClipboard, TerminalClipboardEvent{Text: "unsubscribed"}) {
		t.Fatal("clipboard queued for a non-frontend owner")
	}
	events.subscribed.Store(true)
	server.removeClient(events.id)
	if server.EmitInputOwner(EventTerminalClipboard, TerminalClipboardEvent{Text: "disconnected"}) {
		t.Fatal("clipboard fell back to a superseded event connection")
	}
	subscribe(oldEvents)
	if server.EmitInputOwner(EventTerminalClipboard, TerminalClipboardEvent{Text: "resubscribed"}) {
		t.Fatal("late subscription revived a superseded event connection")
	}
	server.removeClient(oldEvents.id)
	server.removeClient("request")
	if _, retained := server.clipboardRecipients["seat"]; retained {
		t.Fatal("clipboard recipient retained after all frontend connections closed")
	}
}
