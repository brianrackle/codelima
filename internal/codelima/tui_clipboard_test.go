package codelima

import (
	"os"
	"path/filepath"
	"testing"

	"go.rockorager.dev/vaxis"
)

func TestClipboardTUIDelegatesToOuterTerminalWithNativeWriterAvailable(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "native-writer-called")
	for _, name := range []string{"pbcopy", "wl-copy", "xclip", "xsel"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("#!/bin/sh\nprintf called > \"$CODELIMA_TEST_CLIPBOARD\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", root)
	t.Setenv("DISPLAY", ":test")
	t.Setenv("WAYLAND_DISPLAY", "test")
	t.Setenv("SSH_CONNECTION", "")
	t.Setenv("SSH_TTY", "")
	t.Setenv("CODELIMA_TEST_CLIPBOARD", marker)
	// A headless Vaxis still exercises routing, without touching the host clipboard.
	app := &vaxisTUIApp{vx: &vaxis.Vaxis{}}
	if err := app.copyToHostClipboard("response"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("TUI invoked a native clipboard writer: %v", err)
	}
}
