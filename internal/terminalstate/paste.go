package terminalstate

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

const MaxPasteBytes = daemon.MaxMessageSize / 16

var ErrUnsafePaste = errors.New("terminal rejected unsafe paste; enable bracketed paste in the receiving application")

func ValidatePaste(text string) error {
	if len(text) > MaxPasteBytes {
		return fmt.Errorf("paste exceeds the %d-byte limit; nothing was sent", MaxPasteBytes)
	}
	if !utf8.ValidString(text) {
		return errors.New("paste must contain valid UTF-8; nothing was sent")
	}
	return nil
}
