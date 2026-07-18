package codelima

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

const sdkSSHServeCommand = "__sdk-ssh-serve"

// SandboxSSHRuntime is the optional Microsandbox capability used by the
// daemon's dynamic HTTP forwarder. Keeping it separate from SandboxClient
// prevents forwarding concerns from leaking into ordinary node operations.
type SandboxSSHRuntime interface {
	AuthorizeSSHKey(context.Context, string) error
	OpenSSHTransport(context.Context, string) (io.ReadWriteCloser, error)
}

// AuthorizeSSHKey records the daemon-owned public key for subsequent helper
// processes. The SDK accepts authorized keys per SSH server, so unlike the old
// CLI integration there is no global authorization mutation.
func (c *SDKSandboxClient) AuthorizeSSHKey(_ context.Context, publicKeyPath string) error {
	path, err := filepath.Abs(publicKeyPath)
	if err != nil {
		return invalidArgument("failed to resolve forwarding public key path", map[string]any{"path": publicKeyPath})
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return invalidArgument("forwarding public key file is unavailable", map[string]any{"path": path})
	}
	c.transportMu.Lock()
	c.publicKeyPath = path
	c.transportMu.Unlock()
	return nil
}

func (c *SDKSandboxClient) OpenSSHTransport(ctx context.Context, sandboxName string) (io.ReadWriteCloser, error) {
	if err := validateSandboxName(sandboxName); err != nil {
		return nil, err
	}
	c.transportMu.RLock()
	publicKeyPath := c.publicKeyPath
	executable := c.executable
	c.transportMu.RUnlock()
	if publicKeyPath == "" {
		return nil, preconditionFailed("dynamic forwarding key has not been prepared", nil)
	}
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return nil, dependencyUnavailable("resolve codelima executable for SDK SSH helper", err, nil)
		}
	}

	command := exec.CommandContext(ctx, executable, sdkSSHServeCommand,
		"--sandbox", sandboxName,
		"--authorized-keys", publicKeyPath,
	)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	var stderr bytes.Buffer
	command.Stderr = io.MultiWriter(&stderr, writerOrDiscard(c.Stderr))
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, externalCommandFailed("start codelima SDK SSH helper", err, map[string]any{"sandbox_name": sandboxName})
	}
	transport := &execSSHTransport{stdin: stdin, stdout: stdout, command: command, done: make(chan error, 1), stderr: &stderr}
	go func() { transport.done <- command.Wait() }()
	return transport, nil
}

func writerOrDiscard(writer io.Writer) io.Writer {
	if writer == nil {
		return io.Discard
	}
	return writer
}

type execSSHTransport struct {
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	command *exec.Cmd
	done    chan error
	stderr  *bytes.Buffer
	once    sync.Once
}

func (t *execSSHTransport) Read(buffer []byte) (int, error)  { return t.stdout.Read(buffer) }
func (t *execSSHTransport) Write(buffer []byte) (int, error) { return t.stdin.Write(buffer) }

func (t *execSSHTransport) Close() error {
	var closeErr error
	t.once.Do(func() {
		closeErr = errors.Join(t.stdin.Close(), t.stdout.Close())
		if t.command.Process != nil {
			_ = t.command.Process.Kill()
		}
		select {
		case waitErr := <-t.done:
			if waitErr != nil && t.stderr != nil && t.stderr.Len() != 0 {
				closeErr = errors.Join(closeErr, fmt.Errorf("SDK SSH helper: %s", t.stderr.String()))
			}
		case <-time.After(5 * time.Second):
			closeErr = errors.Join(closeErr, errors.New("timed out closing codelima SDK SSH helper"))
		}
	})
	return closeErr
}
