//go:build darwin || linux

package codelima

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
	"github.com/brianrackle/codelima/internal/codelima/daemonclient"
	"github.com/brianrackle/codelima/internal/terminalio"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// Reset only modes the attached guest may have changed. This deliberately
// avoids RIS, which can erase the user's primary-screen scrollback.
const rawAttachReset = "\x1b[?1049l\x1b[!p\x1b[0m\x1b[?1;9;1000;1002;1003;1004;1005;1006;1015;1007;2004;2026;2031l\x1b[?7;25h\x1b[>4;0m\x1b[<999u\x1b[=0u\x1b]8;;\x1b\\\x1b[r\x1b(B\x1b)B\x0f"

func attachDaemonTerminal(ctx context.Context, client *daemonclient.Client, terminalID string) (bool, error) {
	fd, err := unix.Open("/dev/tty", unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return false, fmt.Errorf("open terminal for attachment: %w", err)
	}
	file := os.NewFile(uintptr(fd), "codelima-raw-attachment")
	defer func() { _ = file.Close() }()
	return attachDaemonTerminalFile(ctx, client, terminalID, file)
}

func attachDaemonTerminalFile(ctx context.Context, client *daemonclient.Client, terminalID string, file *os.File) (leftRunning bool, err error) {
	fd, err := terminalio.Descriptor(file)
	if err != nil {
		return false, err
	}
	if !term.IsTerminal(fd) {
		return false, errors.New("raw attachment requires an interactive terminal")
	}
	if err := takeTUIDaemonInput(ctx, client); err != nil {
		return false, err
	}
	cols, rows, err := term.GetSize(fd)
	if err != nil {
		return false, err
	}
	resize := func(width, height int) error {
		return client.Call(ctx, "terminal.resize", map[string]any{"terminal_id": terminalID, "cols": width, "rows": height}, nil)
	}
	if err := resize(cols, rows); err != nil {
		return false, err
	}
	var initial daemon.TerminalAttachResult
	if err := client.Call(ctx, "terminal.attach", daemon.TerminalAttachParams{TerminalID: terminalID}, &initial); err != nil {
		return false, err
	}
	params := daemon.TerminalAttachParams{TerminalID: terminalID, AttachmentID: initial.AttachmentID, After: initial.Through}
	defer func() {
		detachCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = client.Call(detachCtx, "terminal.detach", params, nil)
	}()
	previous, err := term.MakeRaw(fd)
	if err != nil {
		return false, err
	}
	defer func() {
		resetCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = writeRawAttachOutput(resetCtx, fd, []byte(rawAttachReset+"\r\n"))
		err = errors.Join(err, term.Restore(fd, previous))
	}()
	if err := writeRawAttachOutput(ctx, fd, []byte(rawAttachReset+"\x1b[2J\x1b[H")); err != nil {
		return false, err
	}
	if err := writeRawAttachOutput(ctx, fd, initial.Data); err != nil {
		return false, err
	}

	streamCtx, cancel := context.WithCancel(ctx)
	var detached atomic.Bool
	inputDone := make(chan error, 1)
	go func() {
		inputErr := readRawAttachInput(streamCtx, fd, func(data []byte) error {
			return client.Call(streamCtx, "terminal.send_input", map[string]any{"terminal_id": terminalID, "data": data}, nil)
		}, &detached)
		inputDone <- inputErr
		cancel()
	}()
	defer func() {
		cancel()
		inputErr := <-inputDone
		if !errors.Is(inputErr, context.Canceled) {
			err = errors.Join(err, inputErr)
		}
	}()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	nextSize := time.Now().Add(200 * time.Millisecond)
	for {
		select {
		case <-streamCtx.Done():
			if detached.Load() {
				return true, nil
			}
			return false, streamCtx.Err()
		case now := <-tick.C:
			if now.After(nextSize) {
				width, height, sizeErr := term.GetSize(fd)
				if sizeErr != nil {
					return false, sizeErr
				}
				if width != cols || height != rows {
					if err := resize(width, height); err != nil {
						return false, err
					}
					cols, rows = width, height
				}
				nextSize = now.Add(200 * time.Millisecond)
			}
			var batch daemon.TerminalAttachResult
			if err := client.Call(streamCtx, "terminal.attach_read", params, &batch); err != nil {
				if detached.Load() {
					return true, nil
				}
				return false, err
			}
			if len(batch.Data) > 0 {
				if err := writeRawAttachOutput(streamCtx, fd, batch.Data); err != nil {
					if detached.Load() {
						return true, nil
					}
					return false, err
				}
			}
			params.After = batch.Through
			if batch.Exited {
				return false, nil
			}
		}
	}
}

// A stopped or unread physical terminal must not prevent cancellation or
// termios restoration. Raw writes preserve O_NONBLOCK and acknowledge output
// only after every byte has reached the terminal driver.
func writeRawAttachOutput(ctx context.Context, fd int, data []byte) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := unix.Write(fd, data)
		if n > 0 {
			data = data[n:]
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil && !terminalio.IsWouldBlock(err) {
			return err
		}
		if terminalio.IsWouldBlock(err) || (n == 0 && len(data) > 0) {
			poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}}
			if _, err := unix.Poll(poll, 50); err != nil && !errors.Is(err, unix.EINTR) {
				return err
			}
		}
	}
	return nil
}

func readRawAttachInput(ctx context.Context, fd int, send func([]byte) error, detached *atomic.Bool) error {
	var filter rawAttachInput
	buffer := make([]byte, 32<<10)
	poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := unix.Poll(poll, 50)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if n == 0 {
			if data := filter.flush(); len(data) > 0 {
				if err := send(data); err != nil {
					return err
				}
			}
			continue
		}
		count, err := terminalio.Read(fd, buffer)
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		data, detach := filter.feed(buffer[:count])
		if len(data) > 0 {
			if err := send(data); err != nil {
				return err
			}
		}
		if detach {
			detached.Store(true)
			return nil
		}
	}
}
