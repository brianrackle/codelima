//go:build darwin || linux

package codelima

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"git.sr.ht/~rockorager/vaxis"
)

func TestShutdownTerminalProcessEscalatesToSIGKILLForStubbornGroup(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", `trap '' HUP TERM; sleep 300 & sleep 300`)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start() error = %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	})

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	closedIO := false
	if err := shutdownTerminalProcess(cmd.Process.Pid, func() { closedIO = true }, done); err != nil {
		t.Fatalf("shutdownTerminalProcess() error = %v", err)
	}
	if !closedIO {
		t.Fatal("expected closeIO to run before signal escalation")
	}
	select {
	case <-done:
	default:
		t.Fatal("direct child was not reaped when shutdownTerminalProcess returned")
	}
	if err := syscall.Kill(-cmd.Process.Pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("process group %d still alive, kill(-pgid, 0) = %v", cmd.Process.Pid, err)
	}
}

func TestShutdownTerminalProcessTreatsExitedProcessAsSuccess(t *testing.T) {
	trueBinary, err := exec.LookPath("true")
	if err != nil {
		t.Fatalf("look up true executable: %v", err)
	}
	cmd := exec.Command(trueBinary)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start() error = %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("cmd.Wait() error = %v", err)
	}
	done := make(chan struct{})
	close(done)

	started := time.Now()
	if err := shutdownTerminalProcess(cmd.Process.Pid, nil, done); err != nil {
		t.Fatalf("shutdownTerminalProcess() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("shutdownTerminalProcess() took %v for an already-exited process, want a fast ESRCH return", elapsed)
	}
}

func TestShutdownTerminalProcessIgnoresNonPositivePIDs(t *testing.T) {
	closeCalls := 0
	for _, pid := range []int{0, -42} {
		if err := shutdownTerminalProcess(pid, func() { closeCalls++ }, nil); err != nil {
			t.Fatalf("shutdownTerminalProcess(%d) error = %v", pid, err)
		}
	}
	if closeCalls != 2 {
		t.Fatalf("closeIO calls = %d, want 2", closeCalls)
	}
}

func TestVaxisTerminalCloseKillsGrandchildProcesses(t *testing.T) {
	terminal := newTUIVaxisTerminal("node-root", func(vaxis.Event) {})
	if _, ok := terminal.(*vaxisTUITerminal); !ok {
		t.Fatalf("expected vaxis terminal implementation, got %T", terminal)
	}

	pidPath := filepath.Join(t.TempDir(), "grandchild.pid")
	script := fmt.Sprintf(
		`trap '' HUP; sleep 300 & echo "$!" > %s; exec sleep 300`,
		shellQuote(pidPath),
	)
	cmd := exec.Command("/bin/sh", "-c", script)
	if err := terminal.Start(cmd); err != nil {
		t.Fatalf("terminal.Start() error = %v", err)
	}

	grandchildPID := 0
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(pidPath)
		if err == nil {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw)))
			if convErr == nil && pid > 0 {
				grandchildPID = pid
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if grandchildPID == 0 {
		terminal.Close()
		t.Fatal("timed out waiting for the grandchild pid file")
	}
	t.Cleanup(func() {
		_ = syscall.Kill(grandchildPID, syscall.SIGKILL)
	})

	terminal.Close()

	waitDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(waitDeadline) {
		if err := syscall.Kill(grandchildPID, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("grandchild %d is still alive 3s after Close", grandchildPID)
}
