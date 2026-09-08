//go:build cgo && (darwin || linux)

package ghostty

import (
	"go.rockorager.dev/vaxis"
	"testing"
)

func TestNewGhosttyTUITerminalLoadsWhenLibraryInstalled(t *testing.T) {
	t.Parallel()

	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Fatalf("statically linked Ghostty initialization failed: %v", err)
	}
	defer terminal.Close()

	if terminal.TermEnv() != tuiEmbeddedTermEnv {
		t.Fatalf("expected ghostty terminal TERM %q, got %q", tuiEmbeddedTermEnv, terminal.TermEnv())
	}
}
