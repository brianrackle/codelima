package codelima

import "github.com/brianrackle/codelima/internal/terminalstate"

const terminalMaxPasteBytes = terminalstate.MaxPasteBytes

type terminalPasteReceiver interface{ Paste(string) error }
type rendererPasteParams struct {
	Text string `json:"text"`
}

func validateTerminalPaste(text string) error { return terminalstate.ValidatePaste(text) }
