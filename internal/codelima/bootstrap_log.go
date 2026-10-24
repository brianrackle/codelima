package codelima

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const bootstrapLogMaxBytes = 4 << 20

// Chunk writes before the reusable rotator so even one large output write
// cannot exceed the per-generation cap. stdout/stderr share its mutex.
type bootstrapLogWriter struct{ writer *rotatingLogWriter }

func (w bootstrapLogWriter) Write(data []byte) (int, error) {
	total := 0
	chunk := int64(64 << 10)
	if w.writer.maxBytes > 0 {
		chunk = min(chunk, w.writer.maxBytes)
	}
	for len(data) > 0 {
		count := min(len(data), int(chunk))
		n, err := w.writer.write(data[:count], true)
		total += n
		data = data[n:]
		if err != nil {
			return total, err
		}
		if n != count {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

func (s *Service) runLoggedGuestCommand(ctx context.Context, node Node, command, phase string) error {
	if strings.TrimSpace(command) == "" {
		return nil
	}
	path := filepath.Join(s.store.nodeDir(node.ID), "bootstrap.log")
	writer, err := newRotatingLogWriterMode(path, bootstrapLogMaxBytes, 0o600)
	if err != nil {
		return fmt.Errorf("open bootstrap output: %w", err)
	}
	var output io.Writer = bootstrapLogWriter{writer: writer}
	if s.operationOutput != nil {
		output = io.MultiWriter(output, s.operationOutput)
	}
	started := s.now()
	if _, err = fmt.Fprintf(output, "\n## %s · %s\n$ %s\n", started.Format(time.RFC3339Nano), phase, command); err != nil {
		_ = writer.Close()
		return fmt.Errorf("record bootstrap command: %w", err)
	}
	runErr := s.runGuestCommandWithStreams(ctx, node, command, ShellStreams{Stdout: output, Stderr: output})
	status := "completed"
	if runErr != nil {
		status = "failed: " + runErr.Error()
		var exit *exec.ExitError
		if errors.As(runErr, &exit) {
			status = fmt.Sprintf("failed · exit %d: %s", exit.ExitCode(), runErr)
		}
	}
	_, writeErr := fmt.Fprintf(output, "\n## %s · %s · %s · %s\n", s.now().Format(time.RFC3339Nano), phase, status, s.now().Sub(started).Round(time.Millisecond))
	return errors.Join(runErr, writeErr, writer.Close())
}
