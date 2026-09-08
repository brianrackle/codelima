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
	return t.snapshot.Metadata
}
