package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/brianrackle/test_lima/internal/codelima"
)

func main() {
	os.Exit(run())
}

// run wraps the real entrypoint so `defer stop()` executes before os.Exit
// (deferred calls are skipped when os.Exit runs in the same frame). Ctrl+C
// (SIGINT) and SIGTERM cancel the context instead of killing the process
// outright, letting the TUI drain terminal sessions and restore the host
// terminal, and letting in-flight exec.CommandContext children be stopped.
func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return codelima.Run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
}
