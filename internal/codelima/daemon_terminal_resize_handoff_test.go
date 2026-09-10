//go:build cgo && (darwin || linux)

package codelima

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestIsolatedTerminalResizeKeepsPTYNonblockingForHandoff(t *testing.T) {
	terminal := newLazyVariantTestTerminal(t, "resize-handoff")
	command := exec.Command("/bin/sh", "-c", `stty -echo; printf 'ready\n'; while IFS= read -r line; do printf '%s\n' "$line"; done`)
	if err := terminal.Start(command); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, 5*time.Second, func() bool {
		return strings.Contains(terminal.ReadVisible(ReadText).Text, "ready")
	}, "shell ready")
	terminal.mu.Lock()
	fd, err := ghosttyPTYFileDescriptor(terminal.pty)
	shellPID := terminal.childPID
	terminal.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range [][4]int{{110, 24, 10, 20}, {80, 20, 10, 20}, {120, 30, 12, 24}} {
		if err := terminal.ResizePixels(size[0], size[1], size[2], size[3]); err != nil {
			t.Fatal(err)
		}
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
		if err != nil || flags&unix.O_NONBLOCK == 0 {
			t.Fatalf("resize changed PTY to blocking: flags=%#x err=%v", flags, err)
		}
		actual, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
		if err != nil {
			t.Fatal(err)
		}
		if int(actual.Col) != size[0] || int(actual.Row) != size[1] || int(actual.Xpixel) != size[0]*size[2] || int(actual.Ypixel) != size[1]*size[3] {
			t.Fatalf("PTY size = %+v, want %v", actual, size)
		}
	}
	terminal.SendInput([]byte("before-handoff\n"))
	waitForCondition(t, 5*time.Second, func() bool {
		return strings.Contains(terminal.ReadVisible(ReadText).Text, "before-handoff")
	}, "output before handoff")
	result := make(chan handoffTerminalState, 1)
	go func() { result <- terminal.BeginHandoff() }()
	select {
	case state := <-result:
		if state.Err != nil || state.PTY == nil {
			t.Fatalf("handoff failed: %v", state.Err)
		}
		defer func() { _ = state.PTY.Close() }()
		if state.ChildPID != shellPID || state.Cols != 120 || state.Rows != 30 {
			t.Fatalf("handoff lost shell/geometry: pid=%d size=%dx%d", state.ChildPID, state.Cols, state.Rows)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handoff did not stop the idle PTY reader after resizing")
	}
	if err := terminal.RollbackHandoff(); err != nil {
		t.Fatal(err)
	}
	terminal.SendInput([]byte("after-handoff\n"))
	waitForCondition(t, 5*time.Second, func() bool {
		return strings.Contains(terminal.ReadVisible(ReadText).Text, "after-handoff")
	}, "same shell output after rollback")
}
