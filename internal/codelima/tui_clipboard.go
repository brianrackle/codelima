package codelima

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type osc52ClipboardScanner struct {
	onClipboard func(string)
	inOSC       bool
	escPending  bool
	prefixESC   bool
	buffer      strings.Builder
}

func newOSC52ClipboardScanner(onClipboard func(string)) *osc52ClipboardScanner {
	return &osc52ClipboardScanner{onClipboard: onClipboard}
}

func (s *osc52ClipboardScanner) Write(data []byte) {
	if s == nil || len(data) == 0 {
		return
	}

	for _, b := range data {
		s.writeByte(b)
	}
}

func (s *osc52ClipboardScanner) writeByte(b byte) {
	if !s.inOSC {
		if s.prefixESC {
			s.prefixESC = false
			if b == ']' {
				s.inOSC = true
				s.escPending = false
				s.buffer.Reset()
				return
			}
		}
		s.prefixESC = b == 0x1b
		return
	}

	if s.escPending {
		s.escPending = false
		if b == '\\' {
			s.finish('\\')
			return
		}
		s.buffer.WriteByte(0x1b)
	}

	switch b {
	case 0x07:
		s.finish(0x07)
	case 0x1b:
		s.escPending = true
	default:
		s.buffer.WriteByte(b)
		if s.buffer.Len() > 1024*1024 {
			s.reset()
		}
	}
}

func (s *osc52ClipboardScanner) finish(_ byte) {
	payload := s.buffer.String()
	s.reset()

	parts := strings.SplitN(payload, ";", 3)
	if len(parts) != 3 || parts[0] != "52" || parts[2] == "?" {
		return
	}
	decoded, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return
	}
	if s.onClipboard != nil {
		s.onClipboard(string(decoded))
	}
}

func (s *osc52ClipboardScanner) reset() {
	s.inOSC = false
	s.escPending = false
	s.prefixESC = false
	s.buffer.Reset()
}

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
