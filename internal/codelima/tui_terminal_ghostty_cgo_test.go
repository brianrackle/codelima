//go:build cgo && (darwin || linux)

package codelima

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"git.sr.ht/~rockorager/vaxis"
	"golang.org/x/sys/unix"
)

var ghosttyStderrCaptureMu sync.Mutex

var ghosttyGrandchildPIDPattern = regexp.MustCompile(`GRANDCHILD=(\d+)`)

func waitForProcessGone(t *testing.T, pid int, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process %d is still alive %v after Close", pid, timeout)
}

// newActorTerminal spawns a real child through a runtime-actor terminal with NO
// TUI attached, the way the daemon (Track 3) will. It returns the concrete
// terminal so tests can exercise the actor's cmdRead/cmdSnapshot seam directly.
func newActorTerminal(t *testing.T, cmd *exec.Cmd) *ghosttyTUITerminal {
	t.Helper()

	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	t.Cleanup(terminal.Close)

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}
	if err := ghostty.Start(cmd); err != nil {
		t.Fatalf("ghostty.Start() error = %v", err)
	}
	return ghostty
}

func visibleLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// TestActorReadReturnsVisibleText is the headline DoD test for 2.1: a real
// /bin/sh driven only through the actor's input + read commands — no vaxis
// window, no Draw — and cmdRead returns the on-screen text.
func TestActorReadReturnsVisibleText(t *testing.T) {
	// Empty PS1 removes the shell prompt so the command output lands on its own
	// line regardless of prompt/echo interleaving; tty echo still shows the typed
	// command line separately.
	cmd := exec.Command("/bin/sh")
	cmd.Env = append(os.Environ(), "PS1=")
	ghostty := newActorTerminal(t, cmd)

	ghostty.SendInput([]byte("echo hello-actor\n"))

	var visible string
	waitForCondition(t, 5*time.Second, func() bool {
		result := ghostty.ReadVisible(ReadText)
		if result.Err != nil {
			return false
		}
		visible = result.Text
		return strings.Contains(visible, "hello-actor")
	}, "echoed command output to reach the actor-read visible text")

	// The echoed input line ("echo hello-actor", possibly prompt-prefixed) and
	// the command output line ("hello-actor") both appear; assert the output
	// line exactly, which does not depend on the shell's prompt string.
	foundExact := false
	for _, line := range visibleLines(visible) {
		if line == "hello-actor" {
			foundExact = true
			break
		}
	}
	if !foundExact {
		t.Fatalf("expected an exact visible line %q, got visible text:\n%s", "hello-actor", visible)
	}
}

// TestActorSnapshotIsConsistentAndAdvancesGeneration proves cmdSnapshot returns
// a full, internally consistent cell grid whose generation advances as the child
// streams output, and that snapshots taken mid-stream stay consistent.
func TestActorSnapshotIsConsistentAndAdvancesGeneration(t *testing.T) {
	ghostty := newActorTerminal(t, exec.Command("/bin/sh", "-c",
		`i=0; while true; do echo "tick $i"; i=$((i+1)); sleep 0.02; done`))

	assertConsistent := func(snap TerminalSnapshot) {
		t.Helper()
		if snap.Cols <= 0 || snap.Rows <= 0 {
			t.Fatalf("snapshot has non-positive dimensions: %dx%d", snap.Cols, snap.Rows)
		}
		if got, want := len(snap.Cells), snap.Cols*snap.Rows; got != want {
			t.Fatalf("snapshot cell count = %d, want Cols*Rows = %d", got, want)
		}
		if snap.CursorX < 0 || snap.CursorX > snap.Cols || snap.CursorY < 0 || snap.CursorY >= snap.Rows {
			t.Fatalf("snapshot cursor out of bounds: (%d,%d) grid %dx%d",
				snap.CursorX, snap.CursorY, snap.Cols, snap.Rows)
		}
	}

	first := ghostty.Snapshot()
	if first.Err != nil {
		t.Fatalf("initial Snapshot() error = %v", first.Err)
	}
	assertConsistent(first.Snapshot)
	baseGen := first.Snapshot.Generation

	// Generation must advance as new output streams in.
	var lastGen uint64 = baseGen
	waitForCondition(t, 5*time.Second, func() bool {
		result := ghostty.Snapshot()
		if result.Err != nil {
			return false
		}
		assertConsistent(result.Snapshot)
		lastGen = result.Snapshot.Generation
		return result.Snapshot.Generation > baseGen
	}, "snapshot generation to advance on new child output")

	// Repeated snapshots while output streams stay internally consistent and the
	// generation never goes backwards.
	for i := 0; i < 25; i++ {
		result := ghostty.Snapshot()
		if result.Err != nil {
			t.Fatalf("streaming Snapshot() error = %v", result.Err)
		}
		assertConsistent(result.Snapshot)
		if result.Snapshot.Generation < lastGen {
			t.Fatalf("snapshot generation went backwards: %d < %d", result.Snapshot.Generation, lastGen)
		}
		lastGen = result.Snapshot.Generation
	}
}

// TestActorResizeIsOrderedBeforeInput proves a cmdResize is fully applied
// (including the PTY winsize) before a subsequently-sent cmdInput runs in the
// child: `stty size` reports the resized geometry.
func TestActorResizeIsOrderedBeforeInput(t *testing.T) {
	ghostty := newActorTerminal(t, exec.Command("/bin/sh"))

	// Shrink from the default 80x24 to 40 cols x 10 rows, then ask the child for
	// its window size. stty prints "rows cols".
	ghostty.Resize(40, 10)
	ghostty.SendInput([]byte("stty size\n"))

	waitForCondition(t, 5*time.Second, func() bool {
		result := ghostty.ReadRecent(ReadText)
		if result.Err != nil {
			return false
		}
		return strings.Contains(result.Text, "10 40")
	}, "resized winsize (10 40) to be visible to the child before its input ran")
}

// TestActorCloseIsIdempotentAndGroupKills exercises cmdClose through the actor:
// it group-kills the child's descendants (Track 0.1 helper, now actor-owned),
// reaps the direct child, and a second Close is a no-op that returns promptly.
func TestActorCloseIsIdempotentAndGroupKills(t *testing.T) {
	ghostty := newActorTerminal(t, exec.Command("/bin/sh", "-c",
		`trap '' HUP; sleep 300 & echo "GRANDCHILD=$!"; exec sleep 300`))

	grandchildPID := 0
	waitForCondition(t, 5*time.Second, func() bool {
		match := ghosttyGrandchildPIDPattern.FindStringSubmatch(ghostty.ReadVisible(ReadText).Text)
		if match == nil {
			return false
		}
		pid, err := strconv.Atoi(match[1])
		if err != nil || pid <= 0 {
			return false
		}
		grandchildPID = pid
		return true
	}, "grandchild pid to appear in the actor-read visible text")
	t.Cleanup(func() { _ = syscall.Kill(grandchildPID, syscall.SIGKILL) })

	directPID := ghostty.cmd.Process.Pid

	ghostty.Close()

	// Second Close must be idempotent and return promptly, not hang.
	secondDone := make(chan struct{})
	go func() {
		ghostty.Close()
		close(secondDone)
	}()
	select {
	case <-secondDone:
	case <-time.After(2 * time.Second):
		t.Fatal("second Close() did not return: cmdClose is not idempotent")
	}

	if ghostty.cmd.ProcessState == nil {
		t.Fatal("direct child was not reaped when Close returned")
	}
	if err := syscall.Kill(directPID, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("direct child pid %d still signalable after Close, kill(pid,0) = %v", directPID, err)
	}
	waitForProcessGone(t, grandchildPID, 3*time.Second)
}

// TestCloseAfterChildSelfExitStillGroupKillsSurvivors pins the pre-actor
// guarantee that Close after the child exited on its own (actor already torn
// down via the read-error path) still escalates on the child's process group,
// reaping HUP-immune grandchildren that survived the self-exit.
func TestCloseAfterChildSelfExitStillGroupKillsSurvivors(t *testing.T) {
	// The grandchild ignores HUP and detaches from the tty (so the PTY master
	// sees EOF when the direct child exits); the child lingers briefly so the
	// test can read the grandchild pid, then exits on its own.
	ghostty := newActorTerminal(t, exec.Command("/bin/sh", "-c",
		`trap '' HUP; sleep 300 >/dev/null 2>&1 </dev/null & echo "GRANDCHILD=$!"; sleep 2; exit 0`))

	grandchildPID := 0
	waitForCondition(t, 5*time.Second, func() bool {
		match := ghosttyGrandchildPIDPattern.FindStringSubmatch(ghostty.ReadVisible(ReadText).Text)
		if match == nil {
			return false
		}
		pid, err := strconv.Atoi(match[1])
		if err != nil || pid <= 0 {
			return false
		}
		grandchildPID = pid
		return true
	}, "grandchild pid to appear in the actor-read visible text")
	t.Cleanup(func() { _ = syscall.Kill(grandchildPID, syscall.SIGKILL) })

	// Wait for the child to exit on its own and the actor to tear down.
	select {
	case <-ghostty.actorDone:
	case <-time.After(10 * time.Second):
		t.Fatal("actor did not exit after the child self-exited")
	}

	// The grandchild survived the self-exit (it ignores HUP)...
	if err := syscall.Kill(grandchildPID, 0); err != nil {
		t.Fatalf("grandchild pid %d should survive the child's self-exit, kill(pid,0) = %v", grandchildPID, err)
	}

	// ...and Close must still group-kill it.
	ghostty.Close()
	waitForProcessGone(t, grandchildPID, 3*time.Second)
}

// rawNonblockPipeTarget adapts a raw non-blocking fd to ghosttyPTYWriteTarget so
// a real EAGAIN + POLLOUT round-trip can be exercised without a PTY.
type rawNonblockPipeTarget struct{ fd int }

func (r *rawNonblockPipeTarget) Write(p []byte) (int, error) {
	n, err := unix.Write(r.fd, p)
	if n < 0 {
		n = 0
	}
	return n, err
}
func (r *rawNonblockPipeTarget) Close() error { return unix.Close(r.fd) }
func (r *rawNonblockPipeTarget) Fd() uintptr  { return uintptr(r.fd) }

// TestStartWiresPolloutWaiterForBackpressure is the busy-spin regression guard:
// production Start must construct the PTY writer with a real POLLOUT waiter, not
// nil (which made an EAGAIN spin hot). It asserts both the wiring and that the
// real waitGhosttyPTYWritable actually parks on POLLOUT and resumes on a real fd.
func TestStartWiresPolloutWaiterForBackpressure(t *testing.T) {
	ghostty := newActorTerminal(t, exec.Command("/bin/sh", "-c", "exec sleep 300"))

	ghostty.mu.Lock()
	waiter := ghostty.ptyWriter.waitWritable
	ghostty.mu.Unlock()
	if waiter == nil {
		t.Fatal("Start wired a nil PTY-writable waiter: an EAGAIN would busy-spin (busy-spin fix regressed)")
	}

	// Prove the real waiter parks on POLLOUT and resumes: fill a non-blocking
	// pipe until EAGAIN, then drain it from a goroutine so the write completes.
	var fds [2]int
	if err := unix.Pipe(fds[:]); err != nil {
		t.Fatalf("unix.Pipe() error = %v", err)
	}
	readFD, writeFD := fds[0], fds[1]
	defer func() { _ = unix.Close(readFD) }()
	if err := unix.SetNonblock(writeFD, true); err != nil {
		_ = unix.Close(writeFD)
		t.Fatalf("SetNonblock() error = %v", err)
	}

	// Fill the pipe buffer so the next write blocks (returns EAGAIN).
	filler := make([]byte, 64*1024)
	for {
		n, err := unix.Write(writeFD, filler)
		if err != nil {
			if isGhosttyPTYWouldBlockError(err) {
				break
			}
			_ = unix.Close(writeFD)
			t.Fatalf("filling pipe: unexpected error = %v", err)
		}
		if n == 0 {
			break
		}
	}

	// Drain in the background after a short delay so POLLOUT eventually fires.
	go func() {
		time.Sleep(100 * time.Millisecond)
		buf := make([]byte, 64*1024)
		for {
			n, err := unix.Read(readFD, buf)
			if n <= 0 || err != nil {
				return
			}
		}
	}()

	done := make(chan error, 1)
	go func() {
		done <- ghosttyWriteAllToPTY(&rawNonblockPipeTarget{fd: writeFD}, []byte("resume-after-pollout"), waitGhosttyPTYWritable)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ghosttyWriteAllToPTY() with real POLLOUT waiter error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ghosttyWriteAllToPTY() with real POLLOUT waiter did not complete: EAGAIN wait did not resume")
	}
}

func TestCloseKillsGrandchildProcesses(t *testing.T) {
	cases := []struct {
		name   string
		script string
	}{
		{
			// The grandchild dies from the SIGHUP delivered when the PTY
			// master closes; Close must not need to escalate.
			name:   "sighup-default",
			script: `sleep 300 & echo "GRANDCHILD=$!"; exec sleep 300`,
		},
		{
			// Mirrors real node-tab chains (msb/ssh descendants that
			// survive hangup): Close must escalate to a process-group kill.
			name:   "sighup-ignored",
			script: `trap '' HUP; sleep 300 & echo "GRANDCHILD=$!"; exec sleep 300`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
			if err != nil {
				t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
			}
			defer terminal.Close()

			ghostty, ok := terminal.(*ghosttyTUITerminal)
			if !ok {
				t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
			}

			cmd := exec.Command("/bin/sh", "-c", tc.script)
			if err := ghostty.Start(cmd); err != nil {
				t.Fatalf("ghostty.Start() error = %v", err)
			}

			vx := newRenderTestVaxis(t, 80, 24)
			defer vx.Close()

			grandchildPID := 0
			waitForCondition(t, 5*time.Second, func() bool {
				win := vx.Window()
				win.Clear()
				ghostty.Draw(win)
				match := ghosttyGrandchildPIDPattern.FindStringSubmatch(renderedScreenText(t, vx, 80, 24))
				if match == nil {
					return false
				}
				pid, err := strconv.Atoi(match[1])
				if err != nil || pid <= 0 {
					return false
				}
				grandchildPID = pid
				return true
			}, "grandchild pid to appear in the rendered terminal")
			t.Cleanup(func() {
				_ = syscall.Kill(grandchildPID, syscall.SIGKILL)
			})

			terminal.Close()

			waitForProcessGone(t, grandchildPID, 3*time.Second)
		})
	}
}

func TestCloseReapsDirectChildWithoutZombie(t *testing.T) {
	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	cmd := exec.Command("/bin/sh", "-c", "exec sleep 300")
	if err := ghostty.Start(cmd); err != nil {
		t.Fatalf("ghostty.Start() error = %v", err)
	}

	terminal.Close()

	if cmd.ProcessState == nil {
		t.Fatal("direct child was not reaped when Close returned")
	}
	if err := syscall.Kill(cmd.Process.Pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("direct child pid %d still signalable after Close, kill(pid, 0) = %v", cmd.Process.Pid, err)
	}
}

type ghosttyFakePTYWriteStep struct {
	n   int
	err error
}

type ghosttyFakePTYWriteTarget struct {
	mu     sync.Mutex
	steps  []ghosttyFakePTYWriteStep
	output bytes.Buffer
	closed bool
}

func (t *ghosttyFakePTYWriteTarget) Write(data []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.steps) > 0 {
		step := t.steps[0]
		t.steps = t.steps[1:]
		n := step.n
		if n > len(data) {
			n = len(data)
		}
		if n > 0 {
			_, _ = t.output.Write(data[:n])
		}
		return n, step.err
	}
	if t.closed {
		return 0, os.ErrClosed
	}
	_, _ = t.output.Write(data)
	return len(data), nil
}

func (t *ghosttyFakePTYWriteTarget) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	return nil
}

func (t *ghosttyFakePTYWriteTarget) Fd() uintptr {
	return 0
}

func (t *ghosttyFakePTYWriteTarget) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.output.String()
}

func waitForCondition(t *testing.T, timeout time.Duration, fn func() bool, description string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func TestGhosttyWriteAllToPTYHandlesPartialWrites(t *testing.T) {
	t.Parallel()

	target := &ghosttyFakePTYWriteTarget{
		steps: []ghosttyFakePTYWriteStep{
			{n: 1},
			{n: 2},
		},
	}

	if err := ghosttyWriteAllToPTY(target, []byte("abc"), nil); err != nil {
		t.Fatalf("ghosttyWriteAllToPTY() error = %v", err)
	}
	if got := target.String(); got != "abc" {
		t.Fatalf("ghosttyWriteAllToPTY() wrote %q, want %q", got, "abc")
	}
}

func TestGhosttyWriteAllToPTYWaitsForTemporaryBackpressure(t *testing.T) {
	t.Parallel()

	target := &ghosttyFakePTYWriteTarget{
		steps: []ghosttyFakePTYWriteStep{
			{n: 0, err: unix.EAGAIN},
			{n: 3},
		},
	}

	waitCalls := 0
	if err := ghosttyWriteAllToPTY(target, []byte("abc"), func(fd int) error {
		waitCalls++
		return nil
	}); err != nil {
		t.Fatalf("ghosttyWriteAllToPTY() error = %v", err)
	}
	if waitCalls != 1 {
		t.Fatalf("waitWritable calls = %d, want 1", waitCalls)
	}
	if got := target.String(); got != "abc" {
		t.Fatalf("ghosttyWriteAllToPTY() wrote %q, want %q", got, "abc")
	}
}

func TestGhosttyPTYWriterFlushesQueuedWrites(t *testing.T) {
	t.Parallel()

	target := &ghosttyFakePTYWriteTarget{}
	writer := newGhosttyPTYWriter(target, func(fd int) error { return nil }, nil)
	defer writer.Close()

	if !writer.Enqueue([]byte("ab")) {
		t.Fatal("expected first enqueue to succeed")
	}
	if !writer.Enqueue([]byte("cd")) {
		t.Fatal("expected second enqueue to succeed")
	}

	waitForCondition(t, time.Second, func() bool {
		return target.String() == "abcd"
	}, "queued PTY writes to flush")
}

func TestGhosttyTerminalPreservesDelayedInitialOutput(t *testing.T) {
	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	cmd := exec.Command("sh", "-lc", "sleep 0.2; printf prompt; sleep 0.2")
	if err := ghostty.Start(cmd); err != nil {
		t.Fatalf("ghostty.Start() error = %v", err)
	}

	vx := newRenderTestVaxis(t, 24, 4)
	defer vx.Close()

	waitForCondition(t, 2*time.Second, func() bool {
		win := vx.Window()
		win.Clear()
		ghostty.Draw(win)
		return strings.Contains(renderedScreenText(t, vx, 24, 4), "prompt")
	}, "delayed ghostty PTY output to reach the rendered terminal")
}

func TestGhosttyStyleForColorsLeavesDefaultColorsUnset(t *testing.T) {
	t.Parallel()

	style := ghosttyStyleForColors(0xAABBCC, 0x112233, false, false)
	if style.Foreground != vaxis.ColorDefault {
		t.Fatalf("foreground = %v, want default foreground", style.Foreground)
	}
	if style.Background != vaxis.ColorDefault {
		t.Fatalf("background = %v, want default background", style.Background)
	}
}

func TestGhosttyStyleForColorsPreservesExplicitColors(t *testing.T) {
	t.Parallel()

	style := ghosttyStyleForColors(0xAABBCC, 0x112233, true, true)
	if style.Foreground != vaxis.HexColor(0xAABBCC) {
		t.Fatalf("foreground = %v, want %v", style.Foreground, vaxis.HexColor(0xAABBCC))
	}
	if style.Background != vaxis.HexColor(0x112233) {
		t.Fatalf("background = %v, want %v", style.Background, vaxis.HexColor(0x112233))
	}
}

func TestGhosttyTerminalLeavesDefaultColorsUnset(t *testing.T) {
	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	vx := newRenderTestVaxis(t, 80, 24)
	defer vx.Close()

	ghostty.ingestPTY([]byte("X"))
	win := vx.Window()
	win.Clear()
	ghostty.Draw(win)

	style := renderedCellStyle(t, vx, 0, 0)
	if style.Foreground != vaxis.ColorDefault {
		t.Fatalf("foreground = %v, want default foreground", style.Foreground)
	}
	if style.Background != vaxis.ColorDefault {
		t.Fatalf("background = %v, want default background", style.Background)
	}
}

func TestGhosttyTerminalPreservesExplicitBackgroundEqualToDefault(t *testing.T) {
	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	vx := newRenderTestVaxis(t, 80, 24)
	defer vx.Close()

	defaultBackground := ghostty.defaultBackgroundRGBLocked()
	ghostty.ingestPTY([]byte(fmt.Sprintf(
		"\x1b[48;2;%d;%d;%dmX\x1b[0m",
		(defaultBackground>>16)&0xFF,
		(defaultBackground>>8)&0xFF,
		defaultBackground&0xFF,
	)))
	win := vx.Window()
	win.Clear()
	ghostty.Draw(win)

	style := renderedCellStyle(t, vx, 0, 0)
	if style.Background != vaxis.HexColor(defaultBackground) {
		t.Fatalf("background = %v, want explicit %v", style.Background, vaxis.HexColor(defaultBackground))
	}
}

func TestGhosttyTerminalRedrawsCleanlyAfterWidthGrowth(t *testing.T) {
	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
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

	ghostty.Resize(24, 12)

	cmd := exec.Command("/bin/bash", "--noprofile", "--norc", "-i")
	cmd.Env = append(os.Environ(),
		"TERM="+tuiEmbeddedTermEnv,
		"BASH_SILENCE_DEPRECATION_WARNING=1",
		`PS1=brianrackle@sandbox-codelima-codex-codelima-codex-node-test-019d2fff:/Users/brianrackle/Projects/codelima\$ `,
	)
	if err := ghostty.Start(cmd); err != nil {
		t.Fatalf("ghostty.Start() error = %v", err)
	}

	waitForCondition(t, 5*time.Second, func() bool {
		return strings.Contains(strings.ReplaceAll(renderSnapshot(24, 12), "\n", ""), "brianrackle@sandbox-codelima")
	}, "bash prompt to appear")

	for _, width := range []int{28, 32, 40, 48, 56, 64, 72, 80} {
		ghostty.Resize(width, 12)
		time.Sleep(50 * time.Millisecond)
	}
	wide := renderSnapshot(80, 12)

	got := strings.Join(nonEmptyRenderedLines(wide), "\n")
	want := strings.Join([]string{
		"brianrackle@sandbox-codelima-codex-codelima-codex-node-test-019d2fff:/Users/bria",
		"nrackle/Projects/codelima$",
	}, "\n")
	if got != want {
		t.Fatalf("rendered terminal after width growth = %q, want %q", got, want)
	}
}

func TestGhosttyTerminalWidthGrowthDoesNotInjectFormFeed(t *testing.T) {
	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
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

	ghostty.Resize(24, 12)
	cmd := exec.Command("/bin/sh", "-c", "stty echoctl 2>/dev/null || true; printf 'ready> '; cat")
	cmd.Env = append(os.Environ(), "TERM="+tuiEmbeddedTermEnv)
	if err := ghostty.Start(cmd); err != nil {
		t.Fatalf("ghostty.Start() error = %v", err)
	}

	waitForCondition(t, 5*time.Second, func() bool {
		return strings.Contains(renderSnapshot(24, 12), "ready>")
	}, "test process prompt to appear")

	ghostty.Resize(80, 12)
	time.Sleep(100 * time.Millisecond)
	if got := renderSnapshot(80, 12); strings.Contains(got, "^L") {
		t.Fatalf("width growth injected form feed into the PTY: %q", got)
	}
}

func TestGhosttyTerminalShiftEnterDoesNotLeakModifyOtherKeysSequenceAtBashPrompt(t *testing.T) {
	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
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

func TestGhosttyTerminalHandoffTransfersPTYAndRollbackResumes(t *testing.T) {
	base, err := newGhosttyTUITerminal("handoff-old", func(vaxis.Event) {})
	if err != nil {
		t.Fatalf("newGhosttyTUITerminal() error = %v", err)
	}
	old := base.(*ghosttyTUITerminal)
	cmd := exec.Command("/bin/sh")
	if err := old.Start(cmd); err != nil {
		old.Close()
		t.Fatalf("Start() error = %v", err)
	}
	old.SendInput([]byte("printf before-handoff\\n\n"))
	waitForCondition(t, 5*time.Second, func() bool { return strings.Contains(old.ReadRecent(ReadText).Text, "before-handoff") }, "pre-handoff output")

	state := old.BeginHandoff()
	if state.Err != nil || state.PTY == nil || state.ChildPID <= 0 {
		old.Close()
		t.Fatalf("BeginHandoff() = %#v", state)
	}
	adopted, err := adoptGhosttyTUITerminal("handoff-new", func(vaxis.Event) {}, state.PTY, state.ChildPID, state.Cols, state.Rows, state.Replay)
	if err != nil {
		_ = state.PTY.Close()
		_ = old.RollbackHandoff()
		old.Close()
		t.Fatalf("adoptGhosttyTUITerminal() error = %v", err)
	}
	old.ReleaseAfterHandoff()
	adopted.(*ghosttyTUITerminal).ActivateAfterHandoff()
	adopted.SendInput([]byte("printf after-handoff\\n\n"))
	waitForCondition(t, 5*time.Second, func() bool { return strings.Contains(adopted.ReadRecent(ReadText).Text, "after-handoff") }, "post-handoff output")
	adopted.Close()

	rollbackBase, err := newGhosttyTUITerminal("handoff-rollback", func(vaxis.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	rollback := rollbackBase.(*ghosttyTUITerminal)
	if err := rollback.Start(exec.Command("/bin/sh")); err != nil {
		rollback.Close()
		t.Fatal(err)
	}
	rollbackState := rollback.BeginHandoff()
	if rollbackState.Err != nil {
		rollback.Close()
		t.Fatal(rollbackState.Err)
	}
	_ = rollbackState.PTY.Close()
	if err := rollback.RollbackHandoff(); err != nil {
		rollback.Close()
		t.Fatal(err)
	}
	rollback.SendInput([]byte("printf rollback-ok\\n\n"))
	waitForCondition(t, 5*time.Second, func() bool { return strings.Contains(rollback.ReadRecent(ReadText).Text, "rollback-ok") }, "rollback output")
	rollback.Close()
}

func TestGhosttyTerminalHandoffReplaysAtCapturedGeometry(t *testing.T) {
	ptyFile, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open null PTY stand-in: %v", err)
	}

	const (
		cols = 12
		rows = 4
	)
	adopted, err := adoptGhosttyTUITerminal(
		"handoff-geometry",
		func(vaxis.Event) {},
		ptyFile,
		0,
		cols,
		rows,
		[]byte("abcdefghijklmnop\r\nsecond"),
	)
	if err != nil {
		_ = ptyFile.Close()
		t.Fatalf("adoptGhosttyTUITerminal() error = %v", err)
	}
	t.Cleanup(adopted.Close)

	result := adopted.Snapshot()
	if result.Err != nil {
		t.Fatalf("Snapshot() error = %v", result.Err)
	}
	if result.Snapshot.Cols != cols || result.Snapshot.Rows != rows {
		t.Fatalf("snapshot geometry = %dx%d, want %dx%d", result.Snapshot.Cols, result.Snapshot.Rows, cols, rows)
	}
	lines := strings.Split(daemonSnapshotText(daemonSnapshot(result.Snapshot)), "\n")
	want := []string{"abcdefghijkl", "mnop", "second", ""}
	if !slices.Equal(lines, want) {
		t.Fatalf("handoff replay lines = %#v, want %#v", lines, want)
	}
}

func TestGhosttyTerminalHandoffRejectsInvalidGeometry(t *testing.T) {
	ptyFile, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("open null PTY stand-in: %v", err)
	}
	defer func() { _ = ptyFile.Close() }()

	adopted, err := adoptGhosttyTUITerminal("handoff-invalid-geometry", func(vaxis.Event) {}, ptyFile, 0, 0, 24, nil)
	if err == nil {
		adopted.Close()
		t.Fatal("adoptGhosttyTUITerminal() accepted zero columns")
	}
	if !strings.Contains(err.Error(), "invalid geometry 0x24") {
		t.Fatalf("adoptGhosttyTUITerminal() error = %v", err)
	}
}

func nonEmptyRenderedLines(text string) []string {
	lines := strings.Split(text, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		filtered = append(filtered, line)
	}
	return filtered
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
			name: "shift-enter-matches-legacy",
			key: vaxis.Key{
				Keycode:   vaxis.KeyEnter,
				Modifiers: vaxis.ModShift,
			},
			want: "\r",
		},
		{
			name: "paste-text",
			key: vaxis.Key{
				Text:      "hello\nthere",
				EventType: vaxis.EventPaste,
			},
			want: "hello\nthere",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := encodeTUITerminalKeyWithGhostty(tc.key, encoder, nil, tc.applicationKeypad, tc.cursorKeysApplication)
			if got != tc.want {
				t.Fatalf("encodeTUITerminalKeyWithGhostty() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGhosttyKeyEncoderUsesTerminalModifyOtherKeysMode(t *testing.T) {
	t.Parallel()

	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}
	if ghostty.keyEncoder == nil {
		t.Skip("ghostty key encoder unavailable in this test environment")
	}

	ghostty.ingestPTY([]byte("\x1b[>4;2m"))

	got := encodeTUITerminalKeyWithGhostty(vaxis.Key{
		Keycode:   vaxis.KeyEnter,
		Modifiers: vaxis.ModShift,
	}, ghostty.keyEncoder, ghostty.term, false, false)
	if got != "\x1b[27;2;13~" {
		t.Fatalf("modifyOtherKeys shift-enter encoding = %q, want %q", got, "\x1b[27;2;13~")
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
	}, encoder, nil, false, false)
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

	got := encodeTUITerminalKeyWithGhostty(vaxis.Key{Keycode: vaxis.KeyF26}, encoder, nil, false, false)
	if got != "\x1B[1;5Q" {
		t.Fatalf("unsupported Ghostty key should fall back to legacy encoding, got %q", got)
	}
}

func TestGhosttyMouseEncoderMatchesLegacyCommonSequences(t *testing.T) {
	cases := []struct {
		name             string
		setup            string
		mouse            vaxis.Mouse
		mouseButtonsDown int
		want             string
	}{
		{
			name:  "sgr-press",
			setup: "\x1b[?1000h\x1b[?1006h",
			mouse: vaxis.Mouse{
				Col:       4,
				Row:       2,
				Button:    vaxis.MouseLeftButton,
				EventType: vaxis.EventPress,
			},
			want: "\x1b[<0;5;3M",
		},
		{
			name:  "sgr-release",
			setup: "\x1b[?1000h\x1b[?1006h",
			mouse: vaxis.Mouse{
				Col:       4,
				Row:       2,
				Button:    vaxis.MouseLeftButton,
				EventType: vaxis.EventRelease,
			},
			want: "\x1b[<0;5;3m",
		},
		{
			name:  "drag-motion",
			setup: "\x1b[?1002h\x1b[?1006h",
			mouse: vaxis.Mouse{
				Col:       4,
				Row:       2,
				Button:    vaxis.MouseLeftButton,
				EventType: vaxis.EventMotion,
			},
			mouseButtonsDown: 1,
			want:             "\x1b[<32;5;3M",
		},
		{
			name:  "any-motion-without-button",
			setup: "\x1b[?1003h\x1b[?1006h",
			mouse: vaxis.Mouse{
				Col:       4,
				Row:       2,
				Button:    vaxis.MouseNoButton,
				EventType: vaxis.EventMotion,
			},
			want: "\x1b[<35;5;3M",
		},
		{
			name:  "wheel-up",
			setup: "\x1b[?1000h\x1b[?1006h",
			mouse: vaxis.Mouse{
				Col:       4,
				Row:       2,
				Button:    vaxis.MouseWheelUp,
				EventType: vaxis.EventPress,
			},
			want: "\x1b[<64;5;3M",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
			if err != nil {
				t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
			}
			defer terminal.Close()

			ghostty, ok := terminal.(*ghosttyTUITerminal)
			if !ok {
				t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
			}
			if ghostty.mouseEncoder == nil {
				t.Skip("ghostty mouse encoder unavailable in this test environment")
			}

			ghostty.ingestPTY([]byte(tc.setup))
			got, handled := ghostty.mouseEncoder.Encode(tc.mouse, ghostty.term, ghostty.cols, ghostty.rows, tc.mouseButtonsDown)
			if !handled {
				t.Fatalf("expected ghostty mouse encoder to handle %#v", tc.mouse)
			}
			if got != tc.want {
				t.Fatalf("ghostty mouse encoding = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGhosttyMouseEncoderFallsBackWithoutEncoder(t *testing.T) {
	t.Parallel()

	mouse := vaxis.Mouse{
		Col:       4,
		Row:       2,
		Button:    vaxis.MouseLeftButton,
		EventType: vaxis.EventPress,
	}
	got := encodeTUITerminalMouseWithGhostty(mouse, nil, nil, 80, 24, 0, true, true, true)
	if got != "\x1b[<0;5;3M" {
		t.Fatalf("fallback mouse encoding = %q, want %q", got, "\x1b[<0;5;3M")
	}
}

func TestGhosttyTerminalWheelScrollUsesGhosttyViewportState(t *testing.T) {
	terminal, err := newGhosttyTUITerminal("node-root", func(vaxis.Event) {})
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	var output strings.Builder
	for i := 0; i < 64; i++ {
		fmt.Fprintf(&output, "line %02d\n", i)
	}
	ghostty.ingestPTY([]byte(output.String()))

	ghostty.mu.Lock()
	defer ghostty.mu.Unlock()

	initial, ok := ghostty.scrollbarLocked()
	if !ok {
		t.Fatal("expected Ghostty scrollbar state")
	}
	if initial.total <= initial.length {
		t.Fatalf("expected scrollback, got total=%d length=%d", initial.total, initial.length)
	}
	if !ghostty.viewportAtBottomLocked() {
		t.Fatal("expected viewport to start at bottom")
	}
	if !ghostty.handleWheelLocked(vaxis.MouseWheelUp) {
		t.Fatal("expected wheel-up to scroll Ghostty viewport")
	}

	scrolled, ok := ghostty.scrollbarLocked()
	if !ok {
		t.Fatal("expected Ghostty scrollbar state after scrolling")
	}
	if scrolled.offset >= initial.offset {
		t.Fatalf("expected viewport offset to move upward, got initial=%d scrolled=%d", initial.offset, scrolled.offset)
	}
	if ghostty.viewportAtBottomLocked() {
		t.Fatal("expected viewport to be away from bottom after scrolling")
	}

	ghostty.scrollViewportBottomLocked()
	reset, ok := ghostty.scrollbarLocked()
	if !ok {
		t.Fatal("expected Ghostty scrollbar state after resetting viewport")
	}
	if !ghostty.viewportAtBottomLocked() {
		t.Fatal("expected viewport to return to bottom")
	}
	if reset.offset+reset.length < reset.total {
		t.Fatalf("expected bottom-aligned scrollbar state, got offset=%d length=%d total=%d", reset.offset, reset.length, reset.total)
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

func TestGhosttyFocusReportsAreGatedByFocusMode(t *testing.T) {
	terminal, err := newGhosttyTUITerminal("node-root", nil)
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	target := &ghosttyFakePTYWriteTarget{}
	ghostty.mu.Lock()
	ghostty.ptyWriter = newGhosttyPTYWriter(target, func(fd int) error { return nil }, nil)
	ghostty.mu.Unlock()

	ghostty.Focus()
	ghostty.Blur()

	if got := target.String(); got != "" {
		t.Fatalf("focus reports should stay silent before DECSET 1004, got %q", got)
	}
}

func TestGhosttyFocusReportsUseGhosttyEncodingWhenModeEnabled(t *testing.T) {
	terminal, err := newGhosttyTUITerminal("node-root", nil)
	if err != nil {
		t.Skipf("ghostty terminal unavailable in this test environment: %v", err)
	}
	defer terminal.Close()

	ghostty, ok := terminal.(*ghosttyTUITerminal)
	if !ok {
		t.Fatalf("expected ghostty terminal implementation, got %T", terminal)
	}

	target := &ghosttyFakePTYWriteTarget{}
	ghostty.mu.Lock()
	ghostty.ptyWriter = newGhosttyPTYWriter(target, func(fd int) error { return nil }, nil)
	ghostty.mu.Unlock()

	ghostty.ingestPTY([]byte("\x1b[?1004h"))
	ghostty.Focus()
	waitForCondition(t, time.Second, func() bool {
		return target.String() == "\x1b[I"
	}, "focus gained report to flush")

	ghostty.Blur()
	waitForCondition(t, time.Second, func() bool {
		return target.String() == "\x1b[I\x1b[O"
	}, "focus lost report to flush")
}

func TestGhosttyTerminalAnswersColorSchemeQueryFromStoredTheme(t *testing.T) {
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

	ghostty.mu.Lock()
	ghostty.setColorThemeModeLocked(vaxis.LightMode)
	ghostty.mu.Unlock()

	stderrOutput := captureGhosttyProcessStderr(t, func() {
		ghostty.ingestPTY([]byte("\x1b[?996n"))
	})
	if strings.TrimSpace(stderrOutput) != "" {
		t.Fatalf("expected no Ghostty parser warnings, got %q", stderrOutput)
	}

	if got, want := ghostty.readPendingResponses(), "\x1b[?997;2n"; got != want {
		t.Fatalf("color-scheme query response = %q, want %q", got, want)
	}
}

func TestGhosttyTerminalReportsColorThemeUpdateWhenModeEnabled(t *testing.T) {
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

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("open pipe for terminal update guard: %v", err)
	}
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})

	ghostty.mu.Lock()
	ghostty.pty = writer
	ghostty.mu.Unlock()

	ghostty.ingestPTY([]byte("\x1b[?2031h"))
	stderrOutput := captureGhosttyProcessStderr(t, func() {
		ghostty.Update(vaxis.ColorThemeUpdate{Mode: vaxis.DarkMode})
	})
	if strings.TrimSpace(stderrOutput) != "" {
		t.Fatalf("expected no Ghostty parser warnings, got %q", stderrOutput)
	}

	if got, want := ghostty.readPendingResponses(), "\x1b[?997;1n"; got != want {
		t.Fatalf("color-theme update report = %q, want %q", got, want)
	}
}

func TestGhosttyTerminalAnswersPrimaryDeviceAttributesQuery(t *testing.T) {
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
		ghostty.ingestPTY([]byte("\x1b[c"))
	})
	if strings.TrimSpace(stderrOutput) != "" {
		t.Fatalf("expected no Ghostty parser warnings, got %q", stderrOutput)
	}

	if got, want := ghostty.readPendingResponses(), "\x1b[?62;18;22c"; got != want {
		t.Fatalf("primary device attributes response = %q, want %q", got, want)
	}
}

func TestGhosttyTerminalAnswersXtwinopsSizeQuery(t *testing.T) {
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
		ghostty.ingestPTY([]byte("\x1b[18t"))
	})
	if strings.TrimSpace(stderrOutput) != "" {
		t.Fatalf("expected no Ghostty parser warnings, got %q", stderrOutput)
	}

	if got, want := ghostty.readPendingResponses(), "\x1b[8;24;80t"; got != want {
		t.Fatalf("XTWINOPS size query response = %q, want %q", got, want)
	}
}

func TestGhosttyTerminalAnswersXtversionQuery(t *testing.T) {
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
		ghostty.ingestPTY([]byte("\x1b[>q"))
	})
	if strings.TrimSpace(stderrOutput) != "" {
		t.Fatalf("expected no Ghostty parser warnings, got %q", stderrOutput)
	}

	if got, want := ghostty.readPendingResponses(), "\x1bP>|codelima\x1b\\"; got != want {
		t.Fatalf("XTVERSION response = %q, want %q", got, want)
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
	runGhosttySttyRawPrompt(t, exec.Command("bash", script.scriptPath), script.readyPath, script.resultPath)
}

func TestNestedPTYScriptCommandUsesPlatformSpecificScriptArgs(t *testing.T) {
	t.Parallel()

	scriptPath := "/tmp/path with spaces/stty-raw-prompt.sh"
	tests := []struct {
		name string
		goos string
		want []string
	}{
		{
			name: "darwin",
			goos: "darwin",
			want: []string{"script", "-q", "/dev/null", "bash", scriptPath},
		},
		{
			name: "linux",
			goos: "linux",
			want: []string{"script", "-q", "-c", "bash " + shellQuote(scriptPath), "/dev/null"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd := newNestedPTYScriptCommandForOS(tt.goos, scriptPath)
			if !reflect.DeepEqual(cmd.Args, tt.want) {
				t.Fatalf("newNestedPTYScriptCommandForOS(%q) args = %v, want %v", tt.goos, cmd.Args, tt.want)
			}
		})
	}
}

func TestGhosttyTerminalRoundTripsSttyRawPromptThroughNestedPTY(t *testing.T) {
	if _, err := exec.LookPath("script"); err != nil {
		t.Skipf("script utility unavailable: %v", err)
	}

	script := newSttyRawPromptScript(t)
	runGhosttySttyRawPrompt(t, newNestedPTYScriptCommand(script.scriptPath), script.readyPath, script.resultPath)
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
	scriptPath string
	readyPath  string
	resultPath string
}

func newSttyRawPromptScript(t *testing.T) sttyRawPromptScript {
	t.Helper()

	tempDir := t.TempDir()
	readyPath := filepath.Join(tempDir, "ready")
	resultPath := filepath.Join(tempDir, "result")
	errorPath := filepath.Join(tempDir, "restore.err")
	scriptPath := filepath.Join(tempDir, "stty-raw-prompt.sh")
	body := fmt.Sprintf(`
save_state="$(/bin/stty -g)"
printf ready > %q
/bin/stty raw -echo
IFS='' read -r -n 1 -d '' c
if /bin/stty "${save_state}" 2>%q; then
  printf 'ok:%%s' "$c" > %q
else
  printf 'fail:%%s\nstate=%%s\n' "$(/bin/cat %q)" "${save_state}" > %q
fi
`, readyPath, errorPath, resultPath, errorPath, resultPath)
	if err := os.WriteFile(scriptPath, []byte(body), 0o700); err != nil {
		t.Fatalf("WriteFile(stty raw prompt script) error = %v", err)
	}
	return sttyRawPromptScript{
		scriptPath: scriptPath,
		readyPath:  readyPath,
		resultPath: resultPath,
	}
}

func newNestedPTYScriptCommand(scriptPath string) *exec.Cmd {
	return newNestedPTYScriptCommandForOS(runtime.GOOS, scriptPath)
}

func newNestedPTYScriptCommandForOS(goos string, scriptPath string) *exec.Cmd {
	if goos == "linux" {
		return exec.Command("script", "-q", "-c", "bash "+shellQuote(scriptPath), "/dev/null")
	}
	return exec.Command("script", "-q", "/dev/null", "bash", scriptPath)
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

// TestGhosttyStderrCaptureDoesNotDeadlock exercises the work-item-0.5 change
// that replaced the /dev/null stderr sink in withGhosttyStderrSuppressed with a
// pipe drained by a logging goroutine. It feeds enough warning-triggering bridge
// calls to overflow a pipe buffer if nothing drained it, and asserts the calls
// complete: a stalled reader would block libghostty's writes to fd 2 and hang.
// Exact log content is not assertable (libghostty may print nothing), so this
// asserts the plumbing, not the output.
func TestGhosttyStderrCaptureDoesNotDeadlock(t *testing.T) {
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

	// Point the libghostty capture at a debug-enabled sink so the drain
	// goroutine actually formats each captured line; restore afterwards.
	original := packageLog()
	setPackageLogger(newTextLogger(io.Discard, parseLogLevel("debug")))
	t.Cleanup(func() { setPackageLogger(original) })

	done := make(chan struct{})
	go func() {
		for i := 0; i < 2000; i++ {
			ghostty.ingestPTY([]byte("\x1b[?5m"))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("ghostty stderr capture deadlocked: warning-generating bridge calls did not complete")
	}
}
