//go:build cgo && (darwin || linux)

package codelima

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// RestartRenderer is the operator escape hatch from a renderer the automatic
// policy has given up on, so it must reach the supervisor's forced-restart path
// and leave the session intact: the PTY, the child process and the retained
// journal all survive, and the replacement replays the journal so the screen
// comes back rather than starting blank.
func TestIsolatedTerminalRestartRendererReplacesTheRendererProcess(t *testing.T) {
	terminal := newLazyVariantTestTerminal(t, "manual-renderer-restart")
	if err := terminal.Start(exec.Command("/bin/sh", "-c", "printf 'before-restart\\r\\n'; sleep 30")); err != nil {
		t.Fatalf("start terminal: %v", err)
	}
	waitForCondition(t, 10*time.Second, func() bool {
		return strings.Contains(terminal.ReadVisible(ReadText).Text, "before-restart")
	}, "first published screen")

	before := terminal.RendererStatus()
	if before.Generation == 0 {
		t.Fatalf("renderer status before restart = %#v, want a running generation", before)
	}
	if err := terminal.RestartRenderer(); err != nil {
		t.Fatalf("RestartRenderer() error = %v", err)
	}
	waitForCondition(t, 20*time.Second, func() bool {
		status := terminal.RendererStatus()
		return status.Generation > before.Generation && status.State == rendererStateReady
	}, "replacement renderer generation")

	// A manual restart is deliberately uncharged: it must not consume the budget
	// the automatic policy owns.
	if status := terminal.RendererStatus(); status.RestartCount != before.RestartCount {
		t.Fatalf("manual restart charged the budget: %d restarts, want %d", status.RestartCount, before.RestartCount)
	}
	// The replacement replayed the journal, so the pre-restart screen is back.
	waitForCondition(t, 20*time.Second, func() bool {
		return strings.Contains(terminal.ReadVisible(ReadText).Text, "before-restart")
	}, "replayed screen after restart")
	// And the shell is still the same shell, still connected to the same PTY.
	terminal.SendInput([]byte("printf 'after-restart\\r\\n'\r"))
	waitForCondition(t, 20*time.Second, func() bool {
		return strings.Contains(terminal.ReadVisible(ReadText).Text, "after-restart")
	}, "output after the renderer restart")

	// A closed terminal has no renderer to replace and says so, rather than
	// reporting a restart that never happened.
	terminal.Close()
	if err := terminal.RestartRenderer(); !errors.Is(err, errTerminalClosed) {
		t.Fatalf("RestartRenderer() on a closed terminal = %v, want %v", err, errTerminalClosed)
	}
}
