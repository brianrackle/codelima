//go:build !cgo || (!darwin && !linux)

package codelima

import (
	"errors"
	"os"

	"git.sr.ht/~rockorager/vaxis"
)

func newIsolatedDaemonTerminal(targetKey string, postEvent func(vaxis.Event)) tuiTerminal {
	return newTUIVaxisTerminal(targetKey, postEvent)
}

func adoptIsolatedDaemonTerminal(
	string,
	func(vaxis.Event),
	*os.File,
	int,
	int,
	int,
	[]byte,
	bool,
) (daemonTerminal, error) {
	return nil, errors.New("isolated daemon terminals are unavailable on this build")
}
