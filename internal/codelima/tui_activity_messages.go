package codelima

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

type tuiNativeActivityEvent activityMessageAck

func (a *vaxisTUIApp) applyNativeActivity(event tuiNativeActivityEvent) {
	message := event.Message
	if event.Epoch == "" || message.Sequence == 0 || message.Time.IsZero() {
		return
	}
	if _, err := normalizeActivityMessage(message.activityMessageRequest); err != nil {
		return
	}
	s := &a.activity
	if s.nativeEpoch == event.Epoch && message.Sequence <= s.nativeThrough {
		return
	}
	s.nativeEpoch, s.nativeThrough = event.Epoch, message.Sequence
	if message.Level >= slog.LevelWarn {
		// This record is already durable. A footer notice must never append it
		// again through the frontend's local message outbox.
		text := message.Text
		if message.NodeSlug != "" {
			text = message.NodeSlug + ": " + text
		}
		a.setFooterStatus(message.Level, text)
	}
	a.startActivityLoad(true)
}

type tuiActivityDelivery struct {
	serial     uint64
	inFlight   bool
	deadline   time.Time
	retryAfter time.Time
	cancel     context.CancelFunc
	err        error
}

type tuiActivityMessageEvent struct {
	serial uint64
	id     string
	ack    activityMessageAck
	err    error
}

// The bounded message ring is also the pending queue. Only the event loop
// owns it; one writer uses immutable request data, then posts its ack. A lost
// completion retries the same ID, never a second message. Quitting before an
// ack can lose a local message and the UI explicitly labels that distinction.
func (a *vaxisTUIApp) startActivityMessageDelivery() {
	d := &a.messageDelivery
	if d.inFlight && time.Now().After(d.deadline) {
		d.cancel()
		d.serial++
		d.inFlight = false
	}
	if d.inFlight || time.Now().Before(d.retryAfter) || a.service == nil || a.service.daemonClient == nil || a.daemonDisconnected {
		return
	}
	var pending tuiMessage
	for _, entry := range a.messages.Entries() {
		if entry.ack.Epoch == "" && strings.TrimSpace(plainLogText(entry.Text)) != "" {
			pending = entry
			break
		}
	}
	if pending.ID == "" {
		return
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	d.serial++
	d.cancel, d.inFlight, d.deadline = cancel, true, time.Now().Add(6*time.Second)
	serial, client := d.serial, a.service.daemonClient
	request := activityMessageRequest{ID: pending.ID, Source: "tui", Level: pending.Level, Text: boundedOperationText(plainLogText(pending.Text), 4000)}
	deliver := func() tuiActivityMessageEvent {
		defer cancel()
		var ack activityMessageAck
		err := client.Call(ctx, "activity.append", request, &ack)
		if err == nil && (ack.Message.ID != request.ID || ack.Epoch == "" || ack.Message.Sequence == 0) {
			err = fmt.Errorf("activity persistence acknowledgement is invalid")
		}
		return tuiActivityMessageEvent{serial: serial, id: request.ID, ack: ack, err: err}
	}
	if a.postEvent == nil {
		a.applyActivityMessage(deliver())
		return
	}
	go func() { a.postCompletion(deliver()) }()
}

func (a *vaxisTUIApp) applyActivityMessage(event tuiActivityMessageEvent) {
	d := &a.messageDelivery
	if event.serial != d.serial {
		return
	}
	d.inFlight, d.err = false, event.err
	if event.err != nil {
		// Do not report a failed journal write back through the journal.
		d.retryAfter = time.Now().Add(10 * time.Second)
		return
	}
	d.retryAfter = time.Time{}
	if a.messages != nil && event.ack.Message.ID == event.id && event.ack.Epoch != "" {
		for i := range a.messages.entries {
			if a.messages.entries[i].ID == event.id {
				a.messages.entries[i].ack = event.ack
				break
			}
		}
	}
	a.startActivityMessageDelivery()
}
