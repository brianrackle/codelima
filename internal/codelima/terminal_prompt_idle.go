package codelima

// The daemon snapshot publisher owns this clock, independently of frontends
// and renderer processes. Input time comes from the PTY owner, never a title
// or the time a frontend first happened to see a prompt.
type terminalPromptIdle struct{ since int64 }

func (s *terminalPromptIdle) observe(metadata TerminalMetadata, stale bool, input, now int64) int64 {
	if stale {
		return 0
	}
	if !metadata.CursorAtPrompt || metadata.ProgressState != 0 {
		s.since = 0
		return 0
	}
	if s.since <= 0 || s.since > now {
		s.since = now
	}
	if input > s.since && input <= now {
		s.since = input
	}
	return s.since
}
