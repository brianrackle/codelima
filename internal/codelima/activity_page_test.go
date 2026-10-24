package codelima

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

func TestActivityPageMergesRequestsCompletionsAndMessagesWithStableCursors(t *testing.T) {
	now := time.Now().UTC()
	done := now.Add(-time.Minute)
	operations := []NodeOperation{
		{ID: "active", State: "running", CreatedAt: now, Request: NodeOperationRequest{Kind: "start"}},
		{ID: "done", State: "succeeded", CreatedAt: now.Add(-time.Hour), FinishedAt: &done, Request: NodeOperationRequest{Kind: "stop", Origin: "cli"}},
	}
	message := ActivityMessage{activityMessageRequest: activityMessageRequest{ID: newID(), Source: "clipboard", Text: "copied", Level: slog.LevelInfo}, Time: now.Add(-30 * time.Second), Sequence: 1}
	page, err := buildActivityPage(operations, []ActivityMessage{message}, "", 2)
	if err != nil || len(page.Running) != 1 || len(page.History) != 2 || page.History[0].ID != "active:requested" || page.History[1].ID != "message:"+message.ID || page.Next != page.History[1].ID {
		t.Fatal("first merged page", page, err)
	}
	page, err = buildActivityPage(operations, []ActivityMessage{message}, page.Next, 2)
	if err != nil || len(page.Running) != 1 || len(page.History) != 2 || page.History[0].ID != "done" || !page.History[0].Success || page.History[1].ID != "done:requested" || page.History[1].Source != "cli" || page.Next != "" {
		t.Fatal("older page lost running work or request provenance", page, err)
	}
	if _, err := buildActivityPage(operations, nil, "expired", 2); !IsNotFound(err) {
		t.Fatal("expired cursor silently reset history", err)
	}
	if _, err := buildActivityPage(nil, nil, "", 101); exitCodeForError(err) != ExitInvalidArgument {
		t.Fatal("unbounded page accepted", err)
	}
}

func TestActivityPageDaemonAdmissionAndDegradedHistory(t *testing.T) {
	s, _ := newTestService(t)
	host := newDaemonHost(s)
	t.Cleanup(func() { _ = host.Close() })
	request := activityMessageRequest{ID: newID(), Source: "tui", Text: "shared warning", Level: slog.LevelWarn}
	raw, _ := json.Marshal(request)
	result, err := host.Handle(context.Background(), daemon.ClientContext{}, "activity.append", raw)
	if err != nil {
		t.Fatal(err)
	}
	ack := result.(activityMessageAck)
	if ack.Message.ID != request.ID || ack.Epoch == "" {
		t.Fatal("missing persistence acknowledgement", ack)
	}
	result, err = host.Handle(context.Background(), daemon.ClientContext{}, "activity.list", nil)
	if err != nil {
		t.Fatal(err)
	}
	page := result.(ActivityPage)
	if len(page.History) != 1 || page.MessageThrough != ack.Message.Sequence || page.Epoch != ack.Epoch {
		t.Fatal("ack and snapshot do not share a watermark", page)
	}
	if daemon.ClassifyMethod("activity.append") != daemon.ClassControl || daemon.SeatArbitratedMethod("activity.append") || daemon.ClassifyMethod("activity.list") != daemon.ClassQuery {
		t.Fatal("journal writes need a control lane independent of terminal input ownership")
	}
	host.replaced.Store(true)
	if _, err := host.Handle(context.Background(), daemon.ClientContext{}, "activity.append", raw); err == nil {
		t.Fatal("replaced daemon admitted a journal writer")
	}
	if err := os.WriteFile(filepath.Join(s.cfg.MetadataRoot, "_activity", request.ID+".json"), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	restarted := newDaemonHost(s)
	t.Cleanup(func() { _ = restarted.Close() })
	operations, err := restarted.nodeOperations()
	if err != nil {
		t.Fatal(err)
	}
	operations.records["active"] = &runningNodeOperation{record: NodeOperation{ID: "active", State: "running", CreatedAt: time.Now()}}
	result, err = restarted.Handle(context.Background(), daemon.ClientContext{}, "activity.list", nil)
	if err != nil || result.(ActivityPage).MessageError == "" || len(result.(ActivityPage).Running) != 1 {
		t.Fatal("journal corruption hid cancellation controls", result, err)
	}
	cursor, _ := json.Marshal(map[string]string{"before": "message:" + request.ID})
	result, err = restarted.Handle(context.Background(), daemon.ClientContext{}, "activity.list", cursor)
	if err != nil || result.(ActivityPage).HistoryError == "" || len(result.(ActivityPage).Running) != 1 {
		t.Fatal("unavailable older-page cursor hid running work", result, err)
	}
}

func TestActivityPageReceiptOriginIsNotAnIdempotencyInput(t *testing.T) {
	s, _ := newTestService(t)
	m := testNodeOperations(t, s)
	request := NodeOperationRequest{ID: newID(), Kind: "start", Node: newID(), Origin: "cli"}
	m.records[request.ID] = &runningNodeOperation{record: NodeOperation{ID: request.ID, Request: request, State: "running", CreatedAt: time.Now()}}
	request.Origin = "tui"
	record, found, err := m.repeatedRequestLocked(request)
	if err != nil || !found || record.Request.Origin != "cli" {
		t.Fatal("observer identity changed mutation idempotency or first provenance", record, found, err)
	}
}
