//go:build cgo && (darwin || linux)

package codelima

import (
	"github.com/brianrackle/codelima/internal/ghostty"
	"go.rockorager.dev/vaxis"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type ghosttyTUITerminal = ghostty.Terminal

func newGhosttyTUITerminal(id string, post func(vaxis.Event)) (tuiTerminal, error) {
	return ghostty.New(id, post, nil)
}

func testGhosttyTerminalFactory(t *testing.T) func(string, func(vaxis.Event)) tuiTerminal {
	t.Helper()
	return func(id string, post func(vaxis.Event)) tuiTerminal {
		term, err := newGhosttyTUITerminal(id, post)
		if err != nil {
			t.Fatalf("initialize native test terminal: %v", err)
		}
		return term
	}
}

func TestGhosttyTerminalShiftEnterDoesNotLeakModifyOtherKeysSequenceAtBashPrompt(t *testing.T) {
	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Fatalf("initialize statically linked Ghostty: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	renderSnapshot := func(width, height int) string {
		vx := newRenderTestVaxis(t, width, height)
		defer vx.Close()

		win := vx.Window()
		win.Clear()
		ghostty.Draw(win)
		return renderedScreenText(t, vx, width, height)
	}

	ghostty.Resize(80, 12)

	homeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(homeDir, ".bash_profile"), []byte("export PS1='prompt$ '\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(.bash_profile) error = %v", err)
	}

	cmdArgs := interactiveShellLaunchCommand()
	cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
	cmd.Env = append(os.Environ(),
		"HOME="+homeDir,
		"SHELL=/bin/bash",
		"TERM="+tuiEmbeddedTermEnv,
		"BASH_SILENCE_DEPRECATION_WARNING=1",
	)
	if err := ghostty.Start(cmd); err != nil {
		t.Fatalf("ghostty.Start() error = %v", err)
	}

	waitForCondition(t, 5*time.Second, func() bool {
		return strings.Contains(renderSnapshot(80, 12), "prompt$")
	}, "bash prompt to appear")

	ghostty.Update(vaxis.Key{Keycode: vaxis.KeyEnter, Modifiers: vaxis.ModShift})

	time.Sleep(200 * time.Millisecond)

	screen := renderSnapshot(80, 12)
	if strings.Contains(screen, ";2;13~") {
		t.Fatalf("shift-enter leaked modifyOtherKeys sequence at bash prompt: %q", screen)
	}
}
