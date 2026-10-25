package codelima

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const (
	diagnosticLogLimit     = 256 << 10
	diagnosticCommandLimit = 1 << 20
	diagnosticFileLimit    = 1 << 20
)

type diagnosticCommandResult struct {
	stdout, stderr []byte
	truncated      bool
	err            error
}

type diagnosticBuffer struct {
	buffer    bytes.Buffer
	truncated bool
}

func (b *diagnosticBuffer) Len() int      { return b.buffer.Len() }
func (b *diagnosticBuffer) Bytes() []byte { return b.buffer.Bytes() }

func (b *diagnosticBuffer) Write(data []byte) (int, error) {
	keep := min(len(data), max(0, diagnosticCommandLimit-b.Len()))
	_, _ = b.buffer.Write(data[:keep])
	b.truncated = b.truncated || keep < len(data)
	return len(data), nil
}

// Commands are read-only probes. Only their own process group is killed on a
// deadline; the daemon/renderer PID passed as an argument is never signalled.
func runDiagnosticCommand(ctx context.Context, name string, args ...string) diagnosticCommandResult {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.WaitDelay = time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	var out, errOut diagnosticBuffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	return diagnosticCommandResult{out.Bytes(), errOut.Bytes(), out.truncated || errOut.truncated, err}
}

func readDiagnosticFile(path string, limit int) ([]byte, bool, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("evidence source is not a regular file: %s", path)
	}
	// /proc exposes regular files with size zero; read a bounded stream rather
	// than using stat.Size to decide whether there is content.
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	truncated := len(data) > limit
	return data[:min(len(data), limit)], truncated, err
}
