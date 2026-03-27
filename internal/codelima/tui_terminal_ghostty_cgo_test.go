//go:build cgo && (darwin || linux)

package codelima

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"golang.org/x/sys/unix"
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

func TestGhosttyKeyEncoderMatchesExistingCommonSequences(t *testing.T) {
	t.Parallel()

	encoder, err := newGhosttyKeyEncoder()
	if err != nil {
		t.Skipf("ghostty key encoder unavailable in this test environment: %v", err)
	}
	defer encoder.Close()

	cases := []struct {
		name                  string
		key                   vaxis.Key
		applicationKeypad     bool
		cursorKeysApplication bool
		want                  string
	}{
		{
			name: "cursor-normal",
			key:  vaxis.Key{Keycode: vaxis.KeyUp},
			want: "\x1b[A",
		},
		{
			name:                  "cursor-application",
			key:                   vaxis.Key{Keycode: vaxis.KeyUp},
			cursorKeysApplication: true,
			want:                  "\x1bOA",
		},
		{
			name: "ctrl-c",
			key: vaxis.Key{
				Keycode:        'c',
				BaseLayoutCode: 'c',
				Modifiers:      vaxis.ModCtrl,
			},
			want: "\x03",
		},
		{
			name: "alt-x",
			key: vaxis.Key{
				Keycode:        'x',
				BaseLayoutCode: 'x',
				Modifiers:      vaxis.ModAlt,
			},
			want: "\x1bx",
		},
		{
			name: "shifted-punctuation",
			key: vaxis.Key{
				Text:           ":",
				Keycode:        ';',
				ShiftedCode:    ':',
				BaseLayoutCode: ';',
				Modifiers:      vaxis.ModShift,
			},
			want: ":",
		},
		{
			name: "paste-text",
			key: vaxis.Key{
				Text:      "hello",
				EventType: vaxis.EventPaste,
			},
			want: "hello",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := encodeTUITerminalKeyWithGhostty(tc.key, encoder, tc.applicationKeypad, tc.cursorKeysApplication)
			if got != tc.want {
				t.Fatalf("encodeTUITerminalKeyWithGhostty() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGhosttyKeyEncoderSuppressesReleaseEvents(t *testing.T) {
	t.Parallel()

	encoder, err := newGhosttyKeyEncoder()
	if err != nil {
		t.Skipf("ghostty key encoder unavailable in this test environment: %v", err)
	}
	defer encoder.Close()

	got := encodeTUITerminalKeyWithGhostty(vaxis.Key{
		Keycode:        'a',
		BaseLayoutCode: 'a',
		EventType:      vaxis.EventRelease,
	}, encoder, false, false)
	if got != "" {
		t.Fatalf("release key encoded as %q, want empty sequence", got)
	}
}

func TestGhosttyKeyEncoderFallsBackForUnsupportedFunctionKeys(t *testing.T) {
	t.Parallel()

	encoder, err := newGhosttyKeyEncoder()
	if err != nil {
		t.Skipf("ghostty key encoder unavailable in this test environment: %v", err)
	}
	defer encoder.Close()

	got := encodeTUITerminalKeyWithGhostty(vaxis.Key{Keycode: vaxis.KeyF26}, encoder, false, false)
	if got != "\x1B[1;5Q" {
		t.Fatalf("unsupported Ghostty key should fall back to legacy encoding, got %q", got)
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

func TestGhosttyTerminalRoundTripsSttyRawPrompt(t *testing.T) {
	script := newSttyRawPromptScript(t)
	runGhosttySttyRawPrompt(t, exec.Command("bash", "-lc", script.body), script.readyPath, script.resultPath)
}

func TestGhosttyTerminalRoundTripsSttyRawPromptThroughNestedPTY(t *testing.T) {
	if _, err := exec.LookPath("script"); err != nil {
		t.Skipf("script utility unavailable: %v", err)
	}

	script := newSttyRawPromptScript(t)
	runGhosttySttyRawPrompt(t, exec.Command("script", "-q", "/dev/null", "bash", "-lc", script.body), script.readyPath, script.resultPath)
}

func captureGhosttyProcessStderr(t *testing.T, fn func()) string {
	t.Helper()

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}

	stderrFD := int(os.Stderr.Fd())
	savedFD, err := unix.Dup(stderrFD)
	if err != nil {
		_ = reader.Close()
		_ = writer.Close()
		t.Fatalf("dup stderr error = %v", err)
	}

	if err := unix.Dup2(int(writer.Fd()), stderrFD); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		_ = unix.Close(savedFD)
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

	if err := unix.Dup2(savedFD, stderrFD); err != nil {
		_ = unix.Close(savedFD)
		t.Fatalf("restore stderr error = %v", err)
	}
	_ = unix.Close(savedFD)

	return <-outputCh
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %s", path)
}

type sttyRawPromptScript struct {
	body       string
	readyPath  string
	resultPath string
}

func newSttyRawPromptScript(t *testing.T) sttyRawPromptScript {
	t.Helper()

	tempDir := t.TempDir()
	readyPath := filepath.Join(tempDir, "ready")
	resultPath := filepath.Join(tempDir, "result")
	errorPath := filepath.Join(tempDir, "restore.err")
	return sttyRawPromptScript{
		body: fmt.Sprintf(`
save_state="$(/bin/stty -g)"
printf ready > %q
/bin/stty raw -echo
IFS='' read -r -n 1 -d '' c
if /bin/stty "${save_state}" 2>%q; then
  printf 'ok:%%s' "$c" > %q
else
  printf 'fail:%%s\nstate=%%s\n' "$(/bin/cat %q)" "${save_state}" > %q
fi
`, readyPath, errorPath, resultPath, errorPath, resultPath),
		readyPath:  readyPath,
		resultPath: resultPath,
	}
}

func runGhosttySttyRawPrompt(t *testing.T, cmd *exec.Cmd, readyPath string, resultPath string) {
	t.Helper()

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

	if err := ghostty.Start(cmd); err != nil {
		t.Fatalf("ghostty.Start() error = %v", err)
	}

	waitForFile(t, readyPath, 5*time.Second)
	ghostty.Update(vaxis.Key{Keycode: vaxis.KeyEnter})
	waitForFile(t, resultPath, 5*time.Second)

	output, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("ReadFile(result) error = %v", err)
	}

	if got := strings.TrimSpace(string(output)); !strings.HasPrefix(got, "ok:") {
		t.Fatalf("expected stty restore to succeed, got %q", got)
	}
}
