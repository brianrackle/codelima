//go:build ignore

// tooling_lock runs an installer while holding a kernel flock. The descriptor
// survives exec, so a killed installer cannot release its lock while its build
// subprocess is still writing. Linux and macOS use the same ownership rule.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 3 {
		return fmt.Errorf("usage: tooling_lock <lock-file> <command> [args...]")
	}
	lock, err := os.OpenFile(os.Args[1], os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open installer lock: %w", err)
	}
	defer lock.Close()
	deadline := time.Now().Add(30 * time.Minute)
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			return fmt.Errorf("lock installer: %w", err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for installer lock %s", os.Args[1])
		}
		time.Sleep(25 * time.Millisecond)
	}
	command, err := exec.LookPath(os.Args[2])
	if err != nil {
		return err
	}
	// os.OpenFile uses close-on-exec by default. This descriptor is deliberately
	// inherited by the installer and its children to retain ownership on crash.
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, lock.Fd(), syscall.F_SETFD, 0); errno != 0 {
		return fmt.Errorf("retain installer lock across exec: %w", errno)
	}
	environment := append(os.Environ(), "CODELIMA_INSTALL_LOCK_HELD="+os.Args[1])
	return syscall.Exec(command, os.Args[2:], environment)
}
