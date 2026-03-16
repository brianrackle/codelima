//go:build !cgo || (!darwin && !linux)

package codelima

import (
	"errors"

	"git.sr.ht/~rockorager/vaxis"
)

func newGhosttyTUITerminal(string, func(vaxis.Event)) (tuiTerminal, error) {
	return nil, errors.New("ghostty terminal backend is unavailable on this build")
}
