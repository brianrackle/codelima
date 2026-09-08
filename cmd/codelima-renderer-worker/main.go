package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/brianrackle/codelima/internal/codelima"
	"github.com/brianrackle/codelima/internal/ghostty"
	"go.rockorager.dev/vaxis"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := codelima.RunRendererWorker(ctx, func(id string, event func(vaxis.Event), write func(uint64, uint32, []byte)) (codelima.RendererTerminal, error) {
		return ghostty.New(id, event, write)
	}); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
