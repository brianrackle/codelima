//go:build cgo && (darwin || linux)

package ghostty

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"go.rockorager.dev/vaxis"
	"golang.org/x/sys/unix"
)

func stderrIdentity(t *testing.T) (uint64, uint64) {
	t.Helper()
	var stat unix.Stat_t
	if err := unix.Fstat(2, &stat); err != nil {
		t.Fatalf("fstat stderr: %v", err)
	}
	return uint64(stat.Dev), uint64(stat.Ino)
}

func newCaptureTestTerminal(t *testing.T, id string) *ghosttyTUITerminal {
	t.Helper()
	base, err := newGhosttyTUITerminal(id, func(vaxis.Event) {})
	if err != nil {
		t.Fatalf("initialize the statically linked Ghostty terminal: %v", err)
	}
	terminal, ok := base.(*ghosttyTUITerminal)
	if !ok {
		base.Close()
		t.Fatalf("expected ghostty terminal implementation, got %T", base)
	}
	return terminal
}

// Native diagnostics use the public log callback. Terminal construction,
// ingestion and teardown must never replace the caller's stderr descriptor.
func TestGhosttyTerminalsNeverReplaceProcessStderr(t *testing.T) {
	ghosttyStderrCaptureMu.Lock()
	defer ghosttyStderrCaptureMu.Unlock()

	originalDev, originalIno := stderrIdentity(t)

	first := newCaptureTestTerminal(t, "capture-first")
	defer first.Close()
	if dev, ino := stderrIdentity(t); dev != originalDev || ino != originalIno {
		t.Fatal("creating a terminal replaced the caller's stderr")
	}

	second := newCaptureTestTerminal(t, "capture-second")
	defer second.Close()
	first.ingestPTY([]byte("first terminal\r\n"))
	second.ingestPTY([]byte("second terminal\r\n"))
	if dev, ino := stderrIdentity(t); dev != originalDev || ino != originalIno {
		t.Fatal("concurrent terminals changed stderr during ingestion")
	}

	second.Close()
	if dev, ino := stderrIdentity(t); dev != originalDev || ino != originalIno {
		t.Fatal("closing one terminal changed stderr")
	}

	first.Close()
	if dev, ino := stderrIdentity(t); dev != originalDev || ino != originalIno {
		t.Fatal("closing every terminal changed stderr")
	}
}

func TestGhosttyNativeLogHookForwardsToThePackageLog(t *testing.T) {
	ghosttyStderrCaptureMu.Lock()
	defer ghosttyStderrCaptureMu.Unlock()

	terminal := newCaptureTestTerminal(t, "capture-destination")
	defer terminal.Close()

	var records lockedTestBuffer
	original := packageLog()
	setPackageLogger(newTextLogger(&records, parseLogLevel("debug")))
	t.Cleanup(func() { setPackageLogger(original) })

	marker := fmt.Sprintf("codelima-capture-probe-%d", time.Now().UnixNano())
	emitGhosttyTestLog([]byte(marker))
	drainGhosttyLogs()
	if !strings.Contains(records.String(), marker) {
		t.Fatalf("native callback record never reached package log: %q", records.String())
	}
	if !strings.Contains(records.String(), "source=libghostty") {
		t.Fatalf("captured record lost its source tag: %q", records.String())
	}
}

func TestGhosttyNativeLogSaturationDoesNotBlockTerminalWork(t *testing.T) {
	ghosttyStderrCaptureMu.Lock()
	defer ghosttyStderrCaptureMu.Unlock()

	terminal := newCaptureTestTerminal(t, "saturated-native-log")
	defer terminal.Close()
	drainGhosttyLogs()

	done := make(chan struct{})
	go func() {
		defer close(done)
		message := []byte(strings.Repeat("diagnostic ", 128))
		for range 10000 {
			emitGhosttyTestLog(message)
		}
		terminal.ingestPTY([]byte("terminal remains responsive\r\n"))
		_ = terminal.serveSnapshot()
		_ = terminal.serveRead(ReadVisible, ReadText)
		_ = terminal.readPendingResponses()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("full native log buffer blocked terminal ingestion")
	}
	if text := terminal.serveRead(ReadVisible, ReadText).Text; !strings.Contains(text, "terminal remains responsive") {
		t.Fatalf("terminal did not consume input after log saturation: %q", text)
	}
}

// Two in-process terminals emulating, reading and snapshotting at the same time
// used to be fully serialized by the per-call wrapper's global mutex. They now
// run concurrently, and must stay correct doing so (run this under -race).
func TestConcurrentGhosttyTerminalsEmulateIndependently(t *testing.T) {
	ghosttyStderrCaptureMu.Lock()
	defer ghosttyStderrCaptureMu.Unlock()

	const terminals = 4
	const rounds = 40

	instances := make([]*ghosttyTUITerminal, 0, terminals)
	for index := range terminals {
		instances = append(instances, newCaptureTestTerminal(t, fmt.Sprintf("concurrent-%d", index)))
	}
	defer func() {
		for _, terminal := range instances {
			terminal.Close()
		}
	}()

	var group sync.WaitGroup
	failures := make([]string, terminals)
	for index, terminal := range instances {
		group.Add(1)
		go func() {
			defer group.Done()
			marker := fmt.Sprintf("terminal-%d-marker", index)
			for range rounds {
				terminal.ingestPTY([]byte(marker + "\r\n"))
				_ = terminal.serveSnapshot()
				_ = terminal.serveRead(ReadRecent, ReadText)
			}
			text := terminal.serveRead(ReadVisible, ReadText).Text
			if !strings.Contains(text, marker) {
				failures[index] = fmt.Sprintf("terminal %d screen = %q, want %q", index, text, marker)
				return
			}
			for other := range terminals {
				if other == index {
					continue
				}
				if strings.Contains(text, fmt.Sprintf("terminal-%d-marker", other)) {
					failures[index] = fmt.Sprintf("terminal %d screen leaked terminal %d output", index, other)
					return
				}
			}
		}()
	}
	group.Wait()
	for _, failure := range failures {
		if failure != "" {
			t.Fatal(failure)
		}
	}
}

type lockedTestBuffer struct {
	mu      sync.Mutex
	builder strings.Builder
}

func (b *lockedTestBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.builder.Write(data)
}

func (b *lockedTestBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.builder.String()
}

var _ io.Writer = (*lockedTestBuffer)(nil)
