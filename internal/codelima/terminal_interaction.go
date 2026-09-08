package codelima

import "github.com/brianrackle/codelima/internal/terminalstate"

type TerminalInteractionRequest = terminalstate.InteractionRequest
type TerminalInteractionResult = terminalstate.InteractionResult
type TerminalSearchStatus = terminalstate.SearchStatus
type TerminalColors = terminalstate.Colors
type terminalInteractionReceiver interface {
	Interact(TerminalInteractionRequest) (TerminalInteractionResult, error)
}

func validateTerminalInteraction(request TerminalInteractionRequest) error {
	return terminalstate.ValidateInteraction(request)
}
