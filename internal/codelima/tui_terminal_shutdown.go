//go:build darwin || linux

package codelima

import (
	"errors"
	"fmt"
	"syscall"
	"time"
)

const (
	// terminalShutdownSignalGrace is how long each shutdown stage waits for
	// the child's process group to exit before escalating to the next
	// signal. Shells save history on SIGHUP, so the grace matters.
	terminalShutdownSignalGrace = 250 * time.Millisecond
	// terminalShutdownReapDeadline bounds the final wait after SIGKILL.
	terminalShutdownReapDeadline = 2 * time.Second
	// terminalShutdownPollInterval is the liveness polling cadence. Polling
	// uses kill(-pgid, 0), which works on both Linux and macOS (no /proc).
	terminalShutdownPollInterval = 20 * time.Millisecond
)

// shutdownTerminalProcess tears down an embedded-terminal child and every
// descendant that shares its process group. The child is started as a session
// leader (Setsid), so its process-group id equals pid and kill(-pid, sig)
// signals the whole group.
//
// Escalation: closeIO (writer first, then PTY master — closing the master
// hangs up the line, delivering SIGHUP to the foreground process group) →
// grace → SIGTERM to the group → grace → SIGKILL to the group → bounded reap.
//
// done, when non-nil, is closed once the caller's single reaper goroutine has
// returned from cmd.Wait; shutdownTerminalProcess never calls Wait itself.
// Pass done == nil when another owner reaps the direct child (the Vaxis
// fallback widget). A vanished process group (ESRCH) is success, not an error.
//
// Known limitation (documented, not solved here): a shell running job control
// can place jobs in process groups of their own inside the session; those
// escape a group kill of the leader's group. Our real launch chains today do
// not do that.
func shutdownTerminalProcess(pid int, closeIO func(), done <-chan struct{}) error {
	if closeIO != nil {
		closeIO()
	}
	if pid <= 0 {
		return nil
	}
	if awaitTerminalProcessGroupExit(pid, done, terminalShutdownSignalGrace) {
		return nil
	}
	if err := signalTerminalProcessGroup(pid, syscall.SIGTERM); err != nil {
		return err
	}
	if awaitTerminalProcessGroupExit(pid, done, terminalShutdownSignalGrace) {
		return nil
	}
	if err := signalTerminalProcessGroup(pid, syscall.SIGKILL); err != nil {
		return err
	}
	if awaitTerminalProcessGroupExit(pid, done, terminalShutdownReapDeadline) {
		return nil
	}
	return fmt.Errorf("terminal process group %d is still alive after SIGKILL", pid)
}

func signalTerminalProcessGroup(pid int, signal syscall.Signal) error {
	if err := syscall.Kill(-pid, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("signal terminal process group %d with %s: %w", pid, signal, err)
	}
	return nil
}

// awaitTerminalProcessGroupExit polls until the process group is empty, then
// waits for the direct child to be reaped so callers never leave a zombie.
// An empty group already implies the direct child's process-table entry is
// gone (a zombie still counts as a group member), so the done wait is only
// closing the gap to cmd.Wait's return.
func awaitTerminalProcessGroupExit(pid int, done <-chan struct{}, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if terminalProcessGroupGone(pid) {
			return terminalChildReaped(done, deadline)
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return false
		}
		interval := terminalShutdownPollInterval
		if remaining < interval {
			interval = remaining
		}
		time.Sleep(interval)
	}
}

func terminalProcessGroupGone(pid int) bool {
	return errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH)
}

func terminalChildReaped(done <-chan struct{}, deadline time.Time) bool {
	if done == nil {
		return true
	}
	remaining := time.Until(deadline)
	if remaining < terminalShutdownPollInterval {
		// The group is already empty; give the reaper goroutine at least one
		// beat to return from cmd.Wait even at the deadline edge.
		remaining = terminalShutdownPollInterval
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}
