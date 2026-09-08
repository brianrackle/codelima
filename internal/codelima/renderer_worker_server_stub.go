//go:build !darwin && !linux

package codelima

import (
	"context"
	"errors"
)

func RunRendererWorker(context.Context, RendererTerminalFactory) error {
	return errors.New("Ghostty renderer worker is unavailable on this build")
}
