package codelima

import "context"

func (t *daemonTUITerminal) Interact(request TerminalInteractionRequest) (TerminalInteractionResult, error) {
	if err := validateTerminalInteraction(request); err != nil {
		return TerminalInteractionResult{}, err
	}
	if t.isClosed() {
		return TerminalInteractionResult{}, errTerminalClosed
	}
	ctx, cancel := context.WithTimeout(context.Background(), daemonRPCTimeout)
	defer cancel()
	var result TerminalInteractionResult
	err := t.client.Call(ctx, "terminal.interact", struct {
		TerminalID string                     `json:"terminal_id"`
		Request    TerminalInteractionRequest `json:"request"`
	}{TerminalID: t.id, Request: request}, &result)
	return result, err
}

func (t *daemonTUITerminal) Metadata() TerminalMetadata {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.metadataLocked()
}

func (t *daemonTUITerminal) metadataLocked() TerminalMetadata {
	if t.metadataSequence > t.snapshot.SnapshotSequence {
		return t.metadata
	}
	return t.snapshot.Metadata
}

// updateMetadata updates labels without waking a hidden tab's screen worker.
// A delayed snapshot reply cannot overwrite newer pushed presentation state.
func (t *daemonTUITerminal) updateMetadata(sequence uint64, metadata TerminalMetadata) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || sequence <= t.snapshot.SnapshotSequence || sequence <= t.metadataSequence {
		return false
	}
	changed := t.metadataLocked() != metadata
	t.metadata, t.metadataSequence = metadata, sequence
	return changed
}
