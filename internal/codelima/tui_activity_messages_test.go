package codelima

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"go.rockorager.dev/vaxis"
)

func TestTUIActivityMessageAcknowledgementOverlaysUntilSnapshot(t *testing.T) {
	a := testActivityApp(t, 80, 24)
	a.activity.page = ActivityPage{Epoch: "epoch", MessageThrough: 3}
	a.pushMessage(slog.LevelWarn, "capacity warning")
	entry, _ := a.messages.Latest()
	_, rows := a.activityRows()
	if len(rows) != 1 || rows[0].node != "this window" {
		t.Fatal("uncommitted message presented as shared", rows)
	}
	a.messageDelivery.serial = 2
	ack := activityMessageAck{Epoch: "epoch", Message: ActivityMessage{activityMessageRequest: activityMessageRequest{ID: entry.ID, Source: "tui", Level: entry.Level, Text: entry.Text}, Sequence: 4, Time: entry.Time}}
	a.applyActivityMessage(tuiActivityMessageEvent{serial: 1, id: entry.ID, ack: ack})
	if a.messages.entries[0].ack.Epoch != "" {
		t.Fatal("late delivery changed message state")
	}
	a.applyActivityMessage(tuiActivityMessageEvent{serial: 2, id: entry.ID, ack: ack})
	_, rows = a.activityRows()
	if len(rows) != 1 || rows[0].node == "this window" || rows[0].id != "message:"+entry.ID {
		t.Fatal("acknowledged message disappeared before snapshot", rows)
	}
	a.activity.page.MessageThrough = 4
	a.activity.page.History = []ActivityRecord{activityMessageRecord(ack.Message)}
	_, rows = a.activityRows()
	if len(rows) != 1 {
		t.Fatal("ack and snapshot duplicated a message", rows)
	}
	// A snapshot may cover the ack even if fifty newer rows push it off the
	// current page. Absence from that page must not create a local duplicate.
	a.activity.page.History = nil
	_, rows = a.activityRows()
	if len(rows) != 0 {
		t.Fatal("old ack leaked onto a newer page", rows)
	}
	a.activity.page.Epoch = "new daemon"
	a.activity.page.MessageThrough = 0
	_, rows = a.activityRows()
	if len(rows) != 0 {
		t.Fatal("old epoch overrode the restarted daemon snapshot", rows)
	}
}

func TestTUIActivityTwoClientsShareMessagesAfterLostAcknowledgement(t *testing.T) {
	s, _ := newTestService(t)
	host := newDaemonHost(s)
	home := newDaemonTestRoot(t, "act-")
	server := daemon.NewServer(daemon.Config{Home: home, Version: Version, Handler: host})
	host.broadcast = server.Broadcast
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	t.Cleanup(func() { stop(); <-done; _ = host.Close() })
	waitForDaemonPing(t, home)
	connect := func() *daemonclient.Client {
		client, err := daemonclient.Dial(context.Background(), daemonclient.Options{Home: home, Version: Version})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Close() })
		return client
	}
	a := testActivityApp(t, 80, 24)
	a.service = s.withIO(nil, nil)
	a.service.daemonClient = connect()
	a.activity.page = ActivityPage{}
	events := make(chan vaxis.Event, 4)
	a.postEvent = func(event vaxis.Event) { events <- event }
	a.pushMessage(slog.LevelWarn, "shared from window A")
	receive := func() tuiActivityMessageEvent {
		select {
		case event := <-events:
			return event.(tuiActivityMessageEvent)
		case <-time.After(3 * time.Second):
			t.Fatal("message delivery did not complete")
			return tuiActivityMessageEvent{}
		}
	}
	old := receive()
	// Simulate an event queue losing the first ack after the daemon committed.
	a.messageDelivery.deadline = time.Now().Add(-time.Second)
	a.startActivityMessageDelivery()
	retried := receive()
	a.applyActivityMessage(old)
	if a.messages.entries[0].ack.Epoch != "" {
		t.Fatal("stale ack escaped its serial fence")
	}
	a.applyActivityMessage(retried)
	if retried.err != nil || old.ack != retried.ack {
		t.Fatal("retry duplicated or changed committed message", old, retried)
	}
	b := testActivityApp(t, 150, 38)
	b.service = s.withIO(nil, nil)
	b.service.daemonClient = connect()
	b.startActivityLoad(true)
	_, rows := b.activityRows()
	if len(rows) != 1 || rows[0].text != "shared from window A" || rows[0].node == "this window" {
		t.Fatal("independent observer did not see one shared message", rows, b.activity.err)
	}
}

func TestTUIActivityFailedDeliveryStaysLocalAndBounded(t *testing.T) {
	a := testActivityApp(t, 80, 24)
	a.activity.page = ActivityPage{}
	for range tuiMessageLogDefaultCap + 20 {
		a.pushMessage(slog.LevelError, "warning")
	}
	entry, _ := a.messages.Latest()
	a.messageDelivery.serial = 2
	a.applyActivityMessage(tuiActivityMessageEvent{serial: 2, id: entry.ID, err: errors.New("disconnected")})
	if a.messages.Len() != tuiMessageLogDefaultCap || a.messages.entries[len(a.messages.entries)-1].ack.Epoch != "" {
		t.Fatal("failed delivery grew the ring or claimed persistence")
	}
	_, rows := a.activityRows()
	if len(rows) != tuiMessageLogDefaultCap || rows[0].node != "this window" {
		t.Fatal("failed messages not retained as local")
	}
	a.activity.before = "older"
	_, rows = a.activityRows()
	if len(rows) != 0 {
		t.Fatal("local messages leaked across pages")
	}
}

func TestTUIActivityCanonicalOperationDoesNotRepublishOutput(t *testing.T) {
	a := testActivityApp(t, 80, 24)
	a.retainOperationMessages(&tuiOperationState{Title: "Starting", Lines: []string{"secret bootstrap output"}}, tuiOperationCompleteEvent{ReceiptID: "receipt", Err: errors.New("failed")})
	if a.messages.Len() != 0 {
		t.Fatal("canonical receipt duplicated output into general activity")
	}
}

func TestTUIActivityFooterExpiresWithoutLosingHistory(t *testing.T) {
	a := testActivityApp(t, 80, 24)
	a.setStatus(slog.LevelWarn, "temporary warning")
	a.statusAt = time.Now().Add(-7 * time.Second)
	a.view = tuiViewNodes
	a.draw()
	if strings.Contains(renderedScreenText(t, a.vx, 80, 24), "temporary warning") {
		t.Fatal("footer warning never expired")
	}
	if a.messages.Len() != 1 {
		t.Fatal("footer expiration discarded history")
	}
}

func TestTUIActivityReconnectReportsOneOutageRatherThanEveryRetry(t *testing.T) {
	a := &vaxisTUIApp{messages: newTUIMessageLog(200)}
	for range 5 {
		_, _ = a.handleEvent(tuiDaemonDisconnectedEvent{Err: errors.New("daemon unavailable")})
		a.finishDataRefresh(tuiRefreshCompleteEvent{Err: errors.New("daemon unavailable")})
	}
	if a.messages.Len() != 1 || !a.daemonDisconnected {
		t.Fatal("reconnect retries flooded shared history", a.messages.Len())
	}
	a.daemonDisconnected = false
	_, _ = a.handleEvent(tuiDaemonDisconnectedEvent{Err: errors.New("second outage")})
	if a.messages.Len() != 2 {
		t.Fatal("a later outage was hidden")
	}
}

func TestTUIActivityNativeWarningUsesFooterWithoutRepublishing(t *testing.T) {
	a := testActivityApp(t, 80, 24)
	message := activityMessageAck{Epoch: "journal", Message: ActivityMessage{activityMessageRequest: activityMessageRequest{ID: newID(), Source: "ports", NodeID: newID(), NodeSlug: "web", Level: slog.LevelWarn, Text: "route :3000 unavailable"}, Sequence: 2, Time: time.Now()}}
	store := &tuiSessionStore{postEvent: func(event vaxis.Event) { _, _ = a.handleEvent(event) }}
	store.handleDaemonEvent(daemon.Event{Event: daemon.EventActivityMessage, Data: message})
	if a.status != "web: "+message.Message.Text || a.statusLevel != slog.LevelWarn || a.messages.Len() != 0 {
		t.Fatal("native warning was lost or appended again by the frontend", a.status, a.messages.Entries())
	}
	stamp := time.Now().Add(-7 * time.Second)
	a.statusAt = stamp
	store.handleDaemonEvent(daemon.Event{Event: daemon.EventActivityMessage, Data: message})
	if !a.statusAt.Equal(stamp) {
		t.Fatal("duplicate native event extended the footer notice")
	}
	message.Message.Sequence++
	message.Message.Level, message.Message.Text = slog.LevelInfo, "route added"
	store.handleDaemonEvent(daemon.Event{Event: daemon.EventActivityMessage, Data: message})
	if !a.statusAt.Equal(stamp) {
		t.Fatal("routine native event replaced a warning")
	}
	message.Epoch, message.Message.Sequence, message.Message.Level = "recovered-journal", 1, slog.LevelWarn
	store.handleDaemonEvent(daemon.Event{Event: daemon.EventActivityMessage, Data: message})
	if a.statusAt.Equal(stamp) || a.messages.Len() != 0 {
		t.Fatal("journal epoch change lost the warning or republished it")
	}
}
