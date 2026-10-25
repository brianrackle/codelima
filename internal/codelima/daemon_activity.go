package codelima

import (
	"encoding/json"
	"time"
)

type activityMessageAck struct {
	Message ActivityMessage `json:"message"`
	Epoch   string          `json:"epoch"`
}

func (h *daemonHost) activityJournal() (*activityJournal, error) {
	h.activityMu.Lock()
	defer h.activityMu.Unlock()
	// Opening can fail transiently during an outage. Bound repeated directory
	// scans, but do not cache that failure for the daemon's entire lifetime.
	if h.activity == nil && (h.activityErr == nil || !time.Now().Before(h.activityRetryAt)) {
		h.activity, h.activityErr = newActivityJournal(h.service)
		h.activityRetryAt = time.Now().Add(time.Second)
	}
	return h.activity, h.activityErr
}

// invalidateActivityJournal runs under the exclusive update barrier, after the
// successor has stopped. The next access reloads disk under recovered daemon
// ownership and gets a new epoch, including any successor-acknowledged writes.
func (h *daemonHost) invalidateActivityJournal() {
	h.activityMu.Lock()
	defer h.activityMu.Unlock()
	h.activity, h.activityErr = nil, nil
	h.activityRetryAt = time.Time{}
}

func (h *daemonHost) handleActivity(method string, raw json.RawMessage) (any, error) {
	// Initialization and retention can write even on a list request. Serialize
	// all journal access against a daemon replacement's single-writer boundary.
	if err := h.enterTerminalMutation(); err != nil {
		return nil, err
	}
	defer h.leaveTerminalMutation()
	if method == "activity.append" {
		var request activityMessageRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, toDaemonError(invalidArgument("invalid activity message", nil))
		}
		journal, err := h.activityJournal()
		if err != nil {
			return nil, toDaemonError(err)
		}
		record, err := journal.Append(request)
		return activityMessageAck{Message: record, Epoch: journal.epoch}, toDaemonError(err)
	}
	var params struct {
		Before string `json:"before"`
		Limit  int    `json:"limit"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, toDaemonError(invalidArgument("invalid activity page", nil))
		}
	}
	operations, err := h.nodeOperations()
	if err != nil {
		return nil, toDaemonError(err)
	}
	operations.mu.Lock()
	err = operations.pruneLocked()
	operations.mu.Unlock()
	if err != nil {
		return nil, toDaemonError(err)
	}
	journal, journalErr := h.activityJournal()
	if journalErr == nil {
		journalErr = journal.Prune()
	}
	var messages []ActivityMessage
	var through uint64
	if journalErr == nil {
		messages, through = journal.Snapshot()
	}
	page, err := buildActivityPage(operations.List(), messages, params.Before, params.Limit)
	if IsNotFound(err) {
		// Expiry or an unavailable journal can remove an older-page cursor.
		// Preserve the fresh running projection while asking for a reload.
		page.HistoryError = err.Error()
	} else if err != nil {
		return nil, toDaemonError(err)
	}
	if journalErr != nil {
		page.MessageError = boundedOperationText(plainLogText(journalErr.Error()), 4096)
	} else {
		page.Epoch, page.MessageThrough = journal.epoch, through
	}
	return page, nil
}
