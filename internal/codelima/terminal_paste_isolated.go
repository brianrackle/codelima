//go:build darwin || linux

package codelima

func (t *isolatedDaemonTerminal) Paste(text string) error {
	if err := validateTerminalPaste(text); err != nil {
		return err
	}
	t.mu.Lock()
	renderer, closed, writer := t.renderer, t.closed, t.ptyWriter
	t.mu.Unlock()
	if closed || renderer == nil || writer == nil {
		return errTerminalClosed
	}
	data, err := renderer.Paste(text)
	if err != nil {
		return err
	}
	if len(data) > 0 && !writer.Enqueue(data) {
		return errTerminalInputQueueFull
	}
	return nil
}
