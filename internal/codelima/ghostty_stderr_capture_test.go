//go:build cgo && (darwin || linux)

package codelima

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"git.sr.ht/~rockorager/vaxis"
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
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	terminal, ok := base.(*ghosttyTUITerminal)
	if !ok {
		base.Close()
		t.Fatalf("expected ghostty terminal implementation, got %T", base)
	}
	return terminal
}

// The libghostty stderr redirect is installed once for the whole process and
// handed back when the last terminal goes away, instead of being torn up and
// down around every bridge call.
func TestGhosttyStderrCaptureIsInstalledOncePerProcessAndRestored(t *testing.T) {
	ghosttyStderrCaptureMu.Lock()
	defer ghosttyStderrCaptureMu.Unlock()

	if ghosttyStderrCaptureActive() {
		t.Fatal("libghostty stderr capture is already installed before any terminal exists")
	}
	originalDev, originalIno := stderrIdentity(t)

	first := newCaptureTestTerminal(t, "capture-first")
	if !ghosttyStderrCaptureActive() {
		t.Fatal("capture was not installed for the first libghostty terminal")
	}
	capturedDev, capturedIno := stderrIdentity(t)
	if capturedDev == originalDev && capturedIno == originalIno {
		t.Fatal("fd 2 still points at the caller's stderr while a terminal is live")
	}

	second := newCaptureTestTerminal(t, "capture-second")
	if dev, ino := stderrIdentity(t); dev != capturedDev || ino != capturedIno {
		t.Fatal("a second terminal reinstalled the process-wide redirect")
	}

	second.Close()
	if !ghosttyStderrCaptureActive() {
		t.Fatal("capture was released while another terminal is still live")
	}
	if dev, ino := stderrIdentity(t); dev != capturedDev || ino != capturedIno {
		t.Fatal("fd 2 changed while a terminal is still live")
	}

	first.Close()
	if ghosttyStderrCaptureActive() {
		t.Fatal("capture is still installed after the last terminal closed")
	}
	if dev, ino := stderrIdentity(t); dev != originalDev || ino != originalIno {
		t.Fatal("fd 2 was not handed back after the last terminal closed")
	}
}

// The capture destination is unchanged by the move to a process-wide redirect:
// whatever libghostty writes to fd 2 still reaches the package logger tagged
// source=libghostty, which is the TUI log file in the TUI and a discard sink in
// the renderer worker.
func TestGhosttyStderrCaptureStillForwardsToThePackageLog(t *testing.T) {
	ghosttyStderrCaptureMu.Lock()
	defer ghosttyStderrCaptureMu.Unlock()

	terminal := newCaptureTestTerminal(t, "capture-destination")
	defer terminal.Close()

	var records lockedTestBuffer
	original := packageLog()
	setPackageLogger(newTextLogger(&records, parseLogLevel("debug")))
	t.Cleanup(func() { setPackageLogger(original) })

	marker := fmt.Sprintf("codelima-capture-probe-%d", time.Now().UnixNano())
	if _, err := unix.Write(2, []byte(marker+"\n")); err != nil {
		t.Fatalf("write to captured stderr: %v", err)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		return strings.Contains(records.String(), marker)
	}, "captured stderr record reaching the package log")
	if !strings.Contains(records.String(), "source=libghostty") {
		t.Fatalf("captured record lost its source tag: %q", records.String())
	}
}

// Structural proof that the per-call wrapper is gone. Every libghostty bridge
// call used to run inside withGhosttyStderrSuppressed, which took this exact
// process-global mutex plus four dup/dup2 syscalls; holding the mutex here would
// have blocked every terminal's emulation, reads and input indefinitely.
func TestGhosttyBridgeCallsDoNotTakeTheProcessStderrLock(t *testing.T) {
	ghosttyStderrCaptureMu.Lock()
	defer ghosttyStderrCaptureMu.Unlock()

	terminal := newCaptureTestTerminal(t, "no-global-bridge-lock")

	ghosttyStderr.mu.Lock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		terminal.ingestPTY([]byte("bridge-call-under-capture-lock\r\n"))
		_ = terminal.serveSnapshot()
		_ = terminal.serveRead(ReadVisible, ReadText)
		_ = terminal.readPendingResponses()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		ghosttyStderr.mu.Unlock()
		terminal.Close()
		t.Fatal("bridge calls still serialize on the process-global stderr lock")
	}
	ghosttyStderr.mu.Unlock()
	terminal.Close()
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
