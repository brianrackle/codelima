//go:build darwin || linux

package codelima

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

const rawAttachmentReadBytes = 128 << 10

type rawAttachTerminal interface {
	AttachRaw(context.Context, daemon.ClientContext) (daemon.TerminalAttachResult, error)
	ReadRaw(string, daemon.TerminalAttachParams) (daemon.TerminalAttachResult, error)
	DetachRaw(string, daemon.TerminalAttachParams) error
	RawAttachmentDone(string) <-chan struct{}
}

type daemonRawAttachment struct {
	terminalID, owner string
	terminal          rawAttachTerminal
}

func (t *isolatedDaemonTerminal) AttachRaw(ctx context.Context, client daemon.ClientContext) (result daemon.TerminalAttachResult, err error) {
	t.mu.Lock()
	renderer, closed := t.renderer, t.closed
	t.mu.Unlock()
	if renderer == nil || closed {
		return result, errTerminalClosed
	}
	id, through := newID(), t.journal.Stats().LastID
	if err = t.rawAttachment.begin(client.ID, id, through); err != nil {
		return result, err
	}
	defer func() {
		if err != nil {
			t.rawAttachment.end(id)
		}
	}()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	retry := time.NewTicker(20 * time.Millisecond)
	defer retry.Stop()
	for {
		raw, callErr := renderer.call("attach_state", rendererCheckpointParams{Through: through}, time.Second)
		if callErr != nil {
			return result, callErr
		}
		var state rendererAttachState
		if err = json.Unmarshal(raw, &state); err != nil {
			return result, err
		}
		if state.Ready {
			if _, _, err = t.journal.readOutputAfter(state.Through, rawAttachmentReadBytes); err != nil {
				return result, err
			}
			if err = t.rawAttachment.commit(id, state.Through); err != nil {
				return result, err
			}
			result = daemon.TerminalAttachResult{AttachmentID: id, Data: state.Data, Through: state.Through}
			t.rawAttachment.mu.Lock()
			done := t.rawAttachment.done
			t.rawAttachment.mu.Unlock()
			go t.watchRawAttachment(client.Done, id, done)
			return result, nil
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-deadline.C:
			return result, errors.New("terminal is in an incomplete output sequence; retry attachment")
		case <-retry.C:
		}
	}
}

func (t *isolatedDaemonTerminal) watchRawAttachment(peerDone <-chan struct{}, id string, done <-chan struct{}) {
	defer t.rawAttachment.end(id)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-peerDone:
			return
		case <-done:
			return
		case now := <-ticker.C:
			if t.rawAttachment.expired(id, now) {
				return
			}
		}
	}
}

func (t *isolatedDaemonTerminal) RawAttachmentDone(id string) <-chan struct{} {
	t.rawAttachment.mu.Lock()
	defer t.rawAttachment.mu.Unlock()
	if t.rawAttachment.id == id {
		return t.rawAttachment.done
	}
	done := make(chan struct{})
	close(done)
	return done
}

func (t *isolatedDaemonTerminal) ReadRaw(owner string, params daemon.TerminalAttachParams) (daemon.TerminalAttachResult, error) {
	if err := t.rawAttachment.acknowledge(owner, params.AttachmentID, params.After); err != nil {
		return daemon.TerminalAttachResult{}, err
	}
	data, through, err := t.journal.readOutputAfter(params.After, rawAttachmentReadBytes)
	if err != nil {
		t.rawAttachment.end(params.AttachmentID)
		return daemon.TerminalAttachResult{}, err
	}
	if err := t.rawAttachment.delivered(owner, params.AttachmentID, through); err != nil {
		return daemon.TerminalAttachResult{}, err
	}
	t.mu.Lock()
	exited := t.closed
	t.mu.Unlock()
	return daemon.TerminalAttachResult{AttachmentID: params.AttachmentID, Data: data, Through: through, Exited: exited && through == t.journal.Stats().LastID}, nil
}

func (t *isolatedDaemonTerminal) DetachRaw(owner string, params daemon.TerminalAttachParams) error {
	if err := t.rawAttachment.acknowledge(owner, params.AttachmentID, params.After); err != nil {
		return err
	}
	t.rawAttachment.end(params.AttachmentID)
	return nil
}

func (h *daemonHost) handleRawAttachment(ctx context.Context, client daemon.ClientContext, method string, raw json.RawMessage) (any, error) {
	var params daemon.TerminalAttachParams
	if err := json.Unmarshal(raw, &params); err != nil || params.TerminalID == "" {
		return nil, daemon.Error("InvalidArgument", "terminal_id is required", ExitInvalidArgument, nil)
	}
	if method == "terminal.attach" {
		entry, err := h.lookup(params.TerminalID)
		if err != nil {
			return nil, err
		}
		terminal, ok := entry.term.(rawAttachTerminal)
		if !ok {
			return nil, daemon.Error("DependencyUnavailable", "terminal does not support raw attachment", ExitDependencyUnavailable, nil)
		}
		result, err := terminal.AttachRaw(ctx, client)
		if err != nil {
			return nil, toDaemonError(err)
		}
		h.mu.Lock()
		if h.rawAttachments == nil {
			h.rawAttachments = make(map[string]daemonRawAttachment)
		}
		h.rawAttachments[result.AttachmentID] = daemonRawAttachment{terminalID: params.TerminalID, owner: client.ID, terminal: terminal}
		h.mu.Unlock()
		go func() {
			<-terminal.RawAttachmentDone(result.AttachmentID)
			h.mu.Lock()
			delete(h.rawAttachments, result.AttachmentID)
			h.mu.Unlock()
		}()
		return result, nil
	}
	h.mu.RLock()
	attachment, ok := h.rawAttachments[params.AttachmentID]
	h.mu.RUnlock()
	if !ok || attachment.terminalID != params.TerminalID || attachment.owner != client.ID {
		return nil, daemon.Error("PreconditionFailed", "terminal attachment is no longer active", ExitPreconditionFailed, nil)
	}
	switch method {
	case "terminal.attach_read":
		result, err := attachment.terminal.ReadRaw(client.ID, params)
		return result, toDaemonError(err)
	default:
		return nil, toDaemonError(attachment.terminal.DetachRaw(client.ID, params))
	}
}
