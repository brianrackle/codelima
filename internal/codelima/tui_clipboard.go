package codelima

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

func (a *vaxisTUIApp) copyToHostClipboard(text string) error {
	if a.clipboardPush != nil {
		return a.clipboardPush(text)
	}
	if a.vx != nil {
		a.vx.ClipboardPush(text)
		return nil
	}
	return writeHostClipboard(text)
}

func writeHostClipboard(text string) error {
	switch runtime.GOOS {
	case "darwin":
		return writeClipboardCommand(text, "pbcopy")
	default:
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			if _, err := exec.LookPath("wl-copy"); err == nil {
				return writeClipboardCommand(text, "wl-copy")
			}
		}
		if os.Getenv("DISPLAY") != "" {
			if _, err := exec.LookPath("xclip"); err == nil {
				return writeClipboardCommand(text, "xclip", "-selection", "clipboard")
			}
			if _, err := exec.LookPath("xsel"); err == nil {
				return writeClipboardCommand(text, "xsel", "--clipboard", "--input")
			}
		}
	}
	return fmt.Errorf("no host clipboard command is available")
}

func writeClipboardCommand(text string, name string, args ...string) error {
	command := exec.Command(name, args...)
	command.Stdin = strings.NewReader(text)
	if err := command.Run(); err != nil {
		return fmt.Errorf("write host clipboard: %w", err)
	}
	return nil
}
