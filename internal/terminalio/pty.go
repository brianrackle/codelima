//go:build darwin || linux

// Package terminalio owns portable PTY descriptor and bounded writer operations.
package terminalio

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type WriteTarget interface {
	Write([]byte) (int, error)
	Close() error
	Fd() uintptr
}

type Writer struct {
	target       WriteTarget
	waitWritable func(fd int) error
	onError      func(error)

	mu     sync.Mutex
	cond   *sync.Cond
	queue  bytes.Buffer
	closed bool
	active bool
	done   chan struct{}
}

const QueueMaxBytes = 8 * 1024 * 1024

var ErrQueueFull = errors.New("terminal input queue is full; input was not admitted")

// Descriptor observes an os.File's descriptor without calling
// File.Fd. Go's File.Fd switches a pollable descriptor back to blocking mode;
// PTY pumps and writers require the O_NONBLOCK set at terminal startup to stay
// in force for their bounded poll loops.
func Descriptor(file *os.File) (int, error) {
	if file == nil {
		return -1, os.ErrInvalid
	}
	raw, err := file.SyscallConn()
	if err != nil {
		return -1, err
	}
	fd := -1
	if err := raw.Control(func(value uintptr) {
		fd = int(value)
	}); err != nil {
		return -1, err
	}
	if fd < 0 {
		return -1, os.ErrInvalid
	}
	return fd, nil
}

// Resize changes terminal geometry without File.Fd switching a pollable PTY
// back to blocking mode. Control keeps the descriptor alive through the ioctl,
// so a concurrent close cannot make this operation touch a reused descriptor.
func Resize(file *os.File, size *unix.Winsize) error {
	if file == nil || size == nil {
		return os.ErrInvalid
	}
	raw, err := file.SyscallConn()
	if err != nil {
		return err
	}
	var resizeErr error
	if err := raw.Control(func(fd uintptr) {
		resizeErr = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, size)
	}); err != nil {
		return err
	}
	return resizeErr
}

// Read preserves os.File.Read's EOF contract while using a raw read
// so the caller can observe EAGAIN without File.Fd restoring blocking mode.
func Read(fd int, buffer []byte) (int, error) {
	n, err := unix.Read(fd, buffer)
	if n == 0 && err == nil {
		return 0, io.EOF
	}
	return n, err
}

func writeTargetDescriptor(target WriteTarget) (int, error) {
	if file, ok := target.(*os.File); ok {
		return Descriptor(file)
	}
	if target == nil {
		return -1, os.ErrInvalid
	}
	return int(target.Fd()), nil
}

func NewWriter(target WriteTarget, waitWritable func(fd int) error, onError func(error)) *Writer {
	writer := &Writer{
		target:       target,
		waitWritable: waitWritable,
		onError:      onError,
		done:         make(chan struct{}),
	}
	writer.cond = sync.NewCond(&writer.mu)
	go writer.loop()
	return writer
}

func (w *Writer) Enqueue(data []byte) bool {
	if w == nil || len(data) == 0 {
		return false
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || len(data) > QueueMaxBytes-w.queue.Len() {
		return false
	}
	_, _ = w.queue.Write(data)
	w.cond.Signal()
	return true
}

func (w *Writer) Close() {
	if w == nil {
		return
	}

	w.mu.Lock()
	alreadyClosed := w.closed
	w.closed = true
	w.queue.Reset()
	w.cond.Broadcast()
	target := w.target
	done := w.done
	w.mu.Unlock()

	if !alreadyClosed && target != nil {
		_ = target.Close()
	}
	<-done
}

func (w *Writer) loop() {
	defer close(w.done)

	for {
		chunk, ok := w.nextChunk()
		if !ok {
			return
		}
		err := WriteAll(w.target, chunk, w.waitWritable)
		w.mu.Lock()
		w.active = false
		w.cond.Broadcast()
		w.mu.Unlock()
		if err != nil {
			if w.isClosed() || IsClosedError(err) {
				return
			}
			if w.onError != nil {
				w.onError(err)
			}
			return
		}
	}
}

func (w *Writer) nextChunk() ([]byte, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	for w.queue.Len() == 0 && !w.closed {
		w.cond.Wait()
	}
	if w.queue.Len() == 0 {
		return nil, false
	}

	chunk := slices.Clone(w.queue.Bytes())
	w.queue.Reset()
	w.active = true
	return chunk, true
}

func (w *Writer) Drain(timeout time.Duration) error {
	if w == nil {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for {
		w.mu.Lock()
		empty := w.queue.Len() == 0 && !w.active
		closed := w.closed
		w.mu.Unlock()
		if empty || closed {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out draining terminal writes")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (w *Writer) isClosed() bool {
	if w == nil {
		return true
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closed
}

func WriteAll(target WriteTarget, data []byte, waitWritable func(fd int) error) error {
	if target == nil || len(data) == 0 {
		return nil
	}

	for len(data) > 0 {
		n, err := target.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if len(data) == 0 && err == nil {
			return nil
		}
		if err == nil {
			if n == 0 {
				if waitWritable == nil {
					return io.ErrShortWrite
				}
				fd, descriptorErr := writeTargetDescriptor(target)
				if descriptorErr != nil {
					return descriptorErr
				}
				if err := waitWritable(fd); err != nil {
					return err
				}
			}
			continue
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if IsWouldBlock(err) {
			if waitWritable == nil {
				continue
			}
			fd, descriptorErr := writeTargetDescriptor(target)
			if descriptorErr != nil {
				return descriptorErr
			}
			if err := waitWritable(fd); err != nil {
				return err
			}
			continue
		}
		return err
	}

	return nil
}

func WaitWritable(fd int) error {
	if fd < 0 {
		return unix.EBADF
	}
	for {
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT | unix.POLLHUP | unix.POLLERR}}
		// Keep this wait bounded. Handoff PTYs are nonblocking, and closing an
		// fd concurrently with poll is not guaranteed to wake on every Unix;
		// returning after one beat lets the write loop observe EBADF/closed.
		_, err := unix.Poll(fds, 100)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		return err
	}
}

func IsWouldBlock(err error) bool {
	return errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK)
}

func IsClosedError(err error) bool {
	return errors.Is(err, os.ErrClosed) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, unix.EBADF) ||
		errors.Is(err, unix.EIO)
}

func WaitReadable(fd int, timeout time.Duration) error {
	if fd < 0 {
		return os.ErrClosed
	}
	timeoutMS := int(timeout / time.Millisecond)
	for {
		pollFDs := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN | unix.POLLHUP | unix.POLLERR}}
		_, err := unix.Poll(pollFDs, timeoutMS)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		return err
	}
}

// IsClosedReadError reports whether a read error means the PTY was
// closed under us (no exit status to report), matching the pre-actor readLoop's
// os.ErrClosed branch.
func IsClosedReadError(err error) bool {
	return errors.Is(err, os.ErrClosed)
}

// WritableWaiter exposes the injected readiness strategy for characterization tests.
func (w *Writer) WritableWaiter() func(int) error { return w.waitWritable }
