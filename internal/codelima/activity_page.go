package codelima

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

// ActivityRecord is a projection of either a canonical lifecycle receipt or
// a committed message. Request rows and completion rows have distinct IDs;
// completion retains the running row's ID so selection follows the receipt.
type ActivityRecord struct {
	Action      activityAction `json:"action,omitempty"`
	TerminalID  string         `json:"terminal_id,omitempty"`
	ID          string         `json:"id"`
	OperationID string         `json:"operation_id,omitempty"`
	NodeID      string         `json:"node_id,omitempty"`
	NodeSlug    string         `json:"node_slug,omitempty"`
	Source      string         `json:"source,omitempty"`
	Time        time.Time      `json:"time"`
	Level       slog.Level     `json:"level"`
	Text        string         `json:"text"`
	Success     bool           `json:"success,omitempty"`
}

type ActivityPage struct {
	Running        []NodeOperation  `json:"running"`
	History        []ActivityRecord `json:"history"`
	Next           string           `json:"next,omitempty"`
	Epoch          string           `json:"epoch,omitempty"`
	MessageThrough uint64           `json:"message_through"`
	MessageError   string           `json:"message_error,omitempty"`
	HistoryError   string           `json:"history_error,omitempty"`
}

func activityOperationRecord(op NodeOperation, requested bool) ActivityRecord {
	kind := op.Request.Kind
	if (kind == "create" || kind == "clone") && op.Request.startsNode() {
		kind += " + start"
	}
	r := ActivityRecord{ID: op.ID, OperationID: op.ID, NodeID: op.NodeID, NodeSlug: op.NodeSlug, Source: op.Request.Origin, Time: nodeOperationHistoryTime(op), Text: kind + " · " + op.State, Success: op.State == "succeeded"}
	if op.Batch != nil {
		kind = fmt.Sprintf("batch · %d nodes", op.Batch.Total)
		r.Text, r.Action = kind+" · "+op.State+" · "+op.Batch.summary(), activityOpenOperation
	}
	if op.EnvironmentTest != nil {
		kind = "test " + boundedOperationText(op.EnvironmentTest.EnvironmentSlug, 256)
		r.Text, r.Action = kind+" · "+op.State, activityOpenOperation
	}
	if requested {
		r.ID += ":requested"
		r.Time, r.Text, r.Success = op.CreatedAt, kind+" requested", false
	} else {
		switch op.State {
		case "failed":
			r.Level = slog.LevelError
		case "canceled", "canceling", "interrupted":
			r.Level = slog.LevelWarn
		}
		if op.Error != "" {
			r.Text += " · " + op.Error
		}
		if op.FinishedAt != nil && !op.CreatedAt.IsZero() && !op.FinishedAt.Before(op.CreatedAt) {
			r.Text += " · " + op.FinishedAt.Sub(op.CreatedAt).Truncate(time.Second).String()
		}
	}
	r.Text = plainLogText(r.Text)
	return r
}

func activityMessageRecord(message ActivityMessage) ActivityRecord {
	return ActivityRecord{ID: "message:" + message.ID, NodeID: message.NodeID, NodeSlug: message.NodeSlug, Source: message.Source, Time: message.Time, Level: message.Level, Text: message.Text, Action: message.Action, TerminalID: message.TerminalID, Success: message.Success}
}

func buildActivityPage(operations []NodeOperation, messages []ActivityMessage, before string, limit int) (ActivityPage, error) {
	if limit < 0 || limit > 100 {
		return ActivityPage{}, invalidArgument("activity page limit must be between 1 and 100", nil)
	}
	if limit == 0 {
		limit = 50
	}
	var page ActivityPage
	history := make([]ActivityRecord, 0, len(operations)*2+len(messages))
	for _, op := range operations {
		history = append(history, activityOperationRecord(op, true))
		if op.State == "running" || op.State == "canceling" {
			page.Running = append(page.Running, op)
		} else {
			history = append(history, activityOperationRecord(op, false))
		}
	}
	for _, message := range messages {
		history = append(history, activityMessageRecord(message))
	}
	slices.SortFunc(history, func(a, b ActivityRecord) int {
		if order := b.Time.Compare(a.Time); order != 0 {
			return order
		}
		return strings.Compare(b.ID, a.ID)
	})
	start := 0
	if before != "" {
		index := slices.IndexFunc(history, func(r ActivityRecord) bool { return r.ID == before })
		if index < 0 {
			return page, notFound("activity page cursor expired; reload history", nil)
		}
		start = index + 1
	}
	end := min(len(history), start+limit)
	page.History = history[start:end]
	for {
		if end < len(history) && end > start {
			page.Next = history[end-1].ID
		}
		data, err := json.Marshal(page)
		if err != nil {
			return page, err
		}
		if len(data) < daemon.MaxMessageSize/2-8192 {
			return page, nil
		}
		if len(page.History) <= 1 {
			return ActivityPage{}, dependencyUnavailable("running activity exceeds the response limit", nil, nil)
		}
		end--
		page.History = history[start:end]
	}
}
