//go:build darwin || linux

package codelima

func (t *isolatedDaemonTerminal) Interact(request TerminalInteractionRequest) (TerminalInteractionResult, error) {
	if err := validateTerminalInteraction(request); err != nil {
		return TerminalInteractionResult{}, err
	}
	t.mu.Lock()
	renderer, closed := t.renderer, t.closed
	t.mu.Unlock()
	if closed || renderer == nil {
		return TerminalInteractionResult{}, errTerminalClosed
	}
	return renderer.Interact(request)
}
