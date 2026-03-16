//go:build cgo && (darwin || linux)

package codelima

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"

	"git.sr.ht/~rockorager/vaxis"
)

var ghosttyStderrCaptureMu sync.Mutex

func TestGhosttyStyleForColorsLeavesDefaultBackgroundTransparent(t *testing.T) {
	t.Parallel()

	style := ghosttyStyleForColors(0xAABBCC, 0x000000, 0x000000)
	if style.Foreground != vaxis.HexColor(0xAABBCC) {
		t.Fatalf("foreground = %v, want %v", style.Foreground, vaxis.HexColor(0xAABBCC))
	}
	if style.Background != vaxis.ColorDefault {
		t.Fatalf("background = %v, want default background", style.Background)
	}
}

func TestGhosttyStyleForColorsPreservesNonDefaultBackground(t *testing.T) {
	t.Parallel()

	style := ghosttyStyleForColors(0xAABBCC, 0x112233, 0x000000)
	if style.Background != vaxis.HexColor(0x112233) {
		t.Fatalf("background = %v, want %v", style.Background, vaxis.HexColor(0x112233))
	}
}

func TestGhosttyTerminalDoesNotWriteOSCWarningsToStderr(t *testing.T) {
	ghosttyStderrCaptureMu.Lock()
	defer ghosttyStderrCaptureMu.Unlock()

	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	stderrOutput := captureGhosttyProcessStderr(t, func() {
		ghostty.ingestPTY([]byte("\x1b]112\x07"))
		ghostty.ingestPTY([]byte("\x1b]11;?\x07"))
	})
	if strings.Contains(stderrOutput, "warning(osc):") {
		t.Fatalf("expected Ghostty OSC processing to stay off stderr, got %q", stderrOutput)
	}
}

func TestGhosttyTerminalAnswersModifyOtherKeysQueryWithoutWarnings(t *testing.T) {
	ghosttyStderrCaptureMu.Lock()
	defer ghosttyStderrCaptureMu.Unlock()

	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	ghostty.ingestPTY([]byte("\x1b[>4;2m"))
	stderrOutput := captureGhosttyProcessStderr(t, func() {
		ghostty.ingestPTY([]byte("\x1b[?4m"))
	})
	if strings.TrimSpace(stderrOutput) != "" {
		t.Fatalf("expected no Ghostty parser warnings, got %q", stderrOutput)
	}

	if got, want := ghostty.readPendingResponses(), "\x1b[>4;2m"; got != want {
		t.Fatalf("modifyOtherKeys query response = %q, want %q", got, want)
	}
}

func TestGhosttyTerminalIgnoresVimTitleStackQueriesWithoutWarnings(t *testing.T) {
	ghosttyStderrCaptureMu.Lock()
	defer ghosttyStderrCaptureMu.Unlock()

	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	stderrOutput := captureGhosttyProcessStderr(t, func() {
		ghostty.ingestPTY([]byte("\x1b[22;2t\x1b[22;1t\x1b[23;2t\x1b[23;1t"))
	})
	if strings.TrimSpace(stderrOutput) != "" {
		t.Fatalf("expected no Ghostty parser warnings, got %q", stderrOutput)
	}
}

func TestGhosttyTerminalSuppressesUnknownParserWarningsFromStderr(t *testing.T) {
	ghosttyStderrCaptureMu.Lock()
	defer ghosttyStderrCaptureMu.Unlock()

	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	stderrOutput := captureGhosttyProcessStderr(t, func() {
		ghostty.ingestPTY([]byte("\x1b[?5m"))
	})
	if strings.TrimSpace(stderrOutput) != "" {
		t.Fatalf("expected Ghostty parser warnings to stay contained, got %q", stderrOutput)
	}
}

func captureGhosttyProcessStderr(t *testing.T, fn func()) string {
	t.Helper()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}

	stderrFD := int(os.Stderr.Fd())
	savedFD, err := syscall.Dup(stderrFD)
	if err != nil {
		_ = reader.Close()
		_ = writer.Close()
		t.Fatalf("dup stderr error = %v", err)
	}

	if err := syscall.Dup2(int(writer.Fd()), stderrFD); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		_ = syscall.Close(savedFD)
		t.Fatalf("redirect stderr error = %v", err)
	}
	_ = writer.Close()

	outputCh := make(chan string, 1)
	go func() {
		var buffer bytes.Buffer
		_, _ = io.Copy(&buffer, reader)
		_ = reader.Close()
		outputCh <- buffer.String()
	}()

	fn()

	if err := syscall.Dup2(savedFD, stderrFD); err != nil {
		_ = syscall.Close(savedFD)
		t.Fatalf("restore stderr error = %v", err)
	}
	_ = syscall.Close(savedFD)

	return <-outputCh
}
