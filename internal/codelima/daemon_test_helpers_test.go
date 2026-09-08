package codelima

import "github.com/brianrackle/codelima/internal/codelima/daemon"

func terminalStateIDs(states []daemon.TerminalState) []string {
	ids := make([]string, len(states))
	for index, state := range states {
		ids[index] = state.TerminalID
	}
	return ids
}
